package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/netguard"
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

// The default outbound clients never reach a loopback (admin/member webhooks) or a cloud
// metadata address (S3, registries) whatever URL is configured.
func TestOutboundClientsGuarded(t *testing.T) {
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer sink.Close()
	for name, hc := range map[string]*http.Client{"admin notify": adminNotifyHTTP(), "member notify": publicOnlyHTTP()} {
		if resp, err := hc.Get(sink.URL); err == nil {
			resp.Body.Close()
			t.Fatalf("%s client reached loopback", name)
		} else if !errors.Is(err, netguard.ErrBlocked) {
			t.Fatalf("%s: want a netguard refusal, got %v", name, err)
		}
	}
	// admin-configured endpoints may be local (MinIO on 127.0.0.1:9000) ...
	resp, err := adminEndpointHTTP(5 * time.Second).Get(sink.URL)
	if err != nil {
		t.Fatalf("endpoint client must reach a local endpoint: %v", err)
	}
	resp.Body.Close()
	// ... but never the metadata service (refused before any packet leaves)
	if _, err := adminEndpointHTTP(time.Second).Get("http://169.254.169.254/latest/meta-data/"); !errors.Is(err, netguard.ErrBlocked) {
		t.Fatalf("metadata endpoint not refused: %v", err)
	}
}

func TestBackupEndpointRefusesMetadata(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	for _, ep := range []string{"http://169.254.169.254", "metadata.google.internal", "http://100.100.100.200:80"} {
		if code := e.do("PUT", "/api/v1/settings", map[string]string{"backup_endpoint": ep}, nil); code != 400 {
			t.Fatalf("backup_endpoint %s: %d, want 400", ep, code)
		}
	}
	if code := e.do("PUT", "/api/v1/settings", map[string]string{"backup_endpoint": "http://10.0.0.5:9000"}, nil); code != 200 {
		t.Fatalf("private MinIO endpoint refused: %d", code)
	}
}
