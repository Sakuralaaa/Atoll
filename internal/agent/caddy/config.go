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
	"sort"
	"strings"
	"time"

	"github.com/atoll-sh/atoll/internal/api"
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
