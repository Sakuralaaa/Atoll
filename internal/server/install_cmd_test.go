package server

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/install"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestInstallScriptsServedAndUnknownFilesAre404(t *testing.T) {
	e := newTestEnv(t)
	get := func(p string) (int, string, string) {
		resp, err := http.Get(e.ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
	}
	code, ct, body := get("/install-agent.sh")
	if code != 200 || !strings.HasPrefix(ct, "text/x-shellscript") || body != string(install.AgentScript) {
		t.Fatalf("install-agent.sh: %d %q", code, ct)
	}
	if code, _, body = get("/install-server.sh"); code != 200 || body != string(install.ServerScript) {
		t.Fatalf("install-server.sh: %d", code)
	}
	for _, p := range []string{"/install.sh", "/foo/bar.tar.gz", "/assets/missing.js", "/api/v1/nope", "/agent/v9/x"} {
		if code, _, body := get(p); code != 404 || strings.Contains(body, "<html") {
			t.Fatalf("%s: %d %.40q", p, code, body)
		}
	}
	if code, ct, _ := get("/"); code != 200 || !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("/: %d %q", code, ct)
	}
}

func TestInstallCommandPinsVersionAndScript(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })

	Version = "dev"
	if u := installScriptURL("http://1.2.3.4:7300", "agent"); u != repoRaw+"/main/install/install-agent.sh" {
		t.Fatalf("dev over http: %s", u)
	}
	Version = "v0.1.7"
	if u := installScriptURL("http://1.2.3.4:7300", "agent"); u != repoRaw+"/refs/tags/v0.1.7/install/install-agent.sh" {
		t.Fatalf("release over http: %s", u)
	}
	if u := installScriptURL("https://panel.example.com/", "agent"); u != "https://panel.example.com/install-agent.sh" {
		t.Fatalf("https: %s", u)
	}

	e := newTestEnv(t)
	e.setup()
	var jt api.JoinToken
	e.mustDo("POST", "/api/v1/servers/join-token", nil, &jt, 201)
	if strings.Contains(jt.Command, "/main/") || !strings.Contains(jt.Command, "--token atl_join_") || !strings.HasSuffix(jt.Command, " --version v0.1.7") {
		t.Fatalf("join command: %s", jt.Command)
	}
	var ic api.InstallCommand
	e.mustDo("GET", "/api/v1/servers/agent-install-command", nil, &ic, 200)
	if strings.Contains(ic.Command, "--token") || !strings.Contains(ic.Command, "--version v0.1.7") || ic.Version != "v0.1.7" {
		t.Fatalf("install command: %+v", ic)
	}
}
