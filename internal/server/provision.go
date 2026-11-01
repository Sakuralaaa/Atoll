package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Domain provisioning order (a new hostname with a DNS record Atoll manages):
//
//  1. the DNS record is created while the hostname is held out of the route table;
//  2. in the background, public resolvers (DoH) are polled until the name resolves to the
//     expected target (bounded by DNSWaitTimeout; on timeout the route is pushed anyway);
//  3. the hold is released and the route table is pushed to the agent's Caddy, which only
//     now asks the CA for a certificate, so the CA never sees NXDOMAIN;
//  4. an HTTPS probe confirms the certificate is served (bounded by CertWaitTimeout), and
//     the domain goes from "provisioning" to "active".
//
// Removal is the reverse: the route leaves Caddy first, then the DNS record is deleted.

const statusProvisioning = "provisioning"

// holdRoute keeps hostname out of the route table pushed to agents until releaseRoute.
func (s *Server) holdRoute(host string) {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	s.held[strings.ToLower(host)]++
}

func (s *Server) releaseRoute(host string) {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	h := strings.ToLower(host)
	if s.held[h] <= 1 {
		delete(s.held, h)
		return
	}
	s.held[h]--
}

func (s *Server) routeHeld(host string) bool {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	return s.held[strings.ToLower(host)] > 0
}

// provisionDomain runs steps 2-4 above for a domain whose DNS record was just written.
// The caller has called holdRoute(d.Hostname); provisionDomain releases it.
func (s *Server) provisionDomain(ctx context.Context, d api.Domain, nodeID, wantIP string) {
	released := false
	release := func() {
		if !released {
			released = true
			s.releaseRoute(d.Hostname)
		}
	}
	defer release()

	if err := s.waitResolves(ctx, d.Hostname, d.Proxied, wantIP); err != nil {
		s.log.Warn("domain: DNS not visible in time, pushing the route anyway", "host", d.Hostname, "err", err)
	}
	if ctx.Err() != nil {
		return
	}
	release()
	if _, err := s.st.GetDomain(ctx, d.ID); err != nil {
		return // unbound while we waited
	}
	if err := s.pushIngressSync(ctx, nodeID); err != nil {
		s.log.Warn("domain: ingress push failed (the agent gets it on reconnect)", "host", d.Hostname, "node", nodeID, "err", err)
	}
	note := ""
	if s.needsHTTPSProbe(ctx, d.Proxied) {
		if err := s.waitHTTPS(ctx, d.Hostname); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Warn("domain: HTTPS not confirmed", "host", d.Hostname, "err", err)
			note = fmt.Sprintf("HTTPS not confirmed within %s: %v", s.cfg.CertWaitTimeout, err)
		}
	}
	cur, err := s.st.GetDomain(ctx, d.ID)
	if err != nil || cur.Status != statusProvisioning {
		return // unbound or changed meanwhile
	}
	s.st.SetDomainStatus(ctx, d.ID, "active", note, "")
	cur.Status, cur.Error = "active", note
	s.publish("domain", cur.Domain)
}

// waitResolves polls public DNS until host resolves: to any address when proxied (the
// answer is Cloudflare's edge), otherwise to wantIP.
func (s *Server) waitResolves(ctx context.Context, host string, proxied bool, wantIP string) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.DNSWaitTimeout)
	defer cancel()
	var last error
	for {
		addrs, err := s.cfg.ResolvePublic(ctx, host)
		switch {
		case err != nil:
			last = err
		case len(addrs) == 0:
			last = fmt.Errorf("%s has no A/AAAA record yet", host)
		case proxied || wantIP == "":
			return nil
		default:
			for _, a := range addrs {
				if a == wantIP {
					return nil
				}
			}
			last = fmt.Errorf("%s resolves to %s, not %s yet", host, strings.Join(addrs, ", "), wantIP)
		}
		select {
		case <-ctx.Done():
			if last == nil {
				last = ctx.Err()
			}
			return fmt.Errorf("after %s: %w", s.cfg.DNSWaitTimeout, last)
		case <-time.After(s.cfg.ProvisionPoll):
		}
	}
}

// needsHTTPSProbe: a probe can tell whether the certificate is served when the client sees
// a publicly trusted one, i.e. Cloudflare's edge (proxied) or an ACME certificate. A direct
// connection to a Cloudflare Origin CA certificate never validates, so it is not probed.
func (s *Server) needsHTTPSProbe(ctx context.Context, proxied bool) bool {
	mode, _ := s.st.GetSetting(ctx, "tls_mode")
	return proxied || mode == "acme"
}

func (s *Server) waitHTTPS(ctx context.Context, host string) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.CertWaitTimeout)
	defer cancel()
	var last error
	for {
		pctx, pcancel := context.WithTimeout(ctx, 15*time.Second)
		last = s.cfg.ProbeHTTPS(pctx, host)
		pcancel()
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return last
		case <-time.After(2 * s.cfg.ProvisionPoll):
		}
	}
}

// probeHTTPS is the default HTTPS probe: the TLS handshake must validate, and a proxied
// answer must not be one of Cloudflare's origin errors (520-527, e.g. 525 handshake failed).
// Any other status, 404 or 502 included, means the certificate is in place.
func probeHTTPS(ctx context.Context, host string) error {
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://"+host+"/", nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "atoll-domain-check")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 520 && resp.StatusCode <= 527 {
		return fmt.Errorf("HTTP %d from Cloudflare (origin not serving a valid certificate yet)", resp.StatusCode)
	}
	return nil
}

// resumeProvisioning restarts provisioning for domains left "provisioning" by a restart.
// Their routes are held from New until the background run releases them.
func (s *Server) resumeProvisioning(ctx context.Context, doms []api.Domain) {
	for _, d := range doms {
		d := d
		node, ip := s.domainTarget(ctx, d.ServiceID)
		if node == "" {
			s.releaseRoute(d.Hostname)
			continue
		}
		s.goBG(func(bg context.Context) { s.provisionDomain(bg, d, node, ip) })
	}
}

// domainTarget returns the node serving a service and that node's public IPv4.
func (s *Server) domainTarget(ctx context.Context, serviceID string) (nodeID, ipv4 string) {
	svc, _, err := s.st.GetService(ctx, serviceID)
	if err != nil {
		return "", ""
	}
	nodeID, err = s.serviceNode(ctx, svc)
	if err != nil {
		return "", ""
	}
	if n, err := s.st.GetNode(ctx, nodeID); err == nil {
		ipv4 = n.PublicIPv4
	}
	return nodeID, ipv4
}
