package server

import (
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
	// the name is taken now
	e.mustDo("POST", "/api/v1/app-templates/memos", map[string]string{"server_id": nodeID, "name": "stats"}, nil, 409)
}
