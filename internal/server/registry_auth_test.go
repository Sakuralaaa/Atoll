package server

import (
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestCreateServiceRegistryAuth(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "priv", "server_id": nodeID}, &p, 201)
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "app", "kind": "image", "image": "ghcr.io/me/app:1",
		"registry_auth": map[string]string{"username": "me", "password": "pat-secret"},
	}, &svc, 201)
	if !strings.HasPrefix(svc.RegistryCredID, "reg_") {
		t.Fatalf("registry_cred_id = %q", svc.RegistryCredID)
	}
	_, body := e.rawDo("GET", "/api/v1/services/"+svc.ID, nil)
	if strings.Contains(string(body), "pat-secret") {
		t.Fatal("password returned")
	}
	full, _, err := e.srv.st.GetService(e.srv.bg, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	spec, _, err := e.srv.buildSpec(e.srv.bg, full, "dep_x")
	if err != nil {
		t.Fatal(err)
	}
	if spec.RegistryAuth == nil || spec.RegistryAuth.Username != "me" || spec.RegistryAuth.Password != "pat-secret" || spec.RegistryAuth.Server != "ghcr.io" {
		t.Fatalf("spec auth = %+v", spec.RegistryAuth)
	}
	// half a credential is refused; a client-chosen registry_cred_id is ignored
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "app2", "kind": "image", "image": "nginx:1", "registry_auth": map[string]string{"username": "me"},
	}, nil, 400)
	var other api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "app3", "kind": "image", "image": "nginx:1", "registry_cred_id": svc.RegistryCredID,
	}, &other, 201)
	if other.RegistryCredID != "" {
		t.Fatalf("client registry_cred_id kept: %q", other.RegistryCredID)
	}
	// deleting the service drops its credential
	if err := e.srv.execDeleteService(e.srv.bg, svc.ID, "test"); err != nil {
		t.Fatal(err)
	}
	var cred api.RegistryAuth
	if err := e.srv.st.GetSecretBlob(e.srv.bg, "registry_cred:"+svc.RegistryCredID, &cred); err == nil {
		t.Fatal("credential kept after delete")
	}
}
