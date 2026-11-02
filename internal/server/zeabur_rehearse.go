package server

// Zeabur takeover: rehearse (copy data and start services side by side without taking traffic).

import (
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
	j, err := s.startZeaburOp(r.Context(), r.PathValue("id"), []string{zsPlanned, zsRehearsed, zsFailed, zsRolledBack}, zsRehearsing, (*zrun).rehearse)
	if err != nil {
		return err
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "import.rehearse", j.ID, nil)
	writeJSON(w, 200, j)
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
