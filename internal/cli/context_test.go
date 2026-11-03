package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func cliWithConfig(t *testing.T, cfg string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	app := &App{Version: "test", In: strings.NewReader(""), Out: &out, Err: io.Discard}
	root := NewRoot(app)
	root.SetArgs(append([]string{"--config", cfg}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func TestContextSwitchesPanels(t *testing.T) {
	_, a := newFake(t)
	_, b := newFake(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	// A config from before contexts: becomes the "default" panel.
	if _, err := SaveConfig(cfg, Config{URL: a.URL, Token: "atl_pat_test", User: "chen"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cliWithConfig(t, cfg, "login", "--name", "staging", "--url", b.URL, "--token", "atl_pat_test"); err != nil {
		t.Fatal(err)
	}
	c, _ := LoadConfig(cfg)
	if c.Current != "staging" || c.URL != b.URL || len(c.Panels) != 2 || c.Panels["default"].URL != a.URL {
		t.Fatalf("after login: %+v", c)
	}
	out, err := cliWithConfig(t, cfg, "context", "ls")
	if err != nil || !regexp.MustCompile(`\*\s+staging`).MatchString(out) || !strings.Contains(out, "default") {
		t.Fatalf("ls: %q %v", out, err)
	}
	if _, err := cliWithConfig(t, cfg, "context", "use", "default"); err != nil {
		t.Fatal(err)
	}
	if c, _ = LoadConfig(cfg); c.URL != a.URL || c.Current != "default" {
		t.Fatalf("after use: %+v", c)
	}
	// --panel picks another panel for one command without switching.
	if _, err := cliWithConfig(t, cfg, "--panel", "staging", "servers", "ls"); err != nil {
		t.Fatal(err)
	}
	if _, err := cliWithConfig(t, cfg, "--panel", "nope", "servers", "ls"); err == nil || !strings.Contains(err.Error(), "no saved panel") {
		t.Fatalf("unknown panel: %v", err)
	}
	if _, err := cliWithConfig(t, cfg, "context", "use", "nope"); err == nil {
		t.Fatal("use of unknown panel must fail")
	}
	if _, err := cliWithConfig(t, cfg, "context", "rm", "default"); err != nil {
		t.Fatal(err)
	}
	if c, _ = LoadConfig(cfg); c.URL != "" || c.Current != "" || len(c.Panels) != 1 {
		t.Fatalf("after rm current: %+v", c)
	}
	st, _ := os.Stat(cfg)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
}

func TestCompletionOffersServices(t *testing.T) {
	f, srv := newFake(t)
	f.services = []api.Service{{ID: "svc_1", Name: "api", ProjectID: "prj_1"}, {ID: "svc_2", Name: "worker", ProjectID: "prj_1"}}
	cfg := filepath.Join(t.TempDir(), "config.json")
	if _, err := SaveConfig(cfg, Config{URL: srv.URL, Token: "atl_pat_test"}); err != nil {
		t.Fatal(err)
	}
	out, err := cliWithConfig(t, cfg, "__complete", "restart", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "api\tweb\n") || !strings.Contains(out, "worker\tweb\n") {
		t.Fatalf("completion output %q", out)
	}
	out, err = cliWithConfig(t, cfg, "__complete", "env", "ls", "-s", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "api\tweb") {
		t.Fatalf("flag completion %q", out)
	}
}
