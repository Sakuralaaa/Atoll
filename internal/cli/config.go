package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Config is ~/.config/atoll/config.json (mode 0600).
type Config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	User  string `json:"user,omitempty"`
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
