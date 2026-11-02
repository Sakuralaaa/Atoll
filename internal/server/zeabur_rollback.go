package server

// Zeabur takeover: rollback (undo a cutover) and cleanup (remove Zeabur once the user is done).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ---------- rollback ----------

func (s *Server) handleZeaburRollback(w http.ResponseWriter, r *http.Request) error {
	_, meta, err := s.loadZeaburJob(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if meta.RollbackDisabled {
		return errf(409, "rollback_disabled", "Zeabur was removed from this server; rollback is no longer possible")
	}
	j, err := s.startZeaburOp(r.Context(), r.PathValue("id"), []string{zsLive, zsFailed, zsRehearsed}, zsRollingBack, func(z *zrun) error {
		if err := z.doRollback(); err != nil {
			z.setStatus(zsFailed)
			return err
		}
		z.setStatus(zsRolledBack)
		return nil
	})
	if err != nil {
		return err
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "import.rollback", j.ID, nil)
	writeJSON(w, 200, j)
	return nil
}

// doRollback stops Atoll's copies, restores Zeabur's ingress and scales Zeabur back.
// It keeps going after errors so as much as possible is restored, then reports them all.
func (z *zrun) doRollback() error {
	s, ctx := z.s, z.ctx
	var errs []string
	if err := s.st.SetIngressHold(ctx, z.job.ProjectID, true); err != nil {
		errs = append(errs, err.Error())
	}
	if err := s.pushIngressSync(ctx, z.node); err != nil {
		errs = append(errs, "withdraw Atoll routes: "+err.Error())
		if len(z.meta.CreatedRecords) > 0 {
			errs = append(errs, "DNS records created by the cutover kept until the routes are withdrawn (run rollback again)")
		}
	} else if err := z.removeCreatedRecords(); err != nil {
		// routes first, records second: only once Caddy no longer serves the names
		errs = append(errs, err.Error())
	}
	for _, sv := range z.meta.Services {
		s.st.SetDesiredStopped(ctx, sv.AtollID, true)
		if _, err := s.hub.Call(ctx, z.node, api.MsgAction, api.ActionPayload{ServiceID: sv.AtollID, Action: "stop"}, 2*s.cfg.RequestTimeout); err != nil {
			errs = append(errs, "stop "+sv.Name+": "+err.Error())
		} else {
			s.st.SetServiceStatus(ctx, sv.AtollID, "stopped", "0/1")
			s.publishService(ctx, sv.AtollID)
		}
	}
	if z.meta.IngressParked {
		if _, err := s.hub.Call(ctx, z.node, api.MsgZeaburIngress, api.ZeaburIngressPayload{Park: false}, 5*time.Minute); err != nil {
			errs = append(errs, "restore Zeabur ingress: "+err.Error())
		} else {
			z.meta.IngressParked = false
		}
	}
	// databases first, then apps
	order := append([]zeaburSvcMeta{}, z.meta.Services...)
	sort.SliceStable(order, func(i, j int) bool { return order[i].isDB() && !order[j].isDB() })
	scaled := map[string]bool{}
	for _, k := range z.meta.Scaled {
		scaled[k] = true
	}
	var left []string
	for _, sv := range order {
		key := z.meta.Namespace + "/" + sv.Workload
		if sv.Workload == "" || !scaled[key] {
			continue
		}
		if _, err := s.hub.Call(ctx, z.node, api.MsgZeaburScale, api.ZeaburScalePayload{Namespace: z.meta.Namespace, Workload: sv.Workload, Replicas: -1}, 3*time.Minute); err != nil {
			errs = append(errs, "scale back "+sv.Workload+": "+err.Error())
			left = append(left, key)
		}
	}
	z.meta.Scaled = left
	// record the outcome on the cutover steps (no extra steps: the UI shows 6 + 4)
	for _, n := range cutoverSteps {
		i := z.st.idx(n)
		if z.job.Steps[i].Status == "done" || z.job.Steps[i].Status == "running" {
			z.job.Steps[i].Status = "skipped"
			z.job.Steps[i].Detail = "rolled back"
		}
	}
	z.save()
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// ---------- cleanup ----------

func (s *Server) handleZeaburCleanup(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var req api.ZeaburCleanupRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if p := principalFrom(ctx); !p.Session && p.RequireApproval {
		return errf(403, "approval_required", "removing Zeabur cannot be queued for approval; run it from the panel or with a token without require_approval")
	}
	j, meta, err := s.loadZeaburJob(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	if j.Status != zsLive || meta.Cutovers < 1 {
		return errf(409, "bad_state", "Zeabur can only be removed after a successful cutover (import is %s)", j.Status)
	}
	node, err := s.st.GetNode(ctx, j.ServerID)
	if err != nil {
		return err
	}
	if req.Confirm != node.Name {
		return badRequest("confirm must equal the server name %q exactly", node.Name)
	}
	actor := principalFrom(ctx).actor()
	out, err := s.startZeaburOp(ctx, j.ID, []string{zsLive}, zsCleaning, func(z *zrun) error {
		res, err := s.hub.Call(z.ctx, z.node, api.MsgZeaburUninstall, api.ZeaburUninstallPayload{KeepBackup: req.KeepBackup}, s.cfg.TransferTimeout)
		if err != nil {
			z.setStatus(zsLive)
			return err
		}
		var u struct {
			Backups  []string `json:"backups"`
			ScriptOK bool     `json:"script_ok"`
			Warnings []string `json:"warnings"`
		}
		json.Unmarshal([]byte(res), &u)
		note := fmt.Sprintf("Zeabur k3s removed from %s (%d PVC backup(s) kept under /var/lib/atoll/zeabur-backup)", node.Name, len(u.Backups))
		z.job.Plan.Warnings = append(z.job.Plan.Warnings, note)
		z.job.Plan.Warnings = append(z.job.Plan.Warnings, u.Warnings...)
		z.meta.RollbackDisabled = true
		z.meta.Scaled = nil
		z.meta.IngressParked = false
		z.setStatus(zsCleaned)
		s.st.Audit(z.ctx, actor, "import.cleanup.done", j.ID, map[string]any{"script_ok": u.ScriptOK})
		return nil
	})
	if err != nil {
		return err
	}
	s.st.Audit(ctx, actor, "import.cleanup", j.ID, map[string]any{"keep_backup": req.KeepBackup})
	writeJSON(w, 200, out)
	return nil
}
