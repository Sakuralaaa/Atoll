package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// Disk usage and soft volume limits (v0.1.48). Agents measure; the control server stores the
// newest numbers, keeps a short history, raises volume_full at api.VolumeWarnRatio of a limit
// and stops a service whose volume went over a limit set to "stop". Measuring every
// api.DiskUsageEvery is coarse, so services near a limit are measured every DiskWatchEvery.

// DiskWatchEvery is how often services near a volume limit are measured again.
var DiskWatchEvery = time.Minute

// DiskStartGrace is how long a manual start keeps a service with a full volume running, so the
// owner can clean up from inside the container.
const DiskStartGrace = time.Hour

// maxVolumeLimitMB bounds PUT .../limit (1 PB).
const maxVolumeLimitMB = 1 << 30

func (s *Server) agentDiskUsage(nodeID string) bool {
	c := s.hub.get(nodeID)
	return c != nil && c.diskUsage.Load()
}

// handleDiskUsage stores an agent's measurement and enforces limits of the services in it.
func (s *Server) handleDiskUsage(ctx context.Context, nodeID string, du api.DiskUsagePayload) {
	at := du.At
	if at.IsZero() || at.After(time.Now().Add(time.Minute)) {
		at = time.Now().UTC()
	}
	for _, smp := range du.Services {
		ok, err := s.st.SaveDiskSample(ctx, nodeID, at, smp)
		if err != nil {
			s.log.Warn("disk usage", "service", smp.ServiceID, "err", err)
			continue
		}
		if ok {
			s.enforceDisk(ctx, smp.ServiceID)
		}
	}
}

// overLimit returns the first mounted volume of svc whose newest measurement exceeds a limit
// set to stop ("" = none).
func (s *Server) overLimit(ctx context.Context, svc *api.Service, onlyStop bool) (string, int64, int, error) {
	limits, err := s.st.VolumeLimits(ctx, svc.ID)
	if err != nil || len(limits) == 0 {
		return "", 0, 0, err
	}
	usage, err := s.st.VolumeUsage(ctx, svc.ID)
	if err != nil {
		return "", 0, 0, err
	}
	used := map[string]int64{}
	for _, u := range usage {
		used[u.Volume] = u.Bytes
	}
	for _, v := range svc.Volumes {
		l, ok := limits[v.Name]
		if !ok || v.HostPath != "" || (onlyStop && l.OverLimit != api.OverLimitStop) {
			continue
		}
		if b, ok := used[v.Name]; ok && b > int64(l.LimitMB)<<20 {
			return v.Name, b, l.LimitMB, nil
		}
	}
	return "", 0, 0, nil
}

// enforceDisk stops a running service whose volume is over a "stop" limit, unless a manual start
// gave it a grace period.
func (s *Server) enforceDisk(ctx context.Context, serviceID string) {
	svc, _, err := s.st.GetService(ctx, serviceID)
	if err != nil {
		return
	}
	if stopped, _ := s.st.DesiredStopped(ctx, serviceID); stopped {
		return
	}
	vol, bytes, limit, err := s.overLimit(ctx, svc, true)
	if err != nil || vol == "" {
		return
	}
	now := time.Now()
	if e, _ := s.st.GetDiskEnforcement(ctx, serviceID); now.Before(e.GraceUntil) {
		return
	}
	if _, err := s.serviceAction(ctx, svc, "stop"); err != nil {
		s.log.Warn("stop for full volume", "service", serviceID, "err", err)
		return
	}
	s.st.MarkDiskStopped(ctx, serviceID, now)
	s.st.Audit(ctx, "system", "service.stop", serviceID, map[string]any{"reason": "volume_full", "volume": vol, "bytes": bytes, "limit_mb": limit})
	s.log.Warn("service stopped: volume over limit", "service", serviceID, "volume", vol, "bytes", bytes, "limit_mb", limit)
}

// diskGraceOnStart gives a service that is started by hand while a volume is over its limit
// DiskStartGrace before it is stopped again.
func (s *Server) diskGraceOnStart(ctx context.Context, svc *api.Service) {
	if vol, _, _, err := s.overLimit(ctx, svc, true); err == nil && vol != "" {
		s.st.SetDiskGrace(ctx, svc.ID, time.Now().Add(DiskStartGrace))
	}
}

// startDiskWatcher measures services near a limit every DiskWatchEvery.
func (s *Server) startDiskWatcher() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(DiskWatchEvery)
		defer t.Stop()
		for {
			select {
			case <-s.bg.Done():
				return
			case <-t.C:
				s.watchDisk(s.bg)
			}
		}
	}()
}

var diskMeasuring sync.Map // node id → in flight

func (s *Server) watchDisk(ctx context.Context) {
	near, err := s.st.VolumesNearLimit(ctx)
	if err != nil {
		return
	}
	byNode := map[string][]string{}
	seen := map[string]bool{}
	for _, v := range near {
		if !seen[v.ServiceID] {
			seen[v.ServiceID] = true
			byNode[v.ServerID] = append(byNode[v.ServerID], v.ServiceID)
		}
	}
	for node, ids := range byNode {
		s.measureDisk(ctx, node, ids)
	}
}

// measureDisk asks a node's agent for a measurement now (no-op for old or offline agents, or
// while one is running) and stores it.
func (s *Server) measureDisk(ctx context.Context, nodeID string, ids []string) {
	if !s.agentDiskUsage(nodeID) {
		return
	}
	if _, busy := diskMeasuring.LoadOrStore(nodeID, true); busy {
		return
	}
	defer diskMeasuring.Delete(nodeID)
	res, err := s.hub.Call(ctx, nodeID, api.MsgDiskMeasure, api.DiskMeasurePayload{ServiceIDs: ids}, 2*time.Minute)
	if err != nil {
		s.log.Debug("disk measure", "node", nodeID, "err", err)
		return
	}
	var du api.DiskUsagePayload
	if json.Unmarshal([]byte(res), &du) == nil {
		s.handleDiskUsage(ctx, nodeID, du)
	}
}

// GET /services/{id}/disk?range=1h|24h|7d
func (s *Server) handleServiceDisk(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	since, step, err := rangeStep(r.URL.Query().Get("range"), 7*24*time.Hour)
	if err != nil {
		return err
	}
	out, err := s.serviceDisk(r.Context(), svc)
	if err != nil {
		return err
	}
	if out.History, err = s.st.DiskHistory(r.Context(), svc.ID, since, step); err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) serviceDisk(ctx context.Context, svc *api.Service) (*api.ServiceDisk, error) {
	usage, err := s.st.VolumeUsage(ctx, svc.ID)
	if err != nil {
		return nil, err
	}
	limits, err := s.st.VolumeLimits(ctx, svc.ID)
	if err != nil {
		return nil, err
	}
	node, _ := s.serviceNode(ctx, svc)
	out := &api.ServiceDisk{Supported: s.agentDiskUsage(node) || len(usage) > 0, RootfsBytes: -1, Volumes: []api.VolumeDisk{}}
	used := map[string]store.VolumeUsageRow{}
	for _, u := range usage {
		used[u.Volume] = u
		if out.MeasuredAt == nil || u.At.After(*out.MeasuredAt) {
			t := u.At
			out.MeasuredAt = &t
		}
	}
	if u, ok := used[""]; ok {
		out.RootfsBytes = u.Bytes
		out.TotalBytes += u.Bytes
	}
	mounted := map[string]bool{}
	for _, v := range svc.Volumes {
		mounted[v.Name] = true
		vd := api.VolumeDisk{Name: v.Name, MountPath: v.MountPath, HostPath: v.HostPath != ""}
		if u, ok := used[v.Name]; ok && !vd.HostPath {
			vd.Bytes, vd.Measured = u.Bytes, true
			out.TotalBytes += u.Bytes
		}
		if l, ok := limits[v.Name]; ok && !vd.HostPath {
			vd.LimitMB, vd.OverLimit = l.LimitMB, l.OverLimit
		}
		out.Volumes = append(out.Volumes, vd)
	}
	var orphans []string
	for name := range used {
		if name != "" && !mounted[name] {
			orphans = append(orphans, name)
		}
	}
	sort.Strings(orphans)
	for _, name := range orphans {
		out.Volumes = append(out.Volumes, api.VolumeDisk{Name: name, Bytes: used[name].Bytes, Measured: true, Orphan: true})
	}
	return out, nil
}

// PUT /services/{id}/volumes/{name}/limit {limit_mb, over_limit}
func (s *Server) handlePutVolumeLimit(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	svc, _, err := s.st.GetService(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	var vol *api.Volume
	for i := range svc.Volumes {
		if svc.Volumes[i].Name == name {
			vol = &svc.Volumes[i]
		}
	}
	if vol == nil {
		return errf(404, "not_found", "service has no volume %q", name)
	}
	if vol.HostPath != "" {
		return errf(400, "host_path_volume", "volume %q mounts a host directory; Atoll does not measure or limit it", name)
	}
	var req api.VolumeLimit
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.LimitMB < 0 || req.LimitMB > maxVolumeLimitMB {
		return badRequest("limit_mb must be between 0 (no limit) and %d", maxVolumeLimitMB)
	}
	if req.OverLimit == "" {
		req.OverLimit = api.OverLimitStop
	}
	if req.OverLimit != api.OverLimitStop && req.OverLimit != api.OverLimitAlert {
		return badRequest("over_limit must be stop or alert")
	}
	if err := s.st.SetVolumeLimit(ctx, svc.ID, name, req); err != nil {
		return err
	}
	s.st.Audit(ctx, principalFrom(ctx).actor(), "service.volume_limit", svc.ID, map[string]any{"volume": name, "limit_mb": req.LimitMB, "over_limit": req.OverLimit})
	if req.LimitMB > 0 {
		if node, err := s.serviceNode(ctx, svc); err == nil {
			s.goBG(func(bg context.Context) { s.measureDisk(bg, node, []string{svc.ID}) })
		}
	}
	out, err := s.serviceDisk(ctx, svc)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}
