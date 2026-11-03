package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Config is ~/.config/atoll/config.json (mode 0600).
//
// URL/Token/User are the current panel. Panels holds every named panel (`atoll context`);
// the top-level fields always mirror Panels[Current], so older CLIs keep working.
type Config struct {
	URL     string           `json:"url"`
	Token   string           `json:"token"`
	User    string           `json:"user,omitempty"`
	Current string           `json:"current,omitempty"`
	Panels  map[string]Panel `json:"panels,omitempty"`
}

// Panel is one saved panel login.
type Panel struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	User  string `json:"user,omitempty"`
}

// defaultPanel names a login saved before contexts existed, or with no --name.
const defaultPanel = "default"

// normalize turns a pre-context config (only top-level fields) into one named panel.
func (c *Config) normalize() {
	if c.Panels == nil {
		c.Panels = map[string]Panel{}
	}
	if len(c.Panels) == 0 && c.URL != "" {
		c.Panels[defaultPanel] = Panel{URL: c.URL, Token: c.Token, User: c.User}
		c.Current = defaultPanel
	}
}

// Put saves a panel under name and makes it current.
func (c *Config) Put(name string, p Panel) {
	c.normalize()
	c.Panels[name] = p
	c.Use(name)
}

// Use makes name current; it must exist.
func (c *Config) Use(name string) bool {
	c.normalize()
	p, ok := c.Panels[name]
	if !ok {
		return false
	}
	c.Current, c.URL, c.Token, c.User = name, p.URL, p.Token, p.User
	return true
}

// Remove deletes a panel. Removing the current one clears the top-level login.
func (c *Config) Remove(name string) bool {
	c.normalize()
	if _, ok := c.Panels[name]; !ok {
		return false
	}
	delete(c.Panels, name)
	if c.Current == name {
		c.Current, c.URL, c.Token, c.User = "", "", "", ""
	}
	return true
}

// Panel returns the login for name, or the current one for "".
func (c Config) Panel(name string) (Panel, bool) {
	if name == "" {
		return Panel{URL: c.URL, Token: c.Token, User: c.User}, c.URL != ""
	}
	c.normalize()
	p, ok := c.Panels[name]
	return p, ok
}

// ConfigPath resolves the config file: explicit path, $ATOLL_CONFIG, $XDG_CONFIG_HOME/atoll,
// ~/.config/atoll.
func ConfigPath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if v := os.Getenv("ATOLL_CONFIG"); v != "" {
		return v, nil
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "atoll", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "atoll", "config.json"), nil
}

// LoadConfig reads the config; a missing file is an empty config.
func LoadConfig(explicit string) (Config, error) {
	var c Config
	p, err := ConfigPath(explicit)
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

// SaveConfig writes the config with mode 0600 (directory 0700).
func SaveConfig(explicit string, c Config) (string, error) {
	p, err := ConfigPath(explicit)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return "", err
	}
	_, err = f.Write(append(b, '\n'))
	return p, err
}
