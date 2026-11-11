package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuditExecDetailKeepsNoCommandText(t *testing.T) {
	cmd := []string{"/bin/sh", "-c", "PGPASSWORD=hunter2 psql -h db -c 'select 1'"}
	d := auditExecDetail(cmd)
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "hunter2") || strings.Contains(string(b), "psql") {
		t.Fatalf("audit detail leaks the command: %s", b)
	}
	if d.Program != "sh" || d.Args != 2 || d.Shell != "-c" || len(d.SHA256) != 64 {
		t.Fatalf("unexpected detail: %+v", d)
	}
	// the hash separates arguments, so ["a b"] and ["a","b"] differ
	if auditExecDetail([]string{"echo", "a b"}).SHA256 == auditExecDetail([]string{"echo", "a", "b"}).SHA256 {
		t.Fatal("hash ignores argument boundaries")
	}
	if auditExecDetail(cmd).SHA256 != d.SHA256 {
		t.Fatal("hash not stable")
	}
	if e := auditExecDetail([]string{"ls", "/"}); e.Shell != "" || e.Program != "ls" || e.Args != 1 {
		t.Fatalf("plain command: %+v", e)
	}
}
