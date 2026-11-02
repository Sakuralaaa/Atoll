package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent/tailnet"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

// controlURLs is the ordered list of control server URLs to try (primary first).
func (a *Agent) controlURLs() []string {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.Cfg.ControlURLs()
}

func (a *Agent) currentURL() string {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return strings.TrimRight(a.Cfg.ServerURL, "/")
}

func (a *Agent) tailnetClient() *tailnet.Client {
	if a.Tailnet == nil {
		a.Tailnet = tailnet.New()
	}
	return a.Tailnet
}

// networkInfo is the private-network part of the hello frame.
func (a *Agent) networkInfo(ctx context.Context) (string, *api.NodeNetwork) {
	st := a.tailnetClient().Status(ctx)
	ip := st.IP
	if ip == "" {
		ip = tailnet.LocalIP()
	}
	a.cfgMu.Lock()
	nn := &api.NodeNetwork{State: st.State(), ControlURL: strings.TrimRight(a.Cfg.ServerURL, "/"),
		FallbackURLs: append([]string(nil), a.Cfg.FallbackURLs...)}
	a.cfgMu.Unlock()
	a.connMu.Lock()
	if a.connURL != "" {
		nn.ControlURL = a.connURL // the URL this connection actually uses (may be a fallback)
	}
	a.connMu.Unlock()
	nn.ViaTailnet = tailnet.IsTailnetURL(nn.ControlURL)
	if st.Installed || ip != "" {
		nn.Provider = api.NetworkProviderTailscale
	}
	if a.MeshErr != nil {
		nn.MeshError = a.MeshErr()
	}
	return ip, nn
}

// dropConnection closes the current server connection so connectLoop reconnects using the
// (possibly new) primary URL. Containers are not affected.
func (a *Agent) dropConnection() {
	a.connMu.Lock()
	c := a.conn
	a.connMu.Unlock()
	a.switched.Store(true)
	if c != nil {
		c.CloseNow()
	}
}

// SwitchControlURL verifies that url answers /agent/v1/ping with this node's token, then makes
// it the primary control URL (persisted; every previous URL stays as a fallback). On any
// failure the configuration is left unchanged.
func (a *Agent) SwitchControlURL(ctx context.Context, url, fallback string) error {
	url = strings.TrimRight(strings.TrimSpace(url), "/")
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("control url %q must start with http:// or https://", url)
	}
	timeout := a.ProbeTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := tailnet.Probe(pctx, a.HTTPClient, url, a.Cfg.NodeToken, a.Cfg.NodeID); err != nil {
		return err
	}
	a.cfgMu.Lock()
	next := *a.Cfg
	next.SwitchServer(url, fallback)
	if a.CfgPath != "" {
		if err := SaveConfig(a.CfgPath, &next); err != nil {
			a.cfgMu.Unlock()
			return fmt.Errorf("save config: %w", err)
		}
	}
	a.Cfg.ServerURL, a.Cfg.FallbackURLs = next.ServerURL, next.FallbackURLs
	a.cfgMu.Unlock()
	return nil
}

// handleNetwork implements MsgNetwork: optionally join the tailnet, then switch the control
// URL after verifying it. The old URL is kept on any failure.
func (a *Agent) handleNetwork(ctx context.Context, p api.NetworkPayload) (string, error) {
	tc := a.tailnetClient()
	if p.AuthKey != "" {
		st := tc.Status(ctx)
		if !st.Installed {
			a.Log.Info("installing tailscale (requested by the control server)")
			if out, err := tc.Install(ctx); err != nil {
				return out, err
			}
		}
		host := p.Hostname
		if host == "" {
			h, _ := os.Hostname()
			host = tailnet.Hostname(h)
		}
		if _, err := tc.Up(ctx, p.AuthKey, host); err != nil {
			return "", errors.New(tailnet.Redact(err.Error(), p.AuthKey))
		}
	}
	st := tc.Status(ctx)
	res := api.NetworkResult{ControlURL: a.currentURL(), TailnetIP: st.IP, State: st.State()}
	if res.TailnetIP == "" {
		res.TailnetIP = tailnet.LocalIP()
	}
	want := strings.TrimRight(strings.TrimSpace(p.ControlURL), "/")
	switch {
	case want == "":
		res.Message = "control URL unchanged"
		if fb := strings.TrimRight(strings.TrimSpace(p.FallbackURL), "/"); fb != "" {
			a.cfgMu.Lock()
			if !slices.Contains(a.Cfg.ControlURLs(), fb) {
				a.Cfg.FallbackURLs = append(a.Cfg.FallbackURLs, fb)
				if a.CfgPath != "" {
					_ = SaveConfig(a.CfgPath, a.Cfg)
				}
				res.Message = "added fallback " + fb
			}
			a.cfgMu.Unlock()
		}
	case want == res.ControlURL:
		res.Message = "already using " + want
		if p.FallbackURL != "" {
			a.cfgMu.Lock()
			a.Cfg.SwitchServer(want, p.FallbackURL)
			if a.CfgPath != "" {
				_ = SaveConfig(a.CfgPath, a.Cfg)
			}
			a.cfgMu.Unlock()
		}
	default:
		if tailnet.IsTailnetURL(want) && res.TailnetIP == "" {
			return "", fmt.Errorf("this node is not on the tailnet (tailscale %s); join it first — keeping %s", res.State, res.ControlURL)
		}
		if err := a.SwitchControlURL(ctx, want, p.FallbackURL); err != nil {
			return "", fmt.Errorf("keeping %s: %w", res.ControlURL, err)
		}
		a.Log.Info("control URL switched", "from", res.ControlURL, "to", want)
		res.Switched, res.ControlURL = true, want
		res.Message = "switched; reconnecting"
		// Reply first (dispatch writes the result after we return), then reconnect.
		time.AfterFunc(500*time.Millisecond, a.dropConnection)
	}
	b, _ := json.Marshal(res)
	return string(b), nil
}
