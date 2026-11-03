package server

// Zeabur takeover, node level. Zeabur's ingress-controller is ONE DaemonSet shared by every
// Zeabur project on the server, so parking it (cutover) or restoring it (rollback) acts on the
// whole node. A cutover therefore covers one or more import jobs of the same server: each job
// scales down and syncs its data, the ingress is parked once, atoll-caddy starts with all their
// hostnames and DNS is configured per job. A cutover refuses (409 other_zeabur_projects) while a
// project outside it still serves hostnames through the ingress; a rollback of one job refuses
// (409 node_rollback_required) while other jobs of the node stay cut over.

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// nodeRun is one cutover or rollback of one or more zeabur-local jobs of one server.
type nodeRun struct {
	s     *Server
	ctx   context.Context
	node  string
	runs  []*zrun
	force bool
}

func (n *nodeRun) ids() map[string]bool {
	m := map[string]bool{}
	for _, z := range n.runs {
		m[z.job.ID] = true
	}
	return m
}

func nodeLockKey(serverID string) string { return "zeabur-node:" + serverID }

// startNodeOp validates and locks every job (and the node), runs pre synchronously (its error
// is the request's answer) and then fn in the background.
func (s *Server) startNodeOp(ctx context.Context, serverID string, ids, allowed []string, busyStatus string, pre, fn func(n *nodeRun) error) (*api.ZeaburNodeOp, error) {
	return s.startNodeOpForce(ctx, serverID, ids, allowed, busyStatus, false, pre, fn)
}

func (s *Server) startNodeOpForce(ctx context.Context, serverID string, ids, allowed []string, busyStatus string, force bool, pre, fn func(n *nodeRun) error) (*api.ZeaburNodeOp, error) {
	ids = dedupe(ids)
	if len(ids) == 0 {
		return nil, badRequest("import_ids is required")
	}
	if !s.hub.Online(serverID) {
		return nil, errf(409, "server_offline", "%v", ErrNodeOffline)
	}
	var locked []string
	unlock := func() {
		for _, k := range locked {
			s.unlockJob(k)
		}
	}
	lock := func(k, msg string) error {
		if !s.lockJob(k) {
			return errf(409, "busy", "%s", msg)
		}
		locked = append(locked, k)
		return nil
	}
	if err := lock(nodeLockKey(serverID), "another Zeabur cutover or rollback is running on this server"); err != nil {
		return nil, err
	}
	n := &nodeRun{s: s, ctx: s.bg, node: serverID, force: force}
	for _, id := range ids {
		j, meta, err := s.loadZeaburJob(ctx, id)
		if err != nil {
			unlock()
			return nil, err
		}
		if j.ServerID != serverID {
			unlock()
			return nil, badRequest("import %s belongs to another server", id)
		}
		if meta.RollbackDisabled {
			unlock()
			return nil, errf(409, "bad_state", "import %s: Zeabur was removed from this server", id)
		}
		ok := false
		for _, a := range allowed {
			ok = ok || j.Status == a
		}
		if !ok {
			unlock()
			return nil, errf(409, "bad_state", "import %s is %s; this step needs status %s", id, j.Status, strings.Join(allowed, " or "))
		}
		if err := lock(j.ID, "import "+id+" is busy"); err != nil {
			unlock()
			return nil, err
		}
		if err := lock(j.ProjectID, "another operation is running on the project of import "+id); err != nil {
			unlock()
			return nil, err
		}
		n.runs = append(n.runs, s.newZrun(s.bg, j, meta))
	}
	if pre != nil {
		pctx := n.ctx
		n.ctx = ctx
		err := pre(n)
		n.ctx = pctx
		if err != nil {
			unlock()
			return nil, err
		}
	}
	op := &api.ZeaburNodeOp{ServerID: serverID}
	for _, z := range n.runs {
		z.meta.LastError = ""
		z.setStatus(busyStatus)
		op.Jobs = append(op.Jobs, *maskJob(z.job))
	}
	s.goBG(func(bg context.Context) {
		defer unlock()
		if err := fn(n); err != nil {
			for _, z := range n.runs {
				if z.meta.LastError == "" {
					z.meta.LastError = err.Error()
				}
				z.save()
			}
			s.log.Warn("zeabur node step failed", "server", serverID, "err", err)
		}
	})
	return op, nil
}

// parkedJobs lists the zeabur-local jobs of the server, other than exclude, whose cutover
// parked the shared ingress ("imp_x (environment-…, live)").
func (s *Server) parkedJobs(ctx context.Context, serverID string, exclude map[string]bool) ([]string, error) {
	jobs, err := s.st.ListImports(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, j := range jobs {
		if j.Mode != importKindZeabur || j.ServerID != serverID || exclude[j.ID] {
			continue
		}
		_, meta, err := s.loadZeaburJob(ctx, j.ID)
		if err != nil {
			return nil, err
		}
		if meta.IngressParked && !meta.RollbackDisabled {
			out = append(out, fmt.Sprintf("%s (%s, %s)", j.ID, j.Namespace, j.Status))
		}
	}
	sort.Strings(out)
	return out, nil
}

// coveredNamespaces: the namespaces of the run plus those of jobs already cut over on the node.
func (n *nodeRun) coveredNamespaces() ([]string, error) {
	set := map[string]bool{}
	for _, z := range n.runs {
		set[z.meta.Namespace] = true
	}
	jobs, err := n.s.st.ListImports(n.ctx)
	if err != nil {
		return nil, err
	}
	ids := n.ids()
	for _, j := range jobs {
		if j.Mode != importKindZeabur || j.ServerID != n.node || ids[j.ID] {
			continue
		}
		if _, meta, err := n.s.loadZeaburJob(n.ctx, j.ID); err == nil && meta.IngressParked && !meta.RollbackDisabled {
			set[meta.Namespace] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// otherServing returns the Zeabur projects of inv outside covered whose workloads run and have
// Ingress hosts: they would go offline when the ingress is parked.
func otherServing(inv *api.ZeaburInventory, covered []string) []string {
	cov := map[string]bool{}
	for _, c := range covered {
		cov[c] = true
	}
	var out []string
	for _, p := range inv.Projects {
		if cov[p.Namespace] {
			continue
		}
		running := false
		var hosts []string
		for _, sv := range p.Services {
			if sv.Workload != "" && sv.Replicas > 0 {
				running = true
			}
			hosts = append(hosts, sv.Domains...)
		}
		if !running || len(hosts) == 0 {
			continue
		}
		name := p.ProjectName
		if name == "" {
			name = p.Namespace
		}
		hosts = dedupe(hosts)
		sort.Strings(hosts)
		out = append(out, fmt.Sprintf("%s [%s: %s]", name, p.Namespace, strings.Join(hosts, ", ")))
	}
	sort.Strings(out)
	return out
}

// guardCutover rescans the server and refuses while a project outside the cutover still
// serves hostnames through the shared ingress (unless forced).
func (n *nodeRun) guardCutover() error {
	if n.force {
		return nil
	}
	covered, err := n.coveredNamespaces()
	if err != nil {
		return err
	}
	inv, err := n.s.scanZeabur(n.ctx, n.node, true)
	if err != nil {
		return err
	}
	if others := otherServing(inv, covered); len(others) > 0 {
		return errf(409, "other_zeabur_projects", "cutting over parks Zeabur's ingress, which every Zeabur project on this server shares; these would go offline: %s. Import and rehearse each of them, then cut them over together (POST /api/v1/servers/%s/zeabur/cutover, atoll zeabur cutover --server), or pass force to accept the outage", strings.Join(others, "; "), n.node)
	}
	return nil
}

// ---------- POST /servers/{id}/zeabur/cutover ----------

func (s *Server) handleNodeCutover(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	serverID := r.PathValue("id")
	node, err := s.st.GetNode(ctx, serverID)
	if err != nil {
		return err
	}
	var req api.ZeaburNodeCutoverRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	req.ImportIDs = dedupe(req.ImportIDs)
	if len(req.ImportIDs) == 0 {
		return badRequest("import_ids is required")
	}
	summary := fmt.Sprintf("Cut over %d Zeabur import(s) on %s together (%s): Zeabur services scale to 0, final data sync for each, Zeabur ingress parked once, Atoll takes ports 80/443", len(req.ImportIDs), node.Name, strings.Join(req.ImportIDs, ", "))
	if req.Force {
		summary += "; FORCED: other Zeabur projects on this server stop being served"
	}
	return s.requireConfirm(w, r, "zeabur.node_cutover", serverID, summary, req, func(ctx context.Context) (int, any, error) {
		out, err := s.startNodeCutover(ctx, serverID, req.ImportIDs, req.Force, principalFrom(r.Context()).actor())
		return 200, out, err
	})
}

// startNodeCutover checks and launches a cutover of the given jobs of one server.
func (s *Server) startNodeCutover(ctx context.Context, serverID string, ids []string, force bool, actor string) (*api.ZeaburNodeOp, error) {
	op, err := s.startNodeOpForce(ctx, serverID, ids, []string{zsRehearsed, zsRolledBack}, zsCuttingOver, force, (*nodeRun).guardCutover, (*nodeRun).cutover)
	if err != nil {
		return nil, err
	}
	for _, j := range op.Jobs {
		s.st.Audit(ctx, actor, "import.cutover", j.ID, map[string]any{"server": serverID, "batch": len(op.Jobs), "force": force})
	}
	return op, nil
}

func (n *nodeRun) cutover() error {
	for _, z := range n.runs {
		z.resetSteps(cutoverSteps)
	}
	defer func() { // after a rollback too: the project's ingress hold covers it then
		for _, z := range n.runs {
			z.releaseHeld()
		}
	}()
	err := n.doCutover()
	if err == nil {
		for _, z := range n.runs {
			z.meta.Cutovers++
			z.setStatus(zsLive)
		}
		return nil
	}
	// automatic rollback of every job of the run: Zeabur serves again
	for _, z := range n.runs {
		z.setStatus(zsRollingBack)
	}
	if rerr := n.doRollback(); rerr != nil {
		for _, z := range n.runs {
			z.setStatus(zsFailed)
		}
		return fmt.Errorf("cutover failed (%v) and rollback failed: %w", err, rerr)
	}
	for _, z := range n.runs {
		z.setStatus(zsRolledBack)
	}
	return fmt.Errorf("cutover failed, rolled back automatically: %w", err)
}

func (z *zrun) label() string {
	if n := z.job.Plan.ProjectName; n != "" {
		return n
	}
	return z.job.ID
}

func (n *nodeRun) doCutover() error {
	s, ctx := n.s, n.ctx
	for _, z := range n.runs {
		if err := z.prepareCutover(); err != nil {
			if len(n.runs) == 1 {
				return err
			}
			return fmt.Errorf("%s: %w", z.label(), err)
		}
	}
	// Hostnames that replace *.zeabur.app ones are new names: they may have no DNS record yet.
	// They stay out of the route table until configureDNS has created the record and public
	// resolvers see it (provision.go), so Caddy never asks the CA about an NXDOMAIN name. The
	// other (custom) domains already point here and are routed at once.
	for _, z := range n.runs {
		z.holdRemapped()
	}
	covered, err := n.coveredNamespaces()
	if err != nil {
		return err
	}
	// Marked before the call: a park that fails halfway (DaemonSet patched, operator stopped,
	// ports still busy) must still be undone by the rollback.
	for _, z := range n.runs {
		z.meta.IngressParked = true
		z.st.run(stepIngress)
	}
	failAll := func(err error) error {
		for _, z := range n.runs {
			z.st.fail(stepIngress, err)
		}
		return err
	}
	if _, err := s.hub.Call(ctx, n.node, api.MsgZeaburIngress, api.ZeaburIngressPayload{Park: true, Namespaces: covered, Force: n.force}, 5*time.Minute); err != nil {
		return failAll(fmt.Errorf("park Zeabur ingress: %w", err))
	}
	for _, z := range n.runs {
		if err := s.st.SetIngressHold(ctx, z.job.ProjectID, false); err != nil {
			return failAll(err)
		}
	}
	if err := s.pushIngressStrict(ctx, n.node); err != nil {
		return failAll(fmt.Errorf("start Atoll ingress: %w", err))
	}
	detail := "Zeabur ingress parked (node-wide); atoll-caddy serves 80/443"
	if len(n.runs) > 1 {
		detail = fmt.Sprintf("Zeabur ingress parked once for %d imports (node-wide); atoll-caddy serves 80/443 with all their hostnames", len(n.runs))
	}
	for _, z := range n.runs {
		z.st.done(stepIngress, detail)
	}
	for _, z := range n.runs {
		if err := z.st.do(stepDNS, func() (string, error) { return z.configureDNS() }); err != nil {
			if len(n.runs) == 1 {
				return err
			}
			return fmt.Errorf("%s: %w", z.label(), err)
		}
	}
	return nil
}

// ---------- rollback ----------

// rollback is the background part of a rollback request (one job or the whole node).
func (n *nodeRun) rollback() error {
	if err := n.doRollback(); err != nil {
		for _, z := range n.runs {
			z.setStatus(zsFailed)
		}
		return err
	}
	for _, z := range n.runs {
		z.setStatus(zsRolledBack)
	}
	return nil
}

// doRollback stops Atoll's copies of every job, restores Zeabur's ingress once (only when no
// job outside the run stays cut over) and scales Zeabur back. It keeps going after errors so
// as much as possible is restored, then reports them all.
func (n *nodeRun) doRollback() error {
	s, ctx := n.s, n.ctx
	var errs []string
	add := func(z *zrun, e []string) {
		for _, m := range e {
			if len(n.runs) > 1 {
				m = z.label() + ": " + m
			}
			errs = append(errs, m)
		}
	}
	for _, z := range n.runs {
		add(z, z.rollbackRelease())
	}
	parked := false
	for _, z := range n.runs {
		parked = parked || z.meta.IngressParked
	}
	if parked {
		others, err := s.parkedJobs(ctx, n.node, n.ids())
		switch {
		case err != nil:
			errs = append(errs, "check other cut-over imports: "+err.Error())
		case len(others) > 0:
			// Only reachable by an automatic rollback while other jobs stay cut over (a
			// forced cutover): the shared ingress must stay parked for them.
			for _, z := range n.runs {
				z.meta.IngressParked = false
				z.job.Plan.Warnings = append(z.job.Plan.Warnings, fmt.Sprintf("Rolled back while other imports on this server stay cut over (%s): Zeabur's ingress stays parked, so this project's Zeabur hostnames are not served until those are rolled back too (POST /api/v1/servers/%s/zeabur/rollback)", strings.Join(others, ", "), n.node))
			}
		default:
			if _, err := s.hub.Call(ctx, n.node, api.MsgZeaburIngress, api.ZeaburIngressPayload{Park: false}, 5*time.Minute); err != nil {
				errs = append(errs, "restore Zeabur ingress: "+err.Error())
			} else {
				for _, z := range n.runs {
					z.meta.IngressParked = false
				}
			}
		}
	}
	for _, z := range n.runs {
		add(z, z.rollbackScaleBack())
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// ---------- POST /servers/{id}/zeabur/rollback ----------

// handleNodeRollback rolls back every job of the server that is cut over (or failed with
// Zeabur workloads still scaled down) and restores the shared ingress once.
func (s *Server) handleNodeRollback(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	serverID := r.PathValue("id")
	if _, err := s.st.GetNode(ctx, serverID); err != nil {
		return err
	}
	jobs, err := s.st.ListImports(ctx)
	if err != nil {
		return err
	}
	var ids []string
	for _, j := range jobs {
		if j.Mode != importKindZeabur || j.ServerID != serverID || (j.Status != zsLive && j.Status != zsFailed) {
			continue
		}
		_, meta, err := s.loadZeaburJob(ctx, j.ID)
		if err != nil {
			return err
		}
		if !meta.RollbackDisabled && (meta.IngressParked || len(meta.Scaled) > 0) {
			ids = append(ids, j.ID)
		}
	}
	if len(ids) == 0 {
		return errf(409, "bad_state", "no import on this server is cut over; nothing to roll back")
	}
	op, err := s.startNodeOp(ctx, serverID, ids, []string{zsLive, zsFailed}, zsRollingBack, nil, (*nodeRun).rollback)
	if err != nil {
		return err
	}
	actor := principalFrom(ctx).actor()
	for _, j := range op.Jobs {
		s.st.Audit(ctx, actor, "import.rollback", j.ID, map[string]any{"server": serverID, "node": true})
	}
	writeJSON(w, 200, op)
	return nil
}
