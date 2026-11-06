package server

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/dns"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// Multi-domain: Settings holds a list of root domains (store.root_domains). The default
// one is mirrored to settings.base_domain, so everything that predates the list keeps
// reading base_domain unchanged. Each root has a Cloudflare zone (found by longest suffix
// match with the stored token) and a certificate mode:
//
//	global  follow settings.tls_mode and the uploaded origin certificate (pre-v0.1.24)
//	auto    issue a Cloudflare Origin CA certificate for name + *.name; ACME if that fails
//	acme    Caddy's own ACME issuance
//
// Only roots that are not "global" reach the agents (IngressPayload.Domains), so an
// install whose roots all follow the global settings, such as one upgraded with its
// single base domain, sends exactly the payload it sent before.

const (
	certGlobal = "global"
	certAuto   = "auto"
	certACME   = "acme"
	certOrigin = "origin" // status only
)

// originRenewBefore is how long before expiry an auto-mode origin certificate is reissued.
const originRenewBefore = 30 * 24 * time.Hour

// cloudflare returns the Cloudflare client for the stored token, or nil without one (or
// when tests replace the DNS provider).
func (s *Server) cloudflare(ctx context.Context) *dns.Cloudflare {
	if p, ok := s.dnsProvider(ctx).(*dns.Cloudflare); ok {
		return p
	}
	return nil
}

func rootNames(rs []store.RootDomainRow) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

func normalizeRootName(n string) (string, error) {
	n = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), ".")
	if n == "" {
		return "", badRequest("root domain name required")
	}
	if strings.HasPrefix(n, "*.") || !strings.Contains(n, ".") || !hostnameRe.MatchString(n) || len(n) > 200 {
		return "", badRequest("invalid root domain %q (want e.g. example.com)", n)
	}
	return n, nil
}

func checkCertMode(m string) error {
	switch m {
	case certGlobal, certAuto, certACME:
		return nil
	}
	return badRequest("cert_mode must be global, auto or acme")
}

func (s *Server) handleListRootDomains(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	rs, err := s.st.ListRootDomains(ctx)
	if err != nil {
		return err
	}
	if p := principalFrom(ctx); p != nil && p.Member {
		// members pick a root for their domains and project defaults: names only, no
		// zone, certificate state or errors (notes/subaccounts.md)
		// and only the roots an admin allowed it (B7, member_domains.go)
		allowed, err := s.memberRootsOf(ctx, p)
		if err != nil {
			return err
		}
		out := []memberRootDomain{}
		for _, x := range rs {
			if slices.Contains(allowed, x.Name) {
				out = append(out, memberRootDomain{Name: x.Name, Default: x.Default})
			}
		}
		writeJSON(w, 200, out)
		return nil
	}
	// the root carried over from base_domain (migration 0019) keeps resolving its zone
	// the old way (Settings zone id, else a lookup at DNS time) and never stores it:
	// show it as that zone rather than "no zone found". Display only, no Cloudflare call.
	base, _ := s.st.GetSetting(ctx, "base_domain")
	tok, _ := s.st.GetSetting(ctx, "cloudflare_token")
	for i := range rs {
		if rs[i].ZoneName == "" && rs[i].Name == base && tok != "" {
			rs[i].ZoneName = base
		}
	}
	out := make([]api.RootDomain, len(rs))
	for i, x := range rs {
		out[i] = x.RootDomain
	}
	writeJSON(w, 200, out)
	return nil
}

// memberRootDomain is what GET /root-domains answers a member.
type memberRootDomain struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
}

func (s *Server) handleCreateRootDomain(w http.ResponseWriter, r *http.Request) error {
	var req api.RootDomainRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	name, err := normalizeRootName(req.Name)
	if err != nil {
		return err
	}
	ctx := r.Context()
	existing, err := s.st.ListRootDomains(ctx)
	if err != nil {
		return err
	}
	mode := req.CertMode
	if mode == "" {
		mode = certAuto
		if len(existing) == 0 {
			mode = certGlobal // a single root behaves exactly like the old base_domain
		}
	}
	if err := checkCertMode(mode); err != nil {
		return err
	}
	rd, err := s.addRootDomain(ctx, name, mode, req.Default != nil && *req.Default)
	if err != nil {
		return err
	}
	s.st.Audit(ctx, principalFrom(ctx).actor(), "root_domain.add", name, map[string]string{"cert_mode": mode})
	writeJSON(w, 201, rd)
	return nil
}

// addRootDomain creates a root domain, discovers its zone, settles its certificate and
// pushes the new ingress to every node.
func (s *Server) addRootDomain(ctx context.Context, name, mode string, def bool) (*api.RootDomain, error) {
	status := ""
	if mode == certGlobal {
		status = certGlobal
	}
	if err := s.st.CreateRootDomain(ctx, api.RootDomain{Name: name, CertMode: mode, CertStatus: status, Default: def}); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, errf(409, "conflict", "root domain %s already exists", name)
		}
		return nil, err
	}
	s.discoverZone(ctx, name)
	s.settleRootCert(ctx, name)
	s.pushIngressAll(ctx)
	return s.rootDomainOut(ctx, name)
}

func (s *Server) rootDomainOut(ctx context.Context, name string) (*api.RootDomain, error) {
	r, err := s.st.GetRootDomain(ctx, name)
	if err != nil {
		return nil, err
	}
	s.publish("root_domain", r.RootDomain)
	return &r.RootDomain, nil
}

// discoverZone records the Cloudflare zone of a root (best effort: no token or no zone
// leaves it empty, and DNS records for its hosts then have to be made by hand).
func (s *Server) discoverZone(ctx context.Context, name string) {
	cf := s.cloudflare(ctx)
	if cf == nil {
		return
	}
	id, zone, err := cf.FindZone(ctx, name)
	if err != nil {
		s.log.Info("root domain: no Cloudflare zone", "root", name, "err", err)
		return
	}
	s.st.SetRootDomainZone(ctx, name, id, zone)
}

// settleRootCert decides what certificate a root serves and records it. In auto mode it
// issues (or keeps, while valid) a Cloudflare Origin CA certificate and falls back to ACME
// when the token cannot issue one.
func (s *Server) settleRootCert(ctx context.Context, name string) {
	r, err := s.st.GetRootDomain(ctx, name)
	if err != nil {
		return
	}
	switch r.CertMode {
	case certGlobal:
		s.st.SetRootDomainCert(ctx, name, certGlobal, "", "", nil, "", "", false)
		return
	case certACME:
		s.st.SetRootDomainCert(ctx, name, certACME, "", "", nil, "", "", false)
		return
	}
	if r.CertPEM != "" && r.KeyPEM != "" && r.CertExpiresAt != nil && time.Until(*r.CertExpiresAt) > originRenewBefore {
		s.st.SetRootDomainCert(ctx, name, certOrigin, "", "", nil, "", "", false)
		return
	}
	cf := s.cloudflare(ctx)
	if cf == nil {
		s.st.SetRootDomainCert(ctx, name, certACME, "no Cloudflare API token in Settings: using ACME", "", nil, "", "", false)
		return
	}
	ictx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	oc, err := cf.IssueOriginCert(ictx, name)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "HTTP 403") || strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "1016") || strings.Contains(msg, "10000") || strings.Contains(msg, "uthentication") {
			msg = "the Cloudflare token cannot issue Origin CA certificates (add the zone permission SSL and Certificates: Edit): " + msg
		}
		s.log.Warn("root domain: origin certificate failed, using ACME", "root", name, "err", err)
		// keep a still-valid older certificate rather than dropping to ACME
		if r.CertPEM != "" && r.CertExpiresAt != nil && time.Until(*r.CertExpiresAt) > 0 {
			s.st.SetRootDomainCert(ctx, name, certOrigin, msg, "", nil, "", "", false)
			return
		}
		s.st.SetRootDomainCert(ctx, name, certACME, msg, "", nil, "", "", false)
		return
	}
	exp := oc.NotAfter
	if err := s.st.SetRootDomainCert(ctx, name, certOrigin, "", oc.ID, &exp, oc.CertPEM, oc.KeyPEM, true); err != nil {
		s.log.Error("root domain: store certificate", "root", name, "err", err)
		return
	}
	if r.CertID != "" && r.CertID != oc.ID {
		if err := cf.RevokeOriginCert(ctx, r.CertID); err != nil {
			s.log.Info("root domain: revoke replaced origin certificate", "root", name, "err", err)
		}
	}
}

func (s *Server) handlePatchRootDomain(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	cur, err := s.st.GetRootDomain(ctx, strings.ToLower(r.PathValue("name")))
	if err != nil {
		return err
	}
	var req api.RootDomainRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Name != "" && !strings.EqualFold(req.Name, cur.Name) {
		return badRequest("a root domain cannot be renamed: add the new one and remove the old one")
	}
	changed := false
	if req.CertMode != "" && req.CertMode != cur.CertMode {
		if err := checkCertMode(req.CertMode); err != nil {
			return err
		}
		if err := s.st.SetRootDomainCertMode(ctx, cur.Name, req.CertMode); err != nil {
			return err
		}
		s.settleRootCert(ctx, cur.Name)
		changed = true
	}
	if req.Default != nil && *req.Default && !cur.Default {
		if err := s.st.SetDefaultRootDomain(ctx, cur.Name); err != nil {
			return err
		}
	}
	if changed {
		s.pushIngressAll(ctx)
	}
	s.st.Audit(ctx, principalFrom(ctx).actor(), "root_domain.update", cur.Name, map[string]string{"cert_mode": req.CertMode})
	out, err := s.rootDomainOut(ctx, cur.Name)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// handleRefreshRootDomain rediscovers the zone and retries the certificate.
func (s *Server) handleRefreshRootDomain(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	cur, err := s.st.GetRootDomain(ctx, strings.ToLower(r.PathValue("name")))
	if err != nil {
		return err
	}
	s.discoverZone(ctx, cur.Name)
	s.settleRootCert(ctx, cur.Name)
	s.pushIngressAll(ctx)
	out, err := s.rootDomainOut(ctx, cur.Name)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) handleDeleteRootDomain(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	cur, err := s.st.GetRootDomain(ctx, strings.ToLower(r.PathValue("name")))
	if err != nil {
		return err
	}
	all, err := s.st.ListRootDomains(ctx)
	if err != nil {
		return err
	}
	if cur.Default && len(all) > 1 {
		return errf(409, "is_default", "%s is the default root domain: make another one the default first", cur.Name)
	}
	n, err := s.st.CountDomainsUnder(ctx, cur.Name)
	if err != nil {
		return err
	}
	if n > 0 {
		return errf(409, "in_use", "%d bound domain(s) still use %s: remove them first", n, cur.Name)
	}
	if err := s.st.DeleteRootDomain(ctx, cur.Name); err != nil {
		return err
	}
	if cf := s.cloudflare(ctx); cf != nil && cur.CertID != "" {
		if err := cf.RevokeOriginCert(ctx, cur.CertID); err != nil {
			s.log.Info("root domain: revoke origin certificate", "root", cur.Name, "err", err)
		}
	}
	s.pushIngressAll(ctx)
	s.st.Audit(ctx, principalFrom(ctx).actor(), "root_domain.delete", cur.Name, nil)
	s.publish("root_domain", map[string]any{"name": cur.Name, "deleted": true})
	w.WriteHeader(204)
	return nil
}

// pushIngressAll sends the route table to every node (async, best effort).
func (s *Server) pushIngressAll(ctx context.Context) {
	nodes, _ := s.st.ListNodes(ctx)
	for _, n := range nodes {
		s.pushIngress(ctx, n.ID)
	}
}

// ingressDomains returns the roots with their own certificate for IngressPayload.Domains
// (nil when every root follows the global settings).
func (s *Server) ingressDomains(ctx context.Context) []api.IngressDomain {
	rs, err := s.st.ListRootDomains(ctx)
	if err != nil {
		s.log.Warn("ingress: list root domains", "err", err)
		return nil
	}
	var out []api.IngressDomain
	for _, r := range rs {
		if r.CertMode == certGlobal {
			continue
		}
		switch {
		case r.CertStatus == certOrigin && r.CertPEM != "" && r.KeyPEM != "":
			out = append(out, api.IngressDomain{Name: r.Name, Mode: "origin", CertPEM: r.CertPEM, KeyPEM: r.KeyPEM})
		default:
			out = append(out, api.IngressDomain{Name: r.Name, Mode: "acme"})
		}
	}
	return out
}

// syncLegacyBaseDomain applies a PUT /settings base_domain to the root list: the field
// keeps meaning "the default root domain". With no roots it adds the first one; an
// unknown name renames the only root while it is a global-mode one (the old single-domain
// edit); otherwise an unknown name is refused.
func (s *Server) syncLegacyBaseDomain(ctx context.Context, base string) error {
	rs, err := s.st.ListRootDomains(ctx)
	if err != nil {
		return err
	}
	cur := ""
	for _, r := range rs {
		if r.Default {
			cur = r.Name
		}
	}
	switch {
	case base == cur:
		if base == "" {
			return s.st.SetSetting(ctx, "base_domain", "")
		}
		return nil
	case base == "":
		if len(rs) == 1 { // clearing the only root, as clearing base_domain did
			return s.st.DeleteRootDomain(ctx, rs[0].Name)
		}
		return nil // several roots: remove them in the list instead
	}
	for _, r := range rs {
		if r.Name == base {
			return s.st.SetDefaultRootDomain(ctx, base)
		}
	}
	if len(rs) == 1 && rs[0].CertMode == certGlobal {
		if err := s.st.RenameRootDomain(ctx, rs[0].Name, base); err != nil {
			return err
		}
		s.discoverZone(ctx, base)
		return nil
	}
	if len(rs) > 0 {
		// with a list in use, a name that is not in it is most likely a stale form: never
		// let it add or rename roots behind the user's back
		return badRequest("base_domain %q is not one of the root domains: add it with POST /api/v1/root-domains first", base)
	}
	_, err = s.addRootDomain(ctx, base, certGlobal, true)
	return err
}

// checkLegacyBaseDomain reports, before anything is saved, whether syncLegacyBaseDomain
// would refuse base.
func (s *Server) checkLegacyBaseDomain(ctx context.Context, base string) error {
	if base == "" {
		return nil
	}
	rs, err := s.st.ListRootDomains(ctx)
	if err != nil || len(rs) == 0 || (len(rs) == 1 && rs[0].CertMode == certGlobal) {
		return err
	}
	for _, r := range rs {
		if r.Name == base {
			return nil
		}
	}
	return badRequest("base_domain %q is not one of the root domains: add it with POST /api/v1/root-domains first", base)
}

// projectRoot returns the root domain a project's new domains default to: its own choice
// while that root still exists, else the default root ("" = none configured).
func (s *Server) projectRoot(ctx context.Context, projectID string) string {
	base, _ := s.st.GetSetting(ctx, "base_domain")
	if projectID == "" {
		return base
	}
	p, err := s.st.GetProject(ctx, projectID)
	if err != nil || p.RootDomain == "" {
		return base
	}
	if _, err := s.st.GetRootDomain(ctx, p.RootDomain); err != nil {
		return base
	}
	return p.RootDomain
}

// checkProjectRoot validates a project's root_domain ("" = global default).
func (s *Server) checkProjectRoot(ctx context.Context, root string) (string, error) {
	root = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(root)), ".")
	if root == "" {
		return "", nil
	}
	if _, err := s.st.GetRootDomain(ctx, root); err != nil {
		return "", badRequest("unknown root domain %q (add it in Settings first)", root)
	}
	return root, nil
}
