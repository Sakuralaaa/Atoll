package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// Every audited request names the account behind it (B6): admin or member, through a
// session or a token; rows from before keep working and derive the name from the actor.
func TestAuditRecordsUser(t *testing.T) {
	e := newMemberEnv(t)
	if code := e.mdo("POST", "/api/v1/projects", map[string]string{"name": "mine", "server_id": "srv_a"}, nil); code != 201 {
		t.Fatalf("member create project %d", code)
	}
	var tok api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]any{"name": "ops", "permissions": []string{"read", "delete"}}, &tok, 201)
	e.doWith(http.DefaultClient, tok.Token, "DELETE", "/api/v1/projects/prj_missing", nil, nil)
	// a row written by an older version: no user columns
	if _, err := e.srv.st.DB.Exec(`INSERT INTO audit_log(actor, action, target, created_at) VALUES('user:old','project.create','prj_x',?)`, store.TS(time.Now())); err != nil {
		t.Fatal(err)
	}

	var page api.AuditPage
	e.mustDo("GET", "/api/v1/audit?user=bob", nil, &page, 200)
	seen := map[string]bool{}
	for _, x := range page.Entries {
		if x.UserName != "bob" || x.UserRole != "member" || x.UserID != e.member.ID {
			t.Fatalf("member entry %+v", x)
		}
		seen[x.Action] = true
	}
	if !seen["auth.login"] || !seen["project.create"] {
		t.Fatalf("member actions %v", seen)
	}
	page = api.AuditPage{} // decode afresh: omitted fields would keep the previous row's values
	e.mustDo("GET", "/api/v1/audit?actor=token:", nil, &page, 200)
	if len(page.Entries) == 0 {
		t.Fatal("no token entries")
	}
	for _, x := range page.Entries {
		if x.UserName != "admin" || x.UserRole != "admin" || x.UserID == 0 {
			t.Fatalf("token entry names its owner: %+v", x)
		}
	}
	page = api.AuditPage{}
	e.mustDo("GET", "/api/v1/audit?user=old", nil, &page, 200)
	if len(page.Entries) != 1 || page.Entries[0].UserName != "old" || page.Entries[0].UserID != 0 || page.Entries[0].UserRole != "" {
		t.Fatalf("legacy row %+v", page.Entries)
	}
	// the audit log stays admin-only
	if code := e.mdo("GET", "/api/v1/audit", nil, nil); code != 403 {
		t.Fatalf("member audit %d", code)
	}
}
