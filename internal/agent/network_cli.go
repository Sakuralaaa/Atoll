package agent

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent/tailnet"
)

// SwitchConfigURL is the offline variant of Agent.SwitchControlURL used by
// `atoll-agent tailnet switch|up`: verify url with the node token, then rewrite the config
// file (previous URLs become fallbacks). The running daemon picks it up on restart.
func SwitchConfigURL(ctx context.Context, cfgPath, url, fallback string, hc *http.Client, timeout time.Duration) (*Config, error) {
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	url = strings.TrimRight(strings.TrimSpace(url), "/")
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("url %q must start with http:// or https://", url)
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := tailnet.Probe(pctx, hc, url, cfg.NodeToken, cfg.NodeID); err != nil {
		return nil, fmt.Errorf("keeping %s: %w", cfg.ServerURL, err)
	}
	cfg.SwitchServer(url, fallback)
	if err := SaveConfig(cfgPath, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
