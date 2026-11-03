package server

// Zeabur takeover: cutover (final sync, park Zeabur's ingress, switch DNS and routes to Atoll).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/dns"
)

// ---------- cutover ----------

func (s *Server) handleCutover(w http.ResponseWriter, r *http.Request) error {
	var req api.ZeaburCutoverRequest
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil {
			return err
		}
	}
	j, meta, err := s.loadZeaburJob(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if meta.RollbackDisabled {
		return errf(409, "bad_state", "Zeabur was removed from this server; nothing to cut over")
	}
	node, _ := s.st.GetNode(r.Context(), j.ServerID)
	nodeName := j.ServerID
	if node != nil {
		nodeName = node.Name
	}
	summary := fmt.Sprintf("Cut over Zeabur namespace %s on %s to Atoll: Zeabur services scale to 0, final data sync, Zeabur ingress parked (node-wide), Atoll takes ports 80/443", j.Namespace, nodeName)
	if req.Force {
		summary += "; FORCED: other Zeabur projects on this server stop being served"
	}
	return s.requireConfirm(w, r, "import.cutover", j.ID, summary, req, func(ctx context.Context) (int, any, error) {
		out, err := s.startCutover(ctx, j.ID, req.Force, principalFrom(r.Context()).actor())
		return 200, out, err
	})
}

// startCutover launches the cutover of one job in the background (also called for approved
// confirmations): a node cutover with a single job.
func (s *Server) startCutover(ctx context.Context, id string, force bool, actor string) (*api.ImportJob, error) {
	j, _, err := s.loadZeaburJob(ctx, id)
	if err != nil {
		return nil, err
	}
	op, err := s.startNodeCutover(ctx, j.ServerID, []string{id}, force, actor)
	if err != nil {
		return nil, err
	}
	return &op.Jobs[0], nil
}

// startStopped starts every Atoll service of the job that is stopped (after a rollback) and
// waits until they are healthy.
func (z *zrun) startStopped() error {
	s, ctx := z.s, z.ctx
	since := time.Now()
	var started []string
	for _, sv := range z.meta.Services {
		svc, stopped, err := s.st.GetService(ctx, sv.AtollID)
		if err != nil {
			return err
		}
		if !stopped {
			continue
		}
		if _, err := s.serviceAction(ctx, svc, "start"); err != nil {
			return fmt.Errorf("start %s: %w", sv.Name, err)
		}
		started = append(started, sv.AtollID)
	}
	if len(started) == 0 {
		return nil
	}
	return s.waitHealthy(ctx, started, since, s.cfg.VerifyTimeout)
}

func (z *zrun) scale(sv zeaburSvcMeta, replicas int) error {
	if sv.Workload == "" {
		return nil
	}
	_, err := z.s.hub.Call(z.ctx, z.node, api.MsgZeaburScale, api.ZeaburScalePayload{Namespace: z.meta.Namespace, Workload: sv.Workload, Replicas: replicas}, 3*time.Minute)
	if err != nil {
		return fmt.Errorf("scale %s: %w", sv.Workload, err)
	}
	if replicas == 0 {
		key := z.meta.Namespace + "/" + sv.Workload
		for _, k := range z.meta.Scaled {
			if k == key {
				return nil
			}
		}
		z.meta.Scaled = append(z.meta.Scaled, key)
		z.save()
	}
	return nil
}

// prepareCutover stops the job's Zeabur workloads and does the final data sync (cutover steps
// 1-2). The ingress switch and DNS follow once for all jobs of a node cutover (zeabur_node.go).
func (z *zrun) prepareCutover() error {
	s, ctx := z.s, z.ctx
	if err := z.st.do(stepScaleDown, func() (string, error) {
		// A rollback stops Atoll's copies (desired_stopped): start them again first, or the
		// final DB sync has no target and the ingress has no routes to serve.
		if err := z.startStopped(); err != nil {
			return "", err
		}
		n := 0
		for _, sv := range z.meta.Services {
			if sv.isDB() || sv.Workload == "" {
				continue
			}
			if err := z.scale(sv, 0); err != nil {
				return "", err
			}
			n++
		}
		return fmt.Sprintf("%d Zeabur app workload(s) scaled to 0; databases keep running until the final dump", n), nil
	}); err != nil {
		return err
	}
	if err := z.st.do(stepFinalSync, func() (string, error) {
		var verify []api.ImportVerify
		var bad []string
		for _, sv := range z.meta.Services {
			if !sv.isDB() {
				continue
			}
			res, err := z.dbSync(sv)
			if err != nil {
				return "", err
			}
			verify = append(verify, verifyRows(sv.Name, res)...)
			if !res.Match {
				m := res.Mismatches
				if len(m) == 0 {
					m = []string{"row counts differ"}
				}
				bad = append(bad, sv.Name+": "+strings.Join(m, "; "))
			}
		}
		z.job.Verify = verify
		z.save()
		if len(bad) > 0 {
			return "", fmt.Errorf("database verification failed after final sync: %s", strings.Join(bad, " | "))
		}
		// the databases are copied: stop Zeabur's copies too
		for _, sv := range z.meta.Services {
			if sv.isDB() {
				if err := z.scale(sv, 0); err != nil {
					return "", err
				}
			}
		}
		// final volume mirror with the Atoll app containers stopped
		apps := z.serviceIDs(func(m zeaburSvcMeta) bool { return !m.isDB() && len(m.Volumes) > 0 })
		for _, id := range apps {
			s.hub.Call(ctx, z.node, api.MsgAction, api.ActionPayload{ServiceID: id, Action: "stop"}, 2*s.cfg.RequestTimeout)
		}
		vol, err := z.copyVolumes()
		for _, id := range apps {
			if _, serr := s.hub.Call(ctx, z.node, api.MsgAction, api.ActionPayload{ServiceID: id, Action: "start"}, 2*s.cfg.RequestTimeout); serr != nil && err == nil {
				err = fmt.Errorf("restart %s: %w", id, serr)
			}
		}
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("databases match (%d table count(s)); %s", len(verify), vol), nil
	}); err != nil {
		return err
	}
	return nil
}

// configureDNS creates proxied A records for remapped *.zeabur.app hosts that have none yet;
// custom domains already point at this server and are left alone. Without a DNS provider the
// records are the user's job: hosts that already resolve to this server go active, the others
// stay pending with the record to create, and the cutover still succeeds (Caddy serves them as
// soon as DNS points here).
func (z *zrun) configureDNS() (string, error) {
	s, ctx := z.s, z.ctx
	doms, err := s.st.ListDomains(ctx, "", z.job.ProjectID)
	if err != nil {
		return "", err
	}
	node, err := s.st.GetNode(ctx, z.node)
	if err != nil {
		return "", err
	}
	remapped := map[string]bool{}
	for _, n := range z.meta.DomainMap {
		remapped[n] = true
	}
	prov := s.dnsProvider(ctx)
	finder, _ := prov.(dns.Finder)
	created, kept, custom, resolved := 0, 0, 0, 0
	var bad, manual []string
	var provision []api.Domain
	for _, d := range doms {
		dd := d.Domain
		if !remapped[dd.Hostname] {
			custom++
			s.st.SetDomainStatus(ctx, dd.ID, "active", "", "")
			continue
		}
		if finder != nil {
			recs, err := finder.FindA(ctx, dd.Hostname)
			if err != nil {
				bad = append(bad, dd.Hostname+": "+err.Error())
				continue
			}
			if len(recs) > 0 {
				kept++
				s.st.SetDomainStatus(ctx, dd.ID, "active", "", recs[0].ID)
				continue
			}
		}
		recID, err := prov.UpsertA(ctx, dd.Hostname, node.PublicIPv4, true)
		if errors.Is(err, dns.ErrNotConfigured) {
			if z.resolvesHere(dd.Hostname, node.PublicIPv4) {
				resolved++
				s.st.SetDomainStatus(ctx, dd.ID, "active", "", "")
				continue
			}
			note := fmt.Sprintf("no DNS provider configured: create A %s -> %s", dd.Hostname, orUnknown(node.PublicIPv4))
			s.st.SetDomainStatus(ctx, dd.ID, "pending", note, "")
			manual = append(manual, dd.Hostname)
			continue
		}
		if err != nil {
			s.st.SetDomainStatus(ctx, dd.ID, "error", err.Error(), "")
			bad = append(bad, dd.Hostname+": "+err.Error())
			continue
		}
		created++
		if finder != nil { // known not to exist before: a rollback may delete it again
			if z.meta.CreatedRecords == nil {
				z.meta.CreatedRecords = map[string]string{}
			}
			z.meta.CreatedRecords[dd.Hostname] = recID
			z.save()
		}
		dd.Status, dd.Error, dd.Proxied = statusProvisioning, "", true
		s.st.SetDomainStatus(ctx, dd.ID, statusProvisioning, "", recID)
		s.publish("domain", dd)
		provision = append(provision, dd)
	}
	if len(bad) > 0 {
		return "", fmt.Errorf("DNS failed: %s", strings.Join(bad, "; "))
	}
	// New records: wait until they resolve, route, probe HTTPS (provision.go), in the
	// background; the hold on each hostname passes to its provisioning run. Every other
	// remapped host is routed now.
	for _, dd := range provision {
		dd := dd
		delete(z.held, strings.ToLower(dd.Hostname))
		s.goBG(func(bg context.Context) { s.provisionDomain(bg, dd, z.node, node.PublicIPv4) })
	}
	if z.releaseHeld() {
		if err := s.pushIngressSync(ctx, z.node); err != nil {
			s.log.Warn("cutover: ingress push failed (the agent gets it on reconnect)", "err", err)
		}
	}
	msg := fmt.Sprintf("%d record(s) created, %d already present, %d custom domain(s) unchanged (same IP)", created, kept, custom)
	if created > 0 {
		msg += "; new names are routed once they resolve publicly, HTTPS confirmed in the background"
	}
	if resolved > 0 {
		msg += fmt.Sprintf(", %d already resolve to this server", resolved)
	}
	if len(manual) > 0 {
		msg += fmt.Sprintf("; no DNS provider: create A records to %s for %s", orUnknown(node.PublicIPv4), strings.Join(manual, ", "))
	}
	return msg, nil
}

// holdRemapped holds every bound hostname that replaces a *.zeabur.app one.
func (z *zrun) holdRemapped() {
	doms, err := z.s.st.ListDomains(z.ctx, "", z.job.ProjectID)
	if err != nil {
		return
	}
	remapped := map[string]bool{}
	for _, n := range z.meta.DomainMap {
		remapped[strings.ToLower(n)] = true
	}
	if z.held == nil {
		z.held = map[string]bool{}
	}
	for _, d := range doms {
		h := strings.ToLower(d.Hostname)
		if remapped[h] && !z.held[h] {
			z.held[h] = true
			z.s.holdRoute(h)
		}
	}
}

// releaseHeld releases the hostnames still held by this run and reports whether there were any.
func (z *zrun) releaseHeld() bool {
	had := len(z.held) > 0
	for h := range z.held {
		z.s.releaseRoute(h)
	}
	z.held = nil
	return had
}

// removeCreatedRecords deletes the DNS records the cutover created (routes must already be
// withdrawn). The domains go back to "pending", as before the first cutover.
func (z *zrun) removeCreatedRecords() error {
	s, ctx := z.s, z.ctx
	if len(z.meta.CreatedRecords) == 0 {
		return nil
	}
	byHost := map[string]string{}
	if doms, err := s.st.ListDomains(ctx, "", z.job.ProjectID); err == nil {
		for _, d := range doms {
			byHost[strings.ToLower(d.Hostname)] = d.ID
		}
	}
	prov := s.dnsProvider(ctx)
	var errs []string
	hosts := make([]string, 0, len(z.meta.CreatedRecords))
	for h := range z.meta.CreatedRecords {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		if err := prov.Delete(ctx, h, z.meta.CreatedRecords[h]); err != nil && !errors.Is(err, dns.ErrNotConfigured) {
			errs = append(errs, "delete DNS record "+h+": "+err.Error())
			continue
		}
		delete(z.meta.CreatedRecords, h)
		if id := byHost[strings.ToLower(h)]; id != "" {
			s.st.ClearDomainRecord(ctx, id, "pending", "routed by Atoll after cutover")
			if d, err := s.st.GetDomain(ctx, id); err == nil {
				s.publish("domain", d.Domain)
			}
		}
	}
	z.save()
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// resolvesHere reports whether host already resolves to ip (best effort, short timeout).
func (z *zrun) resolvesHere(host, ip string) bool {
	return z.s.resolvesTo(z.ctx, host, ip)
}
