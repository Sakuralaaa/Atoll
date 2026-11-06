package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
	"golang.org/x/crypto/bcrypt"
)

// memberEnv is a panel with two servers (srv_a, srv_b), one project on each (prj_a,
// prj_b) and member bob assigned srv_a, signed in through mc.
type memberEnv struct {
	*testEnv
	mc     *http.Client
	member *store.User
}

func newMemberEnv(t *testing.T, perms ...string) *memberEnv {
	t.Helper()
	return newMemberEnvWith(t, nil, perms...)
}

func newMemberEnvWith(t *testing.T, mod func(*Config), perms ...string) *memberEnv {
	t.Helper()
	e := newTestEnvWith(t, mod)
	e.setup()
	ctx := context.Background()
	st := e.srv.st
	for _, id := range []string{"srv_a", "srv_b"} {
		if err := st.CreateNode(ctx, api.Server{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []api.Project{{ID: "prj_a", Name: "a", ServerID: "srv_a"}, {ID: "prj_b", Name: "b", ServerID: "srv_b"}} {
		if err := st.CreateProject(ctx, p, false); err != nil {
			t.Fatal(err)
		}
	}
	if len(perms) == 0 {
		perms = []string{api.PermRead, api.PermDeploy}
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("member-pass"), bcrypt.MinCost)
	m, err := st.CreateMember(ctx, "bob", string(hash), perms, []string{"srv_a"}, false)
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	mc := &http.Client{Jar: jar}
	if code := e.doWith(mc, "", "POST", "/api/v1/auth/login", map[string]string{"username": "bob", "password": "member-pass"}, nil); code != 200 {
		t.Fatalf("member login %d", code)
	}
	return &memberEnv{testEnv: e, mc: mc, member: m}
}

// mdo is a request as the member's panel session.
func (e *memberEnv) mdo(method, path string, body, out any) int {
	return e.doWith(e.mc, "", method, path, body, out)
}

func storeMemberDisabled(v bool) store.MemberUpdate { return store.MemberUpdate{Disabled: &v} }
