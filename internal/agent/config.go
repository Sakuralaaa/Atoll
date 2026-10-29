package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Version is the agent version, set at build time with -ldflags "-X github.com/atoll-sh/atoll/internal/agent.Version=v1.2.3".
var Version = "dev"

const (
	DefaultConfigPath = "/etc/atoll/agent.json"
	DefaultDataDir    = "/var/lib/atoll"

	LabelService    = "atoll.service"
	LabelProject    = "atoll.project"
	LabelDeployment = "atoll.deployment"
	LabelSpecHash   = "atoll.spec-hash"
	LabelManaged    = "atoll.managed"
	LabelRole       = "atoll.role"

	IngressNetwork = "atoll-ingress"
	CaddyContainer = "atoll-caddy"
)

// Config is /etc/atoll/agent.json, written by `atoll-agent join` (mode 0600).
type Config struct {
	ServerURL string `json:"server_url"`
	NodeID    string `json:"node_id"`
	NodeToken string `json:"node_token"`
	// DataDir defaults to /var/lib/atoll.
	DataDir string `json:"data_dir,omitempty"`
	// Firewall enables the nftables Cloudflare-only rules for 80/443 when the server asks
	// for them. nil = enabled. Set false to never touch the host firewall.
	Firewall *bool `json:"firewall,omitempty"`
	// CaddyImage overrides the ingress image (default caddy:2).
	CaddyImage string `json:"caddy_image,omitempty"`
}

func (c *Config) FirewallEnabled() bool { return c.Firewall == nil || *c.Firewall }

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.DataDir == "" {
		c.DataDir = DefaultDataDir
	}
	if c.ServerURL == "" || c.NodeToken == "" {
		return nil, fmt.Errorf("%s: server_url and node_token are required (run `atoll-agent join`)", path)
	}
	return &c, nil
}

func SaveConfig(path string, c *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(b, '\n'), 0o600)
}

// Paths are the on-disk locations under the data dir.
type Paths struct{ Root string }

func (p Paths) StateDir() string                  { return filepath.Join(p.Root, "state") }
func (p Paths) StateFile(serviceID string) string { return filepath.Join(p.Root, "state", serviceID+".json") }
func (p Paths) ConfigsDir(serviceID string) string {
	return filepath.Join(p.Root, "configs", serviceID)
}
func (p Paths) VolumesDir(serviceID string) string {
	return filepath.Join(p.Root, "volumes", serviceID)
}
func (p Paths) VolumeDir(serviceID, name string) string {
	return filepath.Join(p.Root, "volumes", serviceID, name)
}
func (p Paths) IngressFile() string { return filepath.Join(p.Root, "ingress", "ingress.json") }
func (p Paths) TLSDir() string      { return filepath.Join(p.Root, "tls") }
func (p Paths) CaddyDir() string    { return filepath.Join(p.Root, "caddy") }
func (p Paths) FirewallDir() string { return filepath.Join(p.Root, "firewall") }
func (p Paths) TmpDir() string      { return filepath.Join(p.Root, "tmp") }

// writeFileAtomic writes to a temp file in the same dir, fsyncs and renames over path.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}
