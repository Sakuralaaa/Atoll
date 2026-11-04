// Package caddy builds the JSON config for the atoll-caddy ingress container and pushes it
// through Caddy's admin API.
package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// In-container paths used by the atoll-caddy container.
const (
	AdminSocket   = "/run/caddy/admin.sock" // bind-mounted from <data>/caddy/run
	ConfigPath    = "/etc/atoll/caddy/caddy.json"
	TLSCertPath   = "/etc/atoll/tls/origin.crt"
	TLSKeyPath    = "/etc/atoll/tls/origin.key"
	originCertTag = "atoll-origin"
)

// Input is everything the generated config depends on.
type Input struct {
	Routes    []api.IngressRoute
	TLSMode   string // "cloudflare-origin" | "acme"
	ACMEEmail string
	// HaveOriginCert reports whether the origin cert/key files exist. In cloudflare-origin
	// mode without a cert Caddy serves plain HTTP only, so the container can still start.
	HaveOriginCert bool
	// Domains are root domains with their own certificate mode (multi-domain). Empty keeps
	// the single-certificate config above byte for byte.
	Domains []Domain
}

// Domain is a root domain with its own certificate: Mode "origin" serves the certificate
// files DomainCertPaths(Name) (HaveCert reports they exist), "acme" lets Caddy issue one.
type Domain struct {
	Name     string
	Mode     string
	HaveCert bool
}

// DomainCertPaths returns the in-container certificate and key paths of a root domain.
func DomainCertPaths(name string) (cert, key string) {
	d := filepath.Dir(TLSCertPath) + "/domains/"
	return d + name + ".crt", d + name + ".key"
}

type obj = map[string]any

// Build returns the full Caddy JSON config (deterministic, indented).
func Build(in Input) ([]byte, error) {
	// One route per hostname; first upstream wins for duplicates. Sorted for stable output.
	byHost := map[string]string{}
	var hosts []string
	for _, r := range in.Routes {
		h := strings.ToLower(strings.TrimSpace(r.Hostname))
		if h == "" || r.Upstream == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(r.Upstream); err != nil {
			return nil, fmt.Errorf("route %s: upstream %q must be host:port", h, r.Upstream)
		}
		if _, dup := byHost[h]; dup {
			continue
		}
		byHost[h] = r.Upstream
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	if len(in.Domains) > 0 {
		return buildMulti(in, hosts, byHost)
	}

	routes := routeList(hosts, byHost)

	server := obj{"routes": routes}
	apps := obj{}
	switch in.TLSMode {
	case "acme":
		server["listen"] = []string{":443"}
		if in.ACMEEmail != "" {
			apps["tls"] = obj{"automation": obj{"policies": []any{obj{
				"issuers": []any{obj{"module": "acme", "email": in.ACMEEmail}},
			}}}}
		}
	case "cloudflare-origin", "":
		if in.HaveOriginCert {
			server["listen"] = []string{":443"}
			// Certificates are static (Cloudflare origin cert); no ACME, keep http->https redirect.
			server["automatic_https"] = obj{"disable_certificates": true}
			server["tls_connection_policies"] = []any{obj{
				"certificate_selection": obj{"any_tag": []string{originCertTag}},
			}}
			apps["tls"] = obj{"certificates": obj{"load_files": []any{obj{
				"certificate": TLSCertPath, "key": TLSKeyPath, "tags": []string{originCertTag},
			}}}}
		} else {
			server["listen"] = []string{":80"}
			server["automatic_https"] = obj{"disable": true}
		}
	default:
		return nil, fmt.Errorf("unknown tls mode %q", in.TLSMode)
	}
	apps["http"] = obj{"servers": obj{"atoll": server}}
	return render(apps)
}

func routeList(hosts []string, byHost map[string]string) []any {
	routes := []any{}
	for _, h := range hosts {
		routes = append(routes, obj{
			"match": []any{obj{"host": []string{h}}},
			"handle": []any{obj{
				"handler":   "reverse_proxy",
				"upstreams": []any{obj{"dial": byHost[h]}},
			}},
			"terminal": true,
		})
	}
	return routes
}

// buildMulti is the config when some root domains carry their own certificate mode. One
// HTTPS server holds every TLS route; Caddy picks the certificate by SNI: hosts under an
// origin root match a connection policy that selects that root's tagged certificate (and
// are skipped by ACME), the rest fall through to the default policy and Caddy's automatic
// HTTPS. Hosts that follow a certificate-less cloudflare-origin global mode stay on plain
// HTTP in a separate :80 server, as they were before.
func buildMulti(in Input, hosts []string, byHost map[string]string) ([]byte, error) {
	names := make([]string, 0, len(in.Domains))
	byName := map[string]Domain{}
	for _, d := range in.Domains {
		n := strings.ToLower(strings.Trim(d.Name, "."))
		if n == "" {
			continue
		}
		if d.Mode != "origin" && d.Mode != "acme" {
			return nil, fmt.Errorf("root domain %s: unknown certificate mode %q", n, d.Mode)
		}
		if d.Mode == "origin" && !d.HaveCert {
			d.Mode = "acme" // the certificate never arrived: better a public one than none
		}
		d.Name = n
		byName[n] = d
		names = append(names, n)
	}
	switch in.TLSMode {
	case "acme", "cloudflare-origin", "":
	default:
		return nil, fmt.Errorf("unknown tls mode %q", in.TLSMode)
	}
	const globalTag = originCertTag
	tagHosts := map[string][]string{} // certificate tag -> hosts it serves
	var tlsHosts, httpHosts, skip []string
	for _, h := range hosts {
		mode, tag := "", ""
		if root := longestRoot(h, names); root != "" {
			d := byName[root]
			mode = d.Mode
			tag = originCertTag + "-" + root
		} else if in.TLSMode == "acme" {
			mode = "acme"
		} else if in.HaveOriginCert {
			mode, tag = "origin", globalTag
		} else {
			mode = "http"
		}
		switch mode {
		case "origin":
			tagHosts[tag] = append(tagHosts[tag], h)
			skip = append(skip, h)
			tlsHosts = append(tlsHosts, h)
		case "acme":
			tlsHosts = append(tlsHosts, h)
		default:
			httpHosts = append(httpHosts, h)
		}
	}

	server := obj{"listen": []string{":443"}, "routes": routeList(tlsHosts, byHost)}
	if len(skip) > 0 {
		server["automatic_https"] = obj{"skip_certificates": skip}
	}
	tags := make([]string, 0, len(tagHosts))
	for t := range tagHosts {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	policies := []any{}
	for _, t := range tags {
		policies = append(policies, obj{
			"match":                 obj{"sni": tagHosts[t]},
			"certificate_selection": obj{"any_tag": []string{t}},
		})
	}
	policies = append(policies, obj{}) // everything else: Caddy's own certificates
	server["tls_connection_policies"] = policies
	servers := obj{"atoll": server}
	if len(httpHosts) > 0 {
		servers["atoll_http"] = obj{
			"listen":          []string{":80"},
			"routes":          routeList(httpHosts, byHost),
			"automatic_https": obj{"disable": true},
		}
	}

	tlsApp := obj{}
	var files []any
	if len(tagHosts[globalTag]) > 0 {
		files = append(files, obj{"certificate": TLSCertPath, "key": TLSKeyPath, "tags": []string{globalTag}})
	}
	sort.Strings(names)
	for _, n := range names {
		if d := byName[n]; d.Mode == "origin" {
			c, k := DomainCertPaths(n)
			files = append(files, obj{"certificate": c, "key": k, "tags": []string{originCertTag + "-" + n}})
		}
	}
	if len(files) > 0 {
		tlsApp["certificates"] = obj{"load_files": files}
	}
	if in.ACMEEmail != "" {
		tlsApp["automation"] = obj{"policies": []any{obj{
			"issuers": []any{obj{"module": "acme", "email": in.ACMEEmail}},
		}}}
	}
	apps := obj{"http": obj{"servers": servers}}
	if len(tlsApp) > 0 {
		apps["tls"] = tlsApp
	}
	return render(apps)
}

// longestRoot returns the longest root that host equals or is under ("" = none).
func longestRoot(host string, roots []string) string {
	best := ""
	for _, r := range roots {
		if (host == r || strings.HasSuffix(host, "."+r)) && len(r) > len(best) {
			best = r
		}
	}
	return best
}

func render(apps obj) ([]byte, error) {
	cfg := obj{
		"admin": obj{
			"listen": "unix/" + AdminSocket,
			"config": obj{"persist": false},
		},
		"logging": obj{"logs": obj{"default": obj{"level": "WARN"}}},
		"apps":    apps,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cfg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Admin talks to Caddy's admin API over the host side of the unix socket.
type Admin struct {
	SocketPath string // host path, e.g. /var/lib/atoll/caddy/run/admin.sock
}

func (a Admin) client() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", a.SocketPath)
		}},
	}
}

// Load replaces the running config (POST /load). Caddy accepts Host 127.0.0.1 on unix sockets.
func (a Admin) Load(ctx context.Context, cfg []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/load", bytes.NewReader(cfg))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("caddy /load: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}
