package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/dns"
)

// GET /cloudflare/token-check: which of the permissions Atoll uses the stored Cloudflare
// token holds. Zone.DNS: Edit (records for bound domains) on the default root's zone, and
// Zone.SSL and Certificates: Edit on every root whose certificate mode is auto (Origin CA
// issuance, which otherwise fails with 1016 and falls back to ACME). The probes change
// nothing (dns/permissions.go); the answer is cached for cfCheckTTL per token and root list.

const cfCheckTTL = 10 * time.Minute

type cfCheckCache struct {
	key string
	at  time.Time
	out api.CloudflareTokenCheck
}

func (s *Server) handleCloudflareTokenCheck(w http.ResponseWriter, r *http.Request) error {
	out, err := s.cloudflareTokenCheck(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) cloudflareTokenCheck(ctx context.Context, refresh bool) (api.CloudflareTokenCheck, error) {
	tok, _ := s.st.GetSetting(ctx, "cloudflare_token")
	base, _ := s.st.GetSetting(ctx, "base_domain")
	rs, err := s.st.ListRootDomains(ctx)
	if err != nil {
		return api.CloudflareTokenCheck{}, err
	}
	var sslRoots []string
	for _, r := range rs {
		if r.CertMode == certAuto {
			sslRoots = append(sslRoots, r.Name)
		}
	}
	out := api.CloudflareTokenCheck{Configured: tok != "", SSLRoots: sslRoots, CheckedAt: time.Now().UTC()}
	cf := s.cloudflare(ctx)
	if tok == "" || cf == nil {
		out.DNS = dns.PermMissing
		if len(sslRoots) > 0 {
			out.SSL = dns.PermMissing
		}
		return out, nil
	}
	key := hashToken(tok) + "|" + base + "|" + strings.Join(sslRoots, ",")
	s.cfCheckMu.Lock()
	defer s.cfCheckMu.Unlock() // one probe at a time; later callers get the cached answer
	if !refresh && s.cfCheck.key == key && time.Since(s.cfCheck.at) < cfCheckTTL {
		return s.cfCheck.out, nil
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dnsHost := base
	if dnsHost == "" && len(rs) > 0 {
		dnsHost = rs[0].Name
	}
	if dnsHost == "" {
		out.DNS = dns.PermUnknown
		out.DNSError = "no root domain configured"
	} else {
		res, err := cf.ProbeDNSEdit(pctx, dnsHost)
		out.DNS = res
		if err != nil {
			out.DNSError = err.Error()
		}
	}
	for _, root := range sslRoots {
		res, err := cf.ProbeOriginCA(pctx, root)
		// the worst answer wins: one root without the permission is enough to warn
		if out.SSL == "" || res == dns.PermMissing || (res == dns.PermUnknown && out.SSL == dns.PermOK) {
			out.SSL = res
			out.SSLError = ""
			if err != nil {
				out.SSLError = root + ": " + err.Error()
			}
		}
	}
	s.cfCheck = cfCheckCache{key: key, at: time.Now(), out: out}
	return out, nil
}
