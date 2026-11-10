package server

import (
	"context"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// The webhook secret rotate needs deploy for a member (it was session-only before), and a
// read-only member is not shown the secret.
func TestMemberWebhookRotateNeedsDeploy(t *testing.T) {
	for _, tc := range []struct {
		perms  []string
		rotate int
		secret bool
	}{
		{[]string{api.PermRead}, 403, false},
		{[]string{api.PermRead, api.PermDeploy}, 200, true},
	} {
		e := newMemberEnv(t, tc.perms...)
		ctx := context.Background()
		svc := api.Service{ID: "svc_g", ProjectID: "prj_a", Name: "app", Kind: "app", Image: "x", Status: "running", CreatedAt: time.Now()}
		if err := e.srv.st.CreateService(ctx, svc); err != nil {
			t.Fatal(err)
		}
		if err := e.srv.st.PutServiceGit(ctx, svc.ID, api.GitSource{Repo: "https://github.com/acme/app", Branch: "main"}, store.GitSecrets{}, "s3cret-webhook"); err != nil {
			t.Fatal(err)
		}
		var wh api.GitWebhook
		if code := e.mdo("GET", "/api/v1/services/svc_g/webhook", nil, &wh); code != 200 || (wh.Secret != "") != tc.secret {
			t.Fatalf("%v: webhook %d secret=%q", tc.perms, code, wh.Secret)
		}
		if code := e.mdo("POST", "/api/v1/services/svc_g/webhook/rotate", nil, nil); code != tc.rotate {
			t.Fatalf("%v: rotate %d, want %d", tc.perms, code, tc.rotate)
		}
		// the admin keeps rotating
		e.mustDo("POST", "/api/v1/services/svc_g/webhook/rotate", nil, nil, 200)
		if tc.rotate == 403 {
			// and the member cannot reach another member's/the admin's service at all
			if code := e.mdo("POST", "/api/v1/services/svc_nope/webhook/rotate", nil, nil); code != 403 && code != 404 {
				t.Fatalf("rotate unknown service: %d", code)
			}
		}
	}
}
