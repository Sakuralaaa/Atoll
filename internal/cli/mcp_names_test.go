package cli

import (
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestMCPToolNames(t *testing.T) {
	re := regexp.MustCompile(`^[a-z]+_[a-z_]+$`)
	cur := map[string]bool{}
	for _, st := range MCPTools(&App{Version: "test", MCPNoLegacy: true}) {
		n := st.Tool.Name
		if cur[n] {
			t.Errorf("duplicate tool %s", n)
		}
		cur[n] = true
		if !re.MatchString(n) {
			t.Errorf("tool %s is not <resource>_<action>", n)
		}
		if _, old := mcpLegacyNames[n]; old {
			t.Errorf("legacy name %s served with --legacy-names=false", n)
		}
		if MCPToolPermission(n) == "" {
			t.Errorf("tool %s has no permission", n)
		}
	}
	for _, n := range []string{"service_stop", "service_start", "service_exec", "env_get", "env_set", "domain_list", "domain_add", "domain_remove", "service_restart"} {
		if !cur[n] {
			t.Errorf("missing tool %s", n)
		}
	}
	all := map[string]string{}
	for _, st := range MCPTools(&App{Version: "test"}) {
		all[st.Tool.Name] = st.Tool.Description
	}
	for old, n := range mcpLegacyNames {
		if !cur[n] {
			t.Errorf("legacy %s points to unknown tool %s", old, n)
		}
		if !strings.HasPrefix(all[old], "Deprecated alias of "+n) {
			t.Errorf("alias %s: %q", old, all[old])
		}
		if MCPToolPermission(old) != MCPToolPermission(n) {
			t.Errorf("alias %s permission differs", old)
		}
	}
	if MCPToolPermission("service_exec") != api.PermExec || MCPToolPermission("domain_remove") != api.PermDomains {
		t.Fatal("exec needs exec, domain_remove needs domains")
	}
}

func TestMCPExecAndDomainRemove(t *testing.T) {
	f, srv := newFake(t)
	f.services = []api.Service{{ID: "svc_1", Name: "api", ProjectID: "prj_1"}}
	f.extra = func(w http.ResponseWriter, r *http.Request, key string, body []byte) bool {
		switch key {
		case "POST /services/svc_1/exec":
			code := 2
			writeJSON(w, 200, api.ResultPayload{OK: false, Error: "exit code 2", Output: "nope\n", ExitCode: &code})
		case "GET /services/svc_1/domains":
			writeJSON(w, 200, []api.Domain{{ID: "dom_1", Hostname: "App.example.com", ServiceID: "svc_1"}})
		case "DELETE /domains/dom_1":
			w.WriteHeader(204)
		default:
			return false
		}
		return true
	}
	app := &App{Version: "test", URL: srv.URL, Token: "atl_pat_test", ConfigPath: filepath.Join(t.TempDir(), "c.json")}
	isErr, txt := mcpCall(t, app, "service_exec", map[string]any{"service": "api", "cmd": []any{"sh", "-c", "false"}, "timeout_sec": 5})
	if isErr || !strings.Contains(txt, `"exit_code": 2`) {
		t.Fatalf("exec: %v %s", isErr, txt)
	}
	if !strings.Contains(string(f.bodies["POST /services/svc_1/exec"]), `"timeout_sec":5`) {
		t.Fatalf("exec body %s", f.bodies["POST /services/svc_1/exec"])
	}
	isErr, txt = mcpCall(t, app, "domain_remove", map[string]any{"service": "api", "hostname": "app.example.com"})
	if isErr || !f.called("DELETE /domains/dom_1") {
		t.Fatalf("domain_remove: %v %s", isErr, txt)
	}
	if isErr, _ := mcpCall(t, app, "domain_remove", map[string]any{"service": "api", "hostname": "other.example.com"}); !isErr {
		t.Fatal("removing an unknown domain must fail")
	}
}
