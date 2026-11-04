package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/dns"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

type domainRequest struct {
	Hostname string `json:"hostname"`
	// RootDomain completes a bare Hostname ("blog") instead of the project's default root.
	RootDomain string `json:"root_domain,omitempty"`
	PortName   string `json:"port_name"`
	Proxied    *bool  `json:"proxied"`
}

func (s *Server) handleListAllDomains(w http.ResponseWriter, r *http.Request) error {
	ds, err := s.st.ListDomains(r.Context(), "", "")
	if err != nil {
		return err
	}
	if p := principalFrom(r.Context()); p.Projects != nil {
		ds = slices.DeleteFunc(ds, func(d store.DomainRow) bool {
			pid, err := s.projectOf(r.Context(), "services", d.ServiceID)
			return err != nil || !p.inProject(pid)
		})
	}
	writeJSON(w, 200, s.domainsWithCert(r.Context(), ds))
	return nil
}

func plainDomains(ds []store.DomainRow) []api.Domain {
	out := make([]api.Domain, len(ds))
	for i, d := range ds {
		out[i] = d.Domain
	}
	return out
}

func (s *Server) handleListDomains(w http.ResponseWriter, r *http.Request) error {
	if _, _, err := s.st.GetService(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	ds, err := s.st.ListDomains(r.Context(), r.PathValue("id"), "")
	if err != nil {
		return err
	}
	writeJSON(w, 200, s.domainsWithCert(r.Context(), ds))
	return nil
}

func (s *Server) handleAddDomain(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var req domainRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	d, err := s.prepareDomain(r.Context(), svc, req)
	if err != nil {
		return err
	}
	return s.requireConfirm(w, r, "domain.add", svc.ID, fmt.Sprintf("Bind %s to service %s (port %s) and create DNS record", d.Hostname, svc.Name, d.PortName),
		req, func(ctx context.Context) (int, any, error) {
			out, err := s.execAddDomain(ctx, svc.ID, req, principalFrom(r.Context()).actor())
			return 201, out, err
		})
}

// prepareDomain validates a request without side effects.
func (s *Server) prepareDomain(ctx context.Context, svc *api.Service, req domainRequest) (*api.Domain, error) {
	base := s.projectRoot(ctx, svc.ProjectID) // rootdomains.go
	h := req.Hostname
	if req.RootDomain != "" {
		root, err := s.checkProjectRoot(ctx, req.RootDomain)
		if err != nil {
			return nil, err
		}
		// With an explicit root the hostname is the part in front of it.
		h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
		if h == "" || h == "@" {
			h = root
		} else if h != root && !strings.HasSuffix(h, "."+root) {
			h = h + "." + root
		}
		base = root
	}
	host, err := normalizeHostname(h, base)
	if err != nil {
		return nil, err
	}
	port := ""
	for _, p := range svc.Ports {
		if p.Protocol != "http" {
			continue
		}
		if req.PortName == "" || p.Name == req.PortName {
			port = p.Name
			break
		}
	}
	if port == "" {
		if req.PortName != "" {
			return nil, badRequest("service %s has no http port named %q", svc.Name, req.PortName)
		}
		return nil, badRequest("service %s has no http port to route a domain to", svc.Name)
	}
	proxied := true
	if req.Proxied != nil {
		proxied = *req.Proxied
	}
	return &api.Domain{Hostname: host, PortName: port, Proxied: proxied}, nil
}

func (s *Server) execAddDomain(ctx context.Context, serviceID string, req domainRequest, actor string) (*api.Domain, error) {
	svc, _, err := s.st.GetService(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	d, err := s.prepareDomain(ctx, svc, req)
	if err != nil {
		return nil, err
	}
	d.ID = newID("dom")
	d.ServiceID = svc.ID
	d.Status = statusProvisioning
	d.CreatedAt = time.Now().UTC()
	// Held out of the route table until DNS is in place: Caddy asks the CA for a certificate
	// as soon as it sees the hostname, and an NXDOMAIN answer costs a minute-long retry.
	s.holdRoute(d.Hostname)
	if err := s.st.CreateDomain(ctx, *d); err != nil {
		s.releaseRoute(d.Hostname)
		if errors.Is(err, store.ErrConflict) {
			return nil, errf(409, "conflict", "hostname %s is already bound", d.Hostname)
		}
		return nil, err
	}
	nodeID, err := s.serviceNode(ctx, svc)
	if err != nil {
		s.releaseRoute(d.Hostname)
		return nil, err
	}
	ip := ""
	if n, err := s.st.GetNode(ctx, nodeID); err == nil {
		ip = n.PublicIPv4
	}
	if s.upsertDNS(ctx, d, nodeID, statusProvisioning) {
		dd := *d
		s.goBG(func(bg context.Context) { s.provisionDomain(bg, dd, nodeID, ip) })
	} else {
		s.releaseRoute(d.Hostname)
		s.pushIngress(ctx, nodeID)
	}
	// A route change only: the container is redeployed just when the domain reaches
	// something it explicitly references (domainenv.go).
	if after, err := s.st.ListDomains(ctx, "", svc.ProjectID); err == nil {
		s.redeployForDomainChange(ctx, svc.ProjectID, withoutDomain(after, d.ID), after)
	}
	s.st.Audit(ctx, actor, "domain.add", d.ID, map[string]string{"hostname": d.Hostname, "service": svc.ID})
	return d, nil
}

// applyDNS points d at the node's public IPv4 and records the outcome on d.
func (s *Server) applyDNS(ctx context.Context, d *api.Domain, nodeID string) {
	s.upsertDNS(ctx, d, nodeID, "active")
}

// upsertDNS points d at the node's public IPv4, records the outcome on d and reports
// whether the provider wrote the record (d.Status is then okStatus).
func (s *Server) upsertDNS(ctx context.Context, d *api.Domain, nodeID, okStatus string) bool {
	n, err := s.st.GetNode(ctx, nodeID)
	if err != nil {
		return false
	}
	recID, err := s.dnsProvider(ctx).UpsertA(ctx, d.Hostname, n.PublicIPv4, d.Proxied)
	switch {
	case errors.Is(err, dns.ErrNotConfigured) && s.resolvesTo(ctx, d.Hostname, n.PublicIPv4):
		// no DNS provider, but the name already points here (wildcard DNS such as sslip.io,
		// or a record the user made): nothing left to do
		d.Status = "active"
		d.Error = ""
	case errors.Is(err, dns.ErrNotConfigured):
		d.Status = "pending"
		d.Error = fmt.Sprintf("DNS not managed: no Cloudflare token in Settings. Create an A record %s -> %s yourself.", d.Hostname, orUnknown(n.PublicIPv4))
	case err != nil:
		d.Status = "error"
		d.Error = err.Error()
	default:
		d.Status = okStatus
		d.Error = ""
	}
	s.st.SetDomainStatus(ctx, d.ID, d.Status, d.Error, recID)
	s.publish("domain", d)
	return err == nil
}

// resolvesTo reports whether host already resolves to ip (best effort, short timeout).
func (s *Server) resolvesTo(ctx context.Context, host, ip string) bool {
	if ip == "" || s.cfg.LookupHost == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := s.cfg.LookupHost(ctx, host)
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if a == ip {
			return true
		}
	}
	return false
}

func orUnknown(s string) string {
	if s == "" {
		return "<server IP unknown>"
	}
	return s
}

func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) error {
	d, err := s.st.GetDomain(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return s.requireConfirm(w, r, "domain.delete", d.ID, fmt.Sprintf("Unbind %s and delete its DNS record", d.Hostname), nil,
		func(ctx context.Context) (int, any, error) {
			return 204, nil, s.execDeleteDomain(ctx, d.ID, principalFrom(r.Context()).actor())
		})
}

func (s *Server) execDeleteDomain(ctx context.Context, id, actor string) error {
	d, err := s.st.GetDomain(ctx, id)
	if err != nil {
		return err
	}
	// Route first, DNS record second: while the record still exists nobody is sent to a
	// Caddy that no longer knows the name, and the CA is never asked about a dying name.
	nodeID, projectID := "", ""
	if svc, _, err := s.st.GetService(ctx, d.ServiceID); err == nil {
		nodeID, _ = s.serviceNode(ctx, svc)
		projectID = svc.ProjectID
	}
	s.holdRoute(d.Hostname)
	if nodeID != "" && s.hub.Online(nodeID) {
		if err := s.pushIngressSync(ctx, nodeID); err != nil {
			s.log.Warn("domain: route removal push failed", "host", d.Hostname, "node", nodeID, "err", err)
		}
	}
	err = s.dnsProvider(ctx).Delete(ctx, d.Hostname, d.RecordID)
	if err != nil && !errors.Is(err, dns.ErrNotConfigured) {
		s.releaseRoute(d.Hostname) // still bound: serve it again
		if nodeID != "" {
			s.pushIngress(ctx, nodeID)
		}
		return errf(502, "dns_error", "could not delete DNS record: %v", err)
	}
	var before []store.DomainRow
	if projectID != "" {
		before, _ = s.st.ListDomains(ctx, "", projectID)
	}
	err = s.st.DeleteDomain(ctx, id)
	s.releaseRoute(d.Hostname)
	if err != nil {
		return err
	}
	if before != nil {
		s.redeployForDomainChange(ctx, projectID, before, withoutDomain(before, id))
	}
	s.st.Audit(ctx, actor, "domain.delete", id, map[string]string{"hostname": d.Hostname})
	s.publish("domain", map[string]any{"id": id, "service_id": d.ServiceID, "deleted": true})
	return nil
}

// domainsWithCert is plainDomains with Domain.Cert filled in: the certificate the servers
// present for each hostname, worked out the way the ingress payload and caddy.Build do.
func (s *Server) domainsWithCert(ctx context.Context, ds []store.DomainRow) []api.Domain {
	out := plainDomains(ds)
	if len(out) == 0 {
		return out
	}
	mode, _ := s.st.GetSetting(ctx, "tls_mode")
	cert, _ := s.st.GetSetting(ctx, "origin_cert_pem")
	key, _ := s.st.GetSetting(ctx, "origin_key_pem")
	rootMode := map[string]string{}
	var names []string
	if rs, err := s.st.ListRootDomains(ctx); err == nil {
		for _, r := range rs {
			if r.CertMode == certGlobal {
				continue
			}
			m := "acme"
			if r.CertStatus == certOrigin && r.CertPEM != "" && r.KeyPEM != "" {
				m = "origin"
			}
			rootMode[r.Name] = m
			names = append(names, r.Name)
		}
	}
	for i := range out {
		d := &out[i]
		switch root := dns.RootFor(d.Hostname, names); {
		case root != "":
			d.Cert = rootMode[root]
			if d.Cert == "origin" && !d.Proxied {
				d.Cert = "acme" // grey cloud under an Origin CA root (caddy.buildMulti)
			}
		case mode == "acme":
			d.Cert = "acme"
		case cert != "" && key != "":
			d.Cert = "origin"
		default:
			d.Cert = "http"
		}
	}
	return out
}

// handlePatchDomain switches a domain between Cloudflare proxied (orange cloud) and
// DNS-only (grey cloud): the DNS record first, then the route table, since the certificate
// a host gets can depend on it (a grey-cloud host under an Origin CA root gets ACME).
func (s *Server) handlePatchDomain(w http.ResponseWriter, r *http.Request) error {
	d, err := s.st.GetDomain(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var req api.DomainPatchRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Proxied == nil {
		return badRequest("nothing to change: send {\"proxied\": true|false}")
	}
	what := "DNS only (grey cloud)"
	if *req.Proxied {
		what = "Cloudflare proxied (orange cloud)"
	}
	return s.requireConfirm(w, r, "domain.proxy", d.ID, fmt.Sprintf("Switch %s to %s", d.Hostname, what), req,
		func(ctx context.Context) (int, any, error) {
			out, err := s.execSetDomainProxied(ctx, d.ID, *req.Proxied, principalFrom(r.Context()).actor())
			return 200, out, err
		})
}

func (s *Server) execSetDomainProxied(ctx context.Context, id string, proxied bool, actor string) (*api.Domain, error) {
	d, err := s.st.GetDomain(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.Proxied != proxied {
		svc, _, err := s.st.GetService(ctx, d.ServiceID)
		if err != nil {
			return nil, err
		}
		nodeID, err := s.serviceNode(ctx, svc)
		if err != nil {
			return nil, err
		}
		n, err := s.st.GetNode(ctx, nodeID)
		if err != nil {
			return nil, err
		}
		recID, err := s.dnsProvider(ctx).UpsertA(ctx, d.Hostname, n.PublicIPv4, proxied)
		switch {
		case errors.Is(err, dns.ErrNotConfigured):
			return nil, badRequest("the DNS record of %s is not managed by Atoll (no Cloudflare token in Settings): change its proxy status in Cloudflare", d.Hostname)
		case err != nil:
			return nil, errf(502, "dns_error", "could not update the DNS record: %v", err)
		}
		if err := s.st.SetDomainProxied(ctx, d.ID, proxied); err != nil {
			return nil, err
		}
		d.Proxied = proxied
		d.Status, d.Error = statusProvisioning, ""
		s.st.SetDomainStatus(ctx, d.ID, d.Status, "", recID)
		s.publish("domain", d.Domain)
		// the route stays served throughout; once the new record is visible the route
		// table (and with it the certificate choice) is pushed and HTTPS confirmed
		dd := d.Domain
		ip := n.PublicIPv4
		s.goBG(func(bg context.Context) { s.reprovisionDomain(bg, dd, nodeID, ip) })
		s.st.Audit(ctx, actor, "domain.proxy", d.ID, map[string]string{"hostname": d.Hostname, "proxied": fmt.Sprint(proxied)})
	}
	out := s.domainsWithCert(ctx, []store.DomainRow{*d})
	return &out[0], nil
}
