package server

// Zeabur takeover: rehearse (copy data and start services side by side without taking traffic).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ---------- rehearse ----------

func (s *Server) handleRehearse(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var req api.ZeaburRehearseRequest
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil {
			return err
		}
	}
	allowed := []string{zsPlanned, zsRehearsed, zsFailed, zsRolledBack}
	if !req.IgnoreMemory {
		if err := s.checkRehearseMemory(ctx, r.PathValue("id"), allowed); err != nil {
			return err
		}
	}
	j, err := s.startZeaburOp(ctx, r.PathValue("id"), allowed, zsRehearsing, (*zrun).rehearse)
	if err != nil {
		return err
	}
	s.st.Audit(ctx, principalFrom(ctx).actor(), "import.rehearse", j.ID, map[string]any{"ignore_memory": req.IgnoreMemory})
	writeJSON(w, 200, j)
	return nil
}

// rehearseMemDefaultMB is the memory a service is assumed to need when the scan has no pod
// usage for it (no metrics-server), by Atoll kind.
var rehearseMemDefaultMB = map[string]int{"postgres": 256, "mysql": 400, "mongo": 300, "redis": 64}

const rehearseMemDefaultAppMB = 128

// estimateRehearseMB estimates the memory the job's Atoll copies need on top of what runs now:
// each service not already running in Atoll counts its Zeabur pods' current use (scan), or
// the default for its kind. It also returns how the estimate was made.
func estimateRehearseMB(meta *zeaburMeta, inv *api.ZeaburInventory, running func(atollID string) bool) (int, []string) {
	usage := map[string]int{}
	for _, p := range inv.Projects {
		if p.Namespace != meta.Namespace {
			continue
		}
		for _, sv := range p.Services {
			usage[sv.ServiceID] = sv.MemUsageMB
		}
	}
	total := 0
	var parts []string
	for _, sv := range meta.Services {
		if running(sv.AtollID) {
			continue
		}
		mb, how := usage[sv.SourceServiceID], "pod usage"
		if mb <= 0 {
			mb, how = rehearseMemDefaultMB[sv.Kind], "default"
			if mb == 0 {
				mb = rehearseMemDefaultAppMB
			}
		}
		total += mb
		parts = append(parts, fmt.Sprintf("%s %d MB (%s)", sv.Name, mb, how))
	}
	return total, parts
}

// checkRehearseMemory refuses (409 insufficient_memory) when the server's available memory is
// below the estimate: Zeabur's copy keeps running next to Atoll's during the rehearsal.
func (s *Server) checkRehearseMemory(ctx context.Context, id string, allowed []string) error {
	j, meta, err := s.loadZeaburJob(ctx, id)
	if err != nil {
		return err
	}
	ok := false
	for _, a := range allowed {
		ok = ok || j.Status == a
	}
	if !ok || !s.hub.Online(j.ServerID) {
		return nil // startZeaburOp answers with the precise error
	}
	inv, err := s.scanZeabur(ctx, j.ServerID, true)
	if err != nil {
		return err
	}
	if inv.MemFreeMB <= 0 {
		return nil // not reported
	}
	need, parts := estimateRehearseMB(meta, inv, func(atollID string) bool {
		svc, stopped, err := s.st.GetService(ctx, atollID)
		return err == nil && !stopped && svc.Status == "running"
	})
	if need > inv.MemFreeMB {
		return errf(409, "insufficient_memory", "rehearsal needs about %d MB of memory (%s) but only %d MB is available on the server; Zeabur keeps running next to the Atoll copies. Free memory (rehearse projects one at a time, stop other Atoll copies) or pass ignore_memory to override", need, strings.Join(parts, ", "), inv.MemFreeMB)
	}
	return nil
}

func (z *zrun) rehearse() error {
	z.resetSteps(rehearsalSteps)
	err := z.doRehearse()
	if err != nil {
		z.setStatus(zsFailed)
		return err
	}
	z.setStatus(zsRehearsed)
	return nil
}

func (z *zrun) doRehearse() error {
	s, ctx := z.s, z.ctx
	if err := z.st.do(stepResources, func() (string, error) {
		var total int64
		for _, sv := range z.meta.Services {
			for _, v := range sv.Volumes {
				total += v.Bytes
			}
		}
		inv, err := s.scanZeabur(ctx, z.node, true)
		if err != nil {
			return "", err
		}
		free := int64(inv.DiskFreeGB * 1e9)
		if free > 0 && float64(free) < 1.2*float64(total) {
			return "", fmt.Errorf("not enough disk: %s of data needs %s free, only %s available", humanBytes(total), humanBytes(int64(1.2*float64(total))), humanBytes(free))
		}
		return fmt.Sprintf("%s of data, %.1f GB disk free, %d MB memory free", humanBytes(total), inv.DiskFreeGB, inv.MemFreeMB), nil
	}); err != nil {
		return err
	}
	if err := z.st.do(stepImages, func() (string, error) {
		n, pulled := 0, 0
		for _, sv := range z.meta.Services {
			if sv.Image == "" {
				continue
			}
			// Zeabur-built images only exist in containerd; registry images are pulled by
			// their original ref (tag kept), with containerd as the agent's fallback.
			pull := !sv.ZeaburRegistry
			ref, err := s.hub.Call(ctx, z.node, api.MsgZeaburImage, api.ZeaburImagePayload{Image: sv.Image, ServiceID: sv.AtollID, Pull: pull}, s.cfg.TransferTimeout)
			if err != nil {
				return "", fmt.Errorf("%s: %w", sv.Name, err)
			}
			ref = strings.TrimSpace(ref)
			if ref == "" {
				return "", fmt.Errorf("%s: agent returned no image reference", sv.Name)
			}
			if err := z.setImage(sv.AtollID, ref); err != nil {
				return "", err
			}
			if pull {
				pulled++
			} else {
				n++
			}
		}
		return fmt.Sprintf("%d Zeabur-built image(s) exported from containerd; %d registry image(s) pulled with their original tag", n, pulled), nil
	}); err != nil {
		return err
	}
	if err := z.st.do(stepVolumes, func() (string, error) { return z.copyVolumes() }); err != nil {
		return err
	}
	if err := z.st.do(stepDatabases, func() (string, error) {
		if err := z.deployAndWait(true); err != nil {
			return "", err
		}
		msg, mismatched, err := z.copyDatabases()
		if err != nil {
			return "", err
		}
		if mismatched > 0 {
			msg += fmt.Sprintf("; %d table(s) differ because Zeabur is still serving writes — the final sync at cutover must match", mismatched)
		}
		return msg, nil
	}); err != nil {
		return err
	}
	since := time.Now()
	if err := z.st.do(stepStart, func() (string, error) {
		if err := z.deployAndWait(false); err != nil {
			return "", err
		}
		return fmt.Sprintf("%d service(s) running without public ingress; Zeabur keeps serving", len(z.meta.Services)), nil
	}); err != nil {
		return err
	}
	return z.st.do(stepVerify, func() (string, error) {
		if err := s.waitHealthy(ctx, z.serviceIDs(nil), since, s.cfg.VerifyTimeout); err != nil {
			return "", err
		}
		return fmt.Sprintf("all services healthy; %d table/keyspace count(s) recorded", len(z.job.Verify)), nil
	})
}

func (z *zrun) serviceIDs(filter func(zeaburSvcMeta) bool) []string {
	var ids []string
	for _, sv := range z.meta.Services {
		if filter == nil || filter(sv) {
			ids = append(ids, sv.AtollID)
		}
	}
	return ids
}

func (z *zrun) setImage(serviceID, ref string) error {
	svc, _, err := z.s.st.GetService(z.ctx, serviceID)
	if err != nil {
		return err
	}
	if svc.Image == ref {
		return nil
	}
	svc.Image = ref
	return z.s.st.UpdateServiceSpec(z.ctx, *svc)
}

// deployAndWait deploys the database services (dbs=true) or the others and waits for them.
func (z *zrun) deployAndWait(dbs bool) error {
	var ids []string
	for _, sv := range z.meta.Services {
		if sv.isDB() != dbs {
			continue
		}
		d, err := z.s.deploy(z.ctx, sv.AtollID, "import")
		if err != nil {
			return fmt.Errorf("%s: %w", sv.Name, err)
		}
		ids = append(ids, d.ID)
	}
	return z.s.waitDeployments(z.ctx, ids, z.s.cfg.DeployWaitTimeout)
}

// copyVolumes mirrors the PVC dirs of non-database services (databases are dumped instead).
func (z *zrun) copyVolumes() (string, error) {
	n, total := 0, int64(0)
	for _, sv := range z.meta.Services {
		if sv.isDB() {
			continue
		}
		for _, v := range sv.Volumes {
			if v.HostDir == "" {
				continue
			}
			out, err := z.s.hub.Call(z.ctx, z.node, api.MsgZeaburVolume, api.ZeaburVolumePayload{HostDir: v.HostDir, ServiceID: sv.AtollID, Volume: v.Volume}, z.s.cfg.TransferTimeout)
			if err != nil {
				return "", fmt.Errorf("%s/%s: %w", sv.Name, v.Volume, err)
			}
			var res struct {
				Bytes int64 `json:"bytes"`
			}
			json.Unmarshal([]byte(out), &res)
			total += res.Bytes
			n++
		}
	}
	if n == 0 {
		return "no app volumes", nil
	}
	return fmt.Sprintf("%d volume(s), %s copied", n, humanBytes(total)), nil
}

// copyDatabases dumps every database service from its Zeabur pod and restores it into the
// Atoll container, recording row counts in job.Verify. It returns how many tables differ.
func (z *zrun) copyDatabases() (string, int, error) {
	var verify []api.ImportVerify
	n, mismatched := 0, 0
	for _, sv := range z.meta.Services {
		if !sv.isDB() {
			continue
		}
		res, err := z.dbSync(sv)
		if err != nil {
			return "", 0, err
		}
		n++
		verify = append(verify, verifyRows(sv.Name, res)...)
		if !res.Match {
			mismatched += max(1, len(res.Mismatches))
		}
	}
	z.job.Verify = verify
	z.save()
	if n == 0 {
		return "no databases", 0, nil
	}
	return fmt.Sprintf("%d database(s) dumped and restored, %d table count(s) compared", n, len(verify)), mismatched, nil
}

func (z *zrun) dbSync(sv zeaburSvcMeta) (*dbResult, error) {
	out, err := z.s.hub.Call(z.ctx, z.node, api.MsgZeaburDB, api.ZeaburDBPayload{Namespace: z.meta.Namespace, SourceServiceID: sv.SourceServiceID, Kind: sv.Kind, TargetServiceID: sv.AtollID}, z.s.cfg.TransferTimeout)
	var res dbResult
	if out != "" {
		if jerr := json.Unmarshal([]byte(out), &res); jerr != nil && err == nil {
			return nil, fmt.Errorf("%s: invalid zeabur.db result: %v", sv.Name, jerr)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sv.Name, err)
	}
	return &res, nil
}

func verifyRows(service string, res *dbResult) []api.ImportVerify {
	keys := map[string]bool{}
	for k := range res.Source {
		keys[k] = true
	}
	for k := range res.Target {
		keys[k] = true
	}
	ks := make([]string, 0, len(keys))
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	out := make([]api.ImportVerify, 0, len(ks))
	for _, k := range ks {
		out = append(out, api.ImportVerify{Service: service, Table: k, SourceRows: res.Source[k], TargetRows: res.Target[k]})
	}
	return out
}
