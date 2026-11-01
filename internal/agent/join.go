package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Join registers this host with the control server and writes the agent config.
// hello is collected by the caller (CollectHello) so tests can inject it.
func Join(ctx context.Context, serverURL, joinToken string, hello api.HelloPayload, configPath string) (*Config, error) {
	serverURL = strings.TrimRight(serverURL, "/")
	if serverURL == "" || joinToken == "" {
		return nil, fmt.Errorf("--server and --token are required")
	}
	body, _ := json.Marshal(api.JoinRequest{JoinToken: joinToken, Hello: hello})
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/agent/v1/join", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("join: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		var eb api.ErrorBody
		if json.Unmarshal(b, &eb) == nil && eb.Error != "" {
			return nil, fmt.Errorf("join rejected: %s (%s)", eb.Error, resp.Status)
		}
		return nil, fmt.Errorf("join rejected: %s", resp.Status)
	}
	var jr api.JoinResponse
	if err := json.Unmarshal(b, &jr); err != nil {
		return nil, fmt.Errorf("join: bad response: %w", err)
	}
	if jr.NodeToken == "" || jr.NodeID == "" {
		return nil, fmt.Errorf("join: server returned no node id/token")
	}
	cfg := &Config{ServerURL: serverURL, NodeID: jr.NodeID, NodeToken: jr.NodeToken, DataDir: DefaultDataDir}
	// Keep local overrides from an existing config (re-join).
	if old, err := LoadConfig(configPath); err == nil {
		cfg.DataDir, cfg.Firewall, cfg.CaddyImage = old.DataDir, old.Firewall, old.CaddyImage
	}
	if err := SaveConfig(configPath, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// FetchNetwork asks the control server for the private-network settings (GET
// /agent/v1/network with the node token), trying every configured control URL in order.
func FetchNetwork(ctx context.Context, cfg *Config) (api.BootstrapResponse, error) {
	var last error
	for _, base := range cfg.ControlURLs() {
		rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		req, err := http.NewRequestWithContext(rctx, http.MethodGet, base+"/agent/v1/network", nil)
		if err != nil {
			cancel()
			return api.BootstrapResponse{}, err
		}
		req.Header.Set("Authorization", "Bearer "+cfg.NodeToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			last = err
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		cancel()
		if resp.StatusCode/100 != 2 {
			last = fmt.Errorf("%s: %s", base, resp.Status)
			continue
		}
		var br api.BootstrapResponse
		if err := json.Unmarshal(b, &br); err != nil {
			last = fmt.Errorf("%s: bad response: %w", base, err)
			continue
		}
		return br, nil
	}
	if last == nil {
		last = fmt.Errorf("no control URL configured")
	}
	return api.BootstrapResponse{}, last
}
