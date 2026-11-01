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
	d.Status = "pending"
	d.CreatedAt = time.Now().UTC()
	if err := s.st.CreateDomain(ctx, *d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, errf(409, "conflict", "hostname %s is already bound", d.Hostname)
		}
		return nil, err
	}
	nodeID, err := s.serviceNode(ctx, svc)
	if err != nil {
		return nil, err
	}
	s.applyDNS(ctx, d, nodeID)
	s.pushIngress(ctx, nodeID)
	// ATOLL_<PORT>_DOMAIN changed: redeploy so the env reflects it (only if deployed before).
	if _, err := s.st.LatestDeployment(ctx, svc.ID); err == nil {
		s.deploy(ctx, svc.ID, "env")
	}
	s.st.Audit(ctx, actor, "domain.add", d.ID, map[string]string{"hostname": d.Hostname, "service": svc.ID})
	return d, nil
}

// applyDNS points d at the node's public IPv4 and records the outcome on d.
func (s *Server) applyDNS(ctx context.Context, d *api.Domain, nodeID string) {
	n, err := s.st.GetNode(ctx, nodeID)
	if err != nil {
		return
	}
	recID, err := s.dnsProvider(ctx).UpsertA(ctx, d.Hostname, n.PublicIPv4, d.Proxied)
	switch {
	case errors.Is(err, dns.ErrNotConfigured):
		d.Status = "pending"
		d.Error = fmt.Sprintf("DNS not managed: no Cloudflare token in Settings. Create an A record %s -> %s yourself.", d.Hostname, orUnknown(n.PublicIPv4))
	case err != nil:
		d.Status = "error"
		d.Error = err.Error()
	default:
		d.Status = "active"
		d.Error = ""
	}
	s.st.SetDomainStatus(ctx, d.ID, d.Status, d.Error, recID)
	s.publish("domain", d)
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
	if err := s.dnsProvider(ctx).Delete(ctx, d.Hostname, d.RecordID); err != nil && !errors.Is(err, dns.ErrNotConfigured) {
		return errf(502, "dns_error", "could not delete DNS record: %v", err)
	}
	if err := s.st.DeleteDomain(ctx, id); err != nil {
		return err
	}
	if svc, _, err := s.st.GetService(ctx, d.ServiceID); err == nil {
		if nodeID, err := s.serviceNode(ctx, svc); err == nil {
			s.pushIngress(ctx, nodeID)
		}
	}
	s.st.Audit(ctx, actor, "domain.delete", id, map[string]string{"hostname": d.Hostname})
	s.publish("domain", map[string]any{"id": id, "service_id": d.ServiceID, "deleted": true})
	return nil
}
