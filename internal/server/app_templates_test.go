package server

import (
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestAppTemplatesAPI(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var list []api.AppTemplate
	e.mustDo("GET", "/api/v1/app-templates", nil, &list, 200)
	if len(list) < 10 {
		t.Fatalf("templates: %d", len(list))
	}
	e.mustDo("POST", "/api/v1/app-templates/nope", map[string]string{"server_id": nodeID}, nil, 404)
	e.mustDo("POST", "/api/v1/app-templates/ghost", map[string]string{"server_id": nodeID}, nil, 400) // needs a domain

	var res api.AppTemplateResult
	e.mustDo("POST", "/api/v1/app-templates/umami", map[string]string{"server_id": nodeID, "name": "stats"}, &res, 201)
	if res.Project == nil || res.Project.Name != "stats" || len(res.Services) != 2 || res.Services[0].Kind != "postgres" || res.Services[1].Image == "" {
		t.Fatalf("result: %+v", res)
	}
	var vars []api.EnvVar
	e.mustDo("GET", "/api/v1/services/"+res.Services[1].ID+"/env?reveal=1", nil, &vars, 200)
	got := map[string]string{}
	for _, v := range vars {
		got[v.Key] = v.Value
	}
	if len(got["APP_SECRET"]) != 32 || got["DATABASE_URL"] != "${POSTGRES_CONNECTION_STRING}" {
		t.Fatalf("env: %+v", got)
	}
	// config files are carried over with their placeholders filled
	e.mustDo("POST", "/api/v1/app-templates/grok2api", map[string]any{"server_id": nodeID, "inputs": map[string]string{"ADMIN_PASSWORD": "typed-pass-1"}}, &res, 201)
	var svc api.Service
	e.mustDo("GET", "/api/v1/services/"+res.Services[0].ID, nil, &svc, 200)
	if len(svc.Configs) != 1 || svc.Configs[0].Path != "/run/grok2api/config.yaml" || strings.Contains(svc.Configs[0].Content, "{{") ||
		!strings.Contains(svc.Configs[0].Content, "credentialEncryptionKey: \"") {
		t.Fatalf("configs: %+v", svc.Configs)
	}
	if !strings.Contains(svc.Configs[0].Content, `password: "typed-pass-1"`) {
		t.Fatalf("input not filled: %s", svc.Configs[0].Content)
	}
	e.mustDo("POST", "/api/v1/app-templates/outlook-email", map[string]any{"server_id": nodeID, "inputs": map[string]string{"NOPE": "typed-pass-1"}}, nil, 400)
	e.mustDo("POST", "/api/v1/app-templates/outlook-email", map[string]any{"server_id": nodeID, "inputs": map[string]string{"LOGIN_PASSWORD": ""}}, &res, 201)
	e.mustDo("GET", "/api/v1/services/"+res.Services[0].ID+"/env?reveal=1", nil, &vars, 200)
	found := false
	for _, v := range vars {
		if v.Key == "LOGIN_PASSWORD" {
			found = len(v.Value) == 32 && v.Secret
		}
	}
	if !found {
		t.Fatalf("empty input not generated: %+v", vars)
	}
	// the name is taken now
	e.mustDo("POST", "/api/v1/app-templates/memos", map[string]string{"server_id": nodeID, "name": "stats"}, nil, 409)
}
