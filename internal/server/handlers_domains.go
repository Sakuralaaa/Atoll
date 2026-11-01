package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/dns"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

type domainRequest struct {
	Hostname string `json:"hostname"`
	PortName string `json:"port_name"`
	Proxied  *bool  `json:"proxied"`
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
	writeJSON(w, 200, plainDomains(ds))
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
	writeJSON(w, 200, plainDomains(ds))
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
	base, _ := s.st.GetSetting(ctx, "base_domain")
	host, err := normalizeHostname(req.Hostname, base)
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
