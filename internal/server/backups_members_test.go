package server

import (
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// B4: a member sees and restores only the backups of its projects (404 otherwise), needs
// restore to restore, restores onto its own servers only, and never sees repository checks.
func TestMemberBackupsScoped(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeA := startFakeAgent(t, e, api.ZeaburInventory{})
	_, nodeB := startFakeAgent(t, e, api.ZeaburInventory{})
	_, mine, _ := backupProject(t, e, nodeA)
	var other api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "admin-only", "server_id": nodeB}, &other, 201)
	var theirs api.Service
	e.mustDo("POST", "/api/v1/projects/"+other.ID+"/services", map[string]any{"kind": "postgres", "name": "pg"}, &theirs, 201)
	e.mustDo("PUT", "/api/v1/settings", backupSettings(), nil, 200)
	doneBackup(t, e, mine.ID, nodeA, "bak_mine0000001")
	doneBackup(t, e, theirs.ID, nodeB, "bak_their000001")

	ro := memberClient(t, e, "ro", []string{api.PermRead}, []string{nodeA})
	rw := memberClient(t, e, "rw", []string{api.PermRead, api.PermRestore}, []string{nodeA})
	for _, c := range []struct {
		method, path string
		body         any
		want         int
	}{
		{"GET", "/api/v1/services/" + theirs.ID + "/backups", nil, 404},
		{"GET", "/api/v1/services/" + theirs.ID + "/restores", nil, 404},
		{"GET", "/api/v1/services/" + theirs.ID + "/backup-policy", nil, 404},
		{"GET", "/api/v1/services/" + theirs.ID + "/backup-verify", nil, 404},
		{"GET", "/api/v1/backups/bak_their000001", nil, 404},
		{"POST", "/api/v1/backups/bak_their000001/restore", nil, 404},
		{"GET", "/api/v1/backups/bak_mine0000001", nil, 200},
		{"GET", "/api/v1/services/" + mine.ID + "/backups", nil, 200},
		{"GET", "/api/v1/backup-verify", nil, 403},
		{"POST", "/api/v1/backup-verify/check", nil, 403},
		{"GET", "/api/v1/settings/backup/password", nil, 403},
	} {
		if code := e.doWith(rw, "", c.method, c.path, c.body, nil); code != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, code, c.want)
		}
	}
	// restore needs the restore permission
	if code := e.doWith(ro, "", "POST", "/api/v1/backups/bak_mine0000001/restore", nil, nil); code != 403 {
		t.Errorf("restore without the permission: %d", code)
	}
	// only onto its own servers
	if code := e.doWith(rw, "", "POST", "/api/v1/backups/bak_mine0000001/restore", api.BackupRestoreRequest{TargetServerID: nodeB}, nil); code != 400 {
		t.Errorf("restore onto another server: %d", code)
	}
	var job api.RestoreJob
	if code := e.doWith(rw, "", "POST", "/api/v1/backups/bak_mine0000001/restore", nil, &job); code != 200 || job.ID == "" {
		t.Fatalf("restore in place: %d %+v", code, job)
	}
	if code := e.doWith(rw, "", "GET", "/api/v1/restores/"+job.ID, nil, nil); code != 200 {
		t.Errorf("own restore job: %d", code)
	}
	// repository checks (no service) are the admin's
	if err := e.srv.st.SaveVerification(e.srv.bg, api.BackupVerification{ID: "bvf_check000001", Kind: api.BackupVerifyCheck, Status: "ok", Steps: []api.BackupVerifyStep{}}); err != nil {
		t.Fatal(err)
	}
	var vs []api.BackupVerification
	if code := e.doWith(rw, "", "GET", "/api/v1/backup-verifications", nil, &vs); code != 200 || len(vs) != 0 {
		t.Errorf("member verifications: %d %+v", code, vs)
	}
	e.mustDo("GET", "/api/v1/backup-verifications", nil, &vs, 200)
	if len(vs) != 1 {
		t.Errorf("admin verifications: %+v", vs)
	}
	var st api.BackupVerifyStatus
	if code := e.doWith(rw, "", "GET", "/api/v1/services/"+mine.ID+"/backup-verify", nil, &st); code != 200 || st.Check != nil || st.CheckServerID != "" {
		t.Errorf("member verify status shows the repository check: %d %+v", code, st)
	}
}
