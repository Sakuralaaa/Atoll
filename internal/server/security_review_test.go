package server

import (
	"context"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// env/references must not tell a member about services placed on servers it is not assigned.
func TestMemberEnvReferencesHideUnassignedServices(t *testing.T) {
	e := newMemberEnv(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, s := range []struct{ id, name, node string }{{"svc_a", "visible", "srv_a"}, {"svc_b", "hidden", "srv_b"}} {
		if err := e.srv.st.CreateService(ctx, api.Service{ID: s.id, ProjectID: "prj_a", Name: s.name, Kind: "image", Image: "nginx:1.27", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := e.srv.st.SetServiceServer(ctx, s.id, s.node); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.srv.st.ReplaceEnv(ctx, "svc_b", []api.EnvVar{{Key: "TOKEN_URL", Value: "https://hidden.internal", Exposed: true}}); err != nil {
		t.Fatal(err)
	}
	var refs []api.EnvVar
	if code := e.mdo("GET", "/api/v1/services/svc_a/env/references", nil, &refs); code != 200 {
		t.Fatalf("references: %d", code)
	}
	for _, r := range refs {
		if r.Source == "hidden" {
			t.Fatalf("member saw a reference of an unassigned service: %+v", r)
		}
	}
	// the admin still sees it
	refs = nil
	if code := e.do("GET", "/api/v1/services/svc_a/env/references", nil, &refs); code != 200 {
		t.Fatalf("admin references: %d", code)
	}
	found := false
	for _, r := range refs {
		found = found || (r.Source == "hidden" && r.Key == "TOKEN_URL")
	}
	if !found {
		t.Fatalf("admin lost references: %+v", refs)
	}
}
