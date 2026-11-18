package server

// One-click database major-version upgrades (see api/types_dbupgrade.go and
// agent/backup/upgrade.go). The agent converts the data with the service stopped; the server
// then deploys the new image, and rolls back on its own when that deployment fails.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/registry"
)

// dbUpgradeDeployWait bounds the wait for the new version's deployment.
const dbUpgradeDeployWait = 10 * time.Minute

// Step keys of an upgrade (the panel names them).
const (
	dbStepConvert = "convert" // stop, dump with the old version, load with the new one, compare
	dbStepDeploy  = "deploy"  // deploy the new image
)

func (s *Server) agentDBUpgrade(nodeID string) bool {
	c := s.hub.get(nodeID)
	return c != nil && c.dbUpgrade.Load()
}

// dbUpgradeBlocker says why svc cannot be upgraded with one click ("" when it can).
func (s *Server) dbUpgradeBlocker(ctx context.Context, svc *api.Service) string {
	switch {
	case !api.DBUpgradeKinds[svc.Kind]:
		return "only PostgreSQL services have a one-click major upgrade"
	case svc.Git != nil:
		return "the service is built from source"
	case len(svc.Volumes) == 0:
		return "the service has no volume"
	}
	for _, v := range svc.Volumes {
		if v.HostPath != "" {
			return "a volume mounts a host directory"
		}
	}
	node, err := s.serviceNode(ctx, svc)
	if err != nil || !s.hub.Online(node) {
		return "the service's server is offline"
	}
	if !s.agentDBUpgrade(node) {
		return "the agent on the service's server is too old; upgrade it first"
	}
	return ""
}

func (s *Server) handleGetDBUpgrade(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	svc, _, err := s.st.GetService(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	info := api.DBUpgradeInfo{}
	info.Reason = s.dbUpgradeBlocker(ctx, svc)
	info.Supported = info.Reason == ""
	if info.Last, err = s.st.GetDBUpgrade(ctx, svc.ID); err != nil {
		return err
	}
	writeJSON(w, 200, info)
	return nil
}

// majorUpgradeRef checks tag is a higher major of the service's current image tag.
func majorUpgradeRef(image, tag string) (registry.Ref, error) {
	ref, err := registry.ParseRef(image)
	if why := checkable(ref, err); why != "" {
		return ref, badRequest("cannot upgrade %s: %s", image, why)
	}
	if ref.Digest != "" && ref.Tag == "" {
		return ref, badRequest("the image is pinned to a digest; set a version tag first")
	}
	if strings.ContainsAny(tag, "/:@ ") || tag == "" {
		return ref, badRequest("tag must be a plain version tag such as 19 or 19-alpine")
	}
	cur, ok1 := registry.ParseVersion(ref.Tag)
	next, ok2 := registry.ParseVersion(tag)
	if !ok1 || !ok2 {
		return ref, badRequest("both the current tag (%s) and %s must be version tags", ref.Tag, tag)
	}
	if next.Nums[0] <= cur.Nums[0] {
		return ref, badRequest("%s is not a newer major version than %s", tag, ref.Tag)
	}
	return registry.ParseRef(ref.WithTag(tag))
}

func (s *Server) handleStartDBUpgrade(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	svc, _, err := s.st.GetService(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	var req api.DBUpgradeRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if why := s.dbUpgradeBlocker(ctx, svc); why != "" {
		return errf(409, "unsupported", "%s", why)
	}
	ref, err := majorUpgradeRef(svc.Image, strings.TrimSpace(req.Tag))
	if err != nil {
		return err
	}
	last, err := s.st.GetDBUpgrade(ctx, svc.ID)
	if err != nil {
		return err
	}
	if last != nil && last.Status == api.DBUpgradeRunning {
		return errf(409, "busy", "an upgrade of this service is running")
	}
	if last != nil && last.OldKept {
		return errf(409, "old_data_kept", "the data kept from the previous upgrade is still on the server; delete it or roll back first")
	}
	if !s.upd.lock(svc.ID) {
		return errf(409, "busy", "an image update of this service is running; try again in a moment")
	}
	cctx, cancel := context.WithTimeout(registry.WithFresh(ctx), 45*time.Second)
	digest, err := s.upd.reg.Digest(cctx, ref, s.registryAuthFor(ctx, svc))
	cancel()
	if err != nil {
		s.upd.unlock(svc.ID)
		return errf(502, "registry_error", "%s: %s", ref.WithTag(ref.Tag), registryErrText(err))
	}
	u := api.DBUpgrade{
		ServiceID: svc.ID, Status: api.DBUpgradeRunning, FromImage: svc.Image, ToImage: ref.WithTag(ref.Tag),
		Steps:     []api.DBUpgradeStep{{Name: dbStepConvert, Status: "pending"}, {Name: dbStepDeploy, Status: "pending"}},
		StartedAt: time.Now().UTC(), By: principalFrom(ctx).actor(),
	}
	if err := s.st.SaveDBUpgrade(ctx, u); err != nil {
		s.upd.unlock(svc.ID)
		return err
	}
	s.st.Audit(ctx, u.By, "service.db_upgrade", svc.ID, map[string]string{"from": u.FromImage, "to": u.ToImage})
	s.goBG(func(bg context.Context) {
		defer s.upd.unlock(svc.ID)
		s.runDBUpgrade(bg, svc, ref, digest, &u)
	})
	writeJSON(w, 202, u)
	return nil
}

func (s *Server) saveDBUpgrade(ctx context.Context, u *api.DBUpgrade) {
	if err := s.st.SaveDBUpgrade(ctx, *u); err != nil {
		s.log.Error("db upgrade: save", "service", u.ServiceID, "err", err)
	}
	s.publish("db_upgrade", *u)
}

func setDBStep(u *api.DBUpgrade, name, status, detail string) {
	for i := range u.Steps {
		if u.Steps[i].Name == name {
			u.Steps[i].Status, u.Steps[i].Detail = status, detail
		}
	}
}

func (s *Server) finishDBUpgrade(ctx context.Context, u *api.DBUpgrade, status, errText string) {
	now := time.Now().UTC()
	u.Status, u.Error, u.FinishedAt = status, errText, &now
	s.saveDBUpgrade(ctx, u)
	s.publishService(ctx, u.ServiceID)
}

func (s *Server) runDBUpgrade(ctx context.Context, svc *api.Service, ref registry.Ref, digest string, u *api.DBUpgrade) {
	node, err := s.serviceNode(ctx, svc)
	if err != nil {
		s.finishDBUpgrade(ctx, u, api.DBUpgradeFailed, err.Error())
		return
	}
	spec, _, err := s.buildSpec(ctx, svc, newID("dep"))
	if err != nil {
		s.finishDBUpgrade(ctx, u, api.DBUpgradeFailed, err.Error())
		return
	}
	setDBStep(u, dbStepConvert, "running", "")
	s.saveDBUpgrade(ctx, u)
	out, err := s.hub.Call(ctx, node, api.MsgDBUpgrade, api.DBUpgradePayload{
		ServiceID: svc.ID, Kind: svc.Kind, Image: ref.Pinned(digest), RegistryAuth: spec.RegistryAuth,
	}, s.cfg.TransferTimeout)
	if err != nil {
		setDBStep(u, dbStepConvert, "failed", "")
		setDBStep(u, dbStepDeploy, "skipped", "")
		s.finishDBUpgrade(ctx, u, api.DBUpgradeFailed, strings.TrimPrefix(err.Error(), api.MsgDBUpgrade+": "))
		return
	}
	var res api.DBUpgradeResult
	json.Unmarshal([]byte(out), &res)
	setDBStep(u, dbStepConvert, "done", fmt.Sprintf("%s · dump %s", res.After, humanBytes(res.DumpBytes)))
	u.OldKept = true
	setDBStep(u, dbStepDeploy, "running", "")
	s.saveDBUpgrade(ctx, u)

	d, err := s.deployDigest(ctx, svc, ref, digest, "database upgrade")
	if err == nil {
		err = s.waitDeployments(ctx, []string{d.ID}, dbUpgradeDeployWait)
	}
	if err != nil {
		setDBStep(u, dbStepDeploy, "failed", err.Error())
		if rerr := s.rollbackDBUpgrade(ctx, svc.ID, node, u.FromImage); rerr != nil {
			u.Error = fmt.Sprintf("the new version did not start (%v) and the rollback failed: %v", err, rerr)
			s.finishDBUpgrade(ctx, u, api.DBUpgradeFailed, u.Error)
			return
		}
		u.OldKept = false
		s.finishDBUpgrade(ctx, u, api.DBUpgradeRolledBack, fmt.Sprintf("the new version did not start (%v); the old version runs on the old data again", err))
		return
	}
	setDBStep(u, dbStepDeploy, "done", ref.WithTag(ref.Tag))
	s.finishDBUpgrade(ctx, u, api.DBUpgradeDone, "")
	s.goBG(func(bg context.Context) { s.checkImage(bg, svc.ID) })
}

// rollbackDBUpgrade puts the old data back and deploys the old image.
func (s *Server) rollbackDBUpgrade(ctx context.Context, serviceID, node, fromImage string) error {
	if _, err := s.hub.Call(ctx, node, api.MsgDBUpgradeRollback, api.DBUpgradeRef{ServiceID: serviceID}, 10*time.Minute); err != nil {
		return err
	}
	svc, _, err := s.st.GetService(ctx, serviceID)
	if err != nil {
		return err
	}
	svc.Image = fromImage
	if err := s.st.UpdateServiceSpec(ctx, *svc); err != nil {
		return err
	}
	d, err := s.deploy(ctx, serviceID, "db-upgrade-rollback")
	if err != nil {
		return err
	}
	return s.waitDeployments(ctx, []string{d.ID}, dbUpgradeDeployWait)
}

// handleDBUpgradeRollback (POST .../db-upgrade/rollback) returns to the version before the
// last upgrade, discarding what was written since.
func (s *Server) handleDBUpgradeRollback(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	svc, last, node, err := s.keptDBUpgrade(r)
	if err != nil {
		return err
	}
	if !s.upd.lock(svc.ID) {
		return errf(409, "busy", "an image update of this service is running; try again in a moment")
	}
	defer s.upd.unlock(svc.ID)
	if err := s.rollbackDBUpgrade(ctx, svc.ID, node, last.FromImage); err != nil {
		return errf(502, "rollback_failed", "%v", err)
	}
	last.OldKept = false
	s.finishDBUpgrade(ctx, last, api.DBUpgradeRolledBack, "rolled back by "+principalFrom(ctx).actor())
	s.st.Audit(ctx, principalFrom(ctx).actor(), "service.db_upgrade_rollback", svc.ID, map[string]string{"image": last.FromImage})
	s.goBG(func(bg context.Context) { s.checkImage(bg, svc.ID) })
	writeJSON(w, 200, last)
	return nil
}

// handleDBUpgradeCleanup (DELETE .../db-upgrade/old) deletes the data kept by the last upgrade.
func (s *Server) handleDBUpgradeCleanup(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	svc, last, node, err := s.keptDBUpgrade(r)
	if err != nil {
		return err
	}
	if _, err := s.hub.Call(ctx, node, api.MsgDBUpgradeCleanup, api.DBUpgradeRef{ServiceID: svc.ID}, time.Minute); err != nil {
		return errf(502, "cleanup_failed", "%v", err)
	}
	last.OldKept = false
	s.saveDBUpgrade(ctx, last)
	s.st.Audit(ctx, principalFrom(ctx).actor(), "service.db_upgrade_cleanup", svc.ID, nil)
	writeJSON(w, 200, last)
	return nil
}

func (s *Server) keptDBUpgrade(r *http.Request) (*api.Service, *api.DBUpgrade, string, error) {
	ctx := r.Context()
	svc, _, err := s.st.GetService(ctx, r.PathValue("id"))
	if err != nil {
		return nil, nil, "", err
	}
	last, err := s.st.GetDBUpgrade(ctx, svc.ID)
	if err != nil {
		return nil, nil, "", err
	}
	if last == nil || !last.OldKept {
		return nil, nil, "", errf(409, "nothing_kept", "no data from before an upgrade is kept for this service")
	}
	if last.Status == api.DBUpgradeRunning {
		return nil, nil, "", errf(409, "busy", "an upgrade of this service is running")
	}
	node, err := s.serviceNode(ctx, svc)
	if err != nil {
		return nil, nil, "", err
	}
	if !s.agentDBUpgrade(node) {
		return nil, nil, "", errf(409, "offline", "the service's server is offline or its agent is too old")
	}
	return svc, last, node, nil
}

// interruptDBUpgrades marks upgrades a control server restart cut off as failed.
func (s *Server) interruptDBUpgrades() {
	if err := s.st.InterruptDBUpgrades(context.Background(), time.Now()); err != nil {
		s.log.Error("db upgrades: mark interrupted", "err", err)
	}
}
