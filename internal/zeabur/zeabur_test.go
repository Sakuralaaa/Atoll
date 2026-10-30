package zeabur

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fakeCLI emulates `npx zeabur@latest` from fixtures.
type fakeCLI struct {
	t     *testing.T
	runs  map[string]string          // args joined by space → fixture file
	host  func(script string) string // `server exec` script → stdout
	calls []string
}

func (f *fakeCLI) Run(ctx context.Context, json bool, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, key)
	if key == "auth status" {
		return []byte("INFO Logged in as dev@example.com\n"), nil
	}
	name, ok := f.runs[key]
	if !ok {
		return nil, fmt.Errorf("fake: no fixture for %q", key)
	}
	return fixture(f.t, name), nil
}

func (f *fakeCLI) Stream(ctx context.Context, w io.Writer, args ...string) error {
	f.calls = append(f.calls, strings.Join(args[:2], " "))
	if args[0] != "server" || f.host == nil {
		return fmt.Errorf("fake: unsupported stream %v", args[:2])
	}
	script := args[len(args)-1]
	io.WriteString(w, "INFO connecting to server\n")
	if strings.HasSuffix(script, "| base64") {
		inner := strings.TrimSuffix(strings.TrimPrefix(script, "set -o pipefail 2>/dev/null; ( "), " ) | base64")
		enc := base64.StdEncoding.EncodeToString([]byte(f.host(inner)))
		for len(enc) > 76 {
			io.WriteString(w, enc[:76]+"\n")
			enc = enc[76:]
		}
		io.WriteString(w, enc+"\n")
		return nil
	}
	io.WriteString(w, f.host(script))
	return nil
}

func TestParseZeaburJSON(t *testing.T) {
	ps, err := ParseProjects(fixture(t, "project_list.json"))
	if err != nil || len(ps) != 2 {
		t.Fatalf("%+v %v", ps, err)
	}
	if ps[0].ID != "66500000000000000000b001" || ps[0].DedicatedServerID() != "66500000000000000000s001" || ps[1].DedicatedServerID() != "" {
		t.Fatalf("%+v", ps)
	}
	wrapped, err := ParseProjects([]byte(`{"projects":[{"ID":"x","Name":"y","Region":{"ID":"server-z"}}]}`))
	if err != nil || wrapped[0].ID != "x" || wrapped[0].DedicatedServerID() != "z" {
		t.Fatalf("wrapped %+v %v", wrapped, err)
	}
	srv, err := ParseServer(fixture(t, "server_get.json"))
	if err != nil || srv.IsManaged || srv.IP != "203.0.113.7" {
		t.Fatalf("%+v %v", srv, err)
	}
	svcs, err := ParseServices(fixture(t, "service_list_blog.json"))
	if err != nil || len(svcs) != 2 || svcs[1].Name != "mysql" {
		t.Fatalf("log line before JSON must be skipped: %+v %v", svcs, err)
	}
	v, err := ParseVariables(fixture(t, "variables_ghost.json"))
	if err != nil || v.Own["database__client"] != "mysql" || v.Readonly["MYSQL_PORT"] != "3306" {
		t.Fatalf("%+v %v", v, err)
	}
	v, err = ParseVariables(fixture(t, "variables_mysql.json"))
	if err != nil || v.Own["MYSQL_ROOT_PASSWORD"] != "${PASSWORD}" || len(v.Readonly) != 0 {
		t.Fatalf("array form %+v %v", v, err)
	}
	ex, err := ParseExport(fixture(t, "export_blog.yaml"))
	if err != nil || len(ex.Spec.Services) != 2 {
		t.Fatalf("%+v %v", ex, err)
	}
	if got := strings.Join(ex.ExposedKeys()["mysql"], ","); got != "MYSQL_HOST,MYSQL_PASSWORD" {
		t.Fatalf("exposed %q", got)
	}
}

func sharedFake(t *testing.T) *fakeCLI {
	return &fakeCLI{t: t, runs: map[string]string{
		"project list": "project_list.json",
		"project export --id 66500000000000000000b002":       "export_blog.yaml",
		"service list --project-id 66500000000000000000b002": "service_list_blog.json",
		"service get --id 66500000000000000000a101":          "service_get_ghost.json",
		"service get --id 66500000000000000000a102":          "service_get_mysql.json",
		"variable list --id 66500000000000000000a101":        "variables_ghost.json",
		"variable list --id 66500000000000000000a102":        "variables_mysql.json",
		"domain list --id 66500000000000000000a101":          "domains_ghost.json",
		"domain list --id 66500000000000000000a102":          "domains_empty.json",
	}}
}

func envOf(s api.ImportService, k string) string {
	for _, e := range s.Env {
		if e.Key == k {
			return e.Value
		}
	}
	return "<missing>"
}

func TestSharedClusterInventoryAndPlan(t *testing.T) {
	cli := sharedFake(t)
	inv, err := GatherInventory(context.Background(), cli, InventoryOptions{ProjectIDs: []string{"blog"}})
	if err != nil {
		t.Fatal(err)
	}
	pi, ok := inv.Project("blog")
	if !ok || pi.ServerID != "" || pi.Host != nil {
		t.Fatalf("%+v", pi)
	}
	var report bytes.Buffer
	PrintReport(&report, inv)
	out := report.String()
	if strings.Contains(out, "Myp4ssw0rd") {
		t.Fatal("report leaks a secret")
	}
	for _, want := range []string{"ghost", "mysql", "myblog.zeabur.app", "shared cluster", "MYSQL_PASSWORD"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}

	plan := BuildPlan(pi, PlanOptions{TargetServer: "srv_aaaaaaaaaaaa", BaseDomain: "example.com"})
	if len(plan.Services) != 2 {
		t.Fatalf("%+v", plan.Services)
	}
	var ghost, mysql api.ImportService
	for _, s := range plan.Services {
		if s.Name == "ghost" {
			ghost = s
		} else {
			mysql = s
		}
	}
	if ghost.Kind != "image" || ghost.Image != "ghost:5" || len(ghost.Ports) != 1 || ghost.Ports[0].ContainerPort != 2368 || ghost.Ports[0].Protocol != "http" {
		t.Fatalf("ghost %+v", ghost)
	}
	if v := envOf(ghost, "url"); v != "https://myblog.example.com" {
		t.Errorf("url = %q", v)
	}
	if v := envOf(ghost, "database__connection__host"); v != "mysql" {
		t.Errorf("host = %q", v)
	}
	if v := envOf(ghost, "database__connection__password"); v != "Myp4ssw0rdMyp4ssw0rdMyp4ssw0rd12" {
		t.Errorf("password not expanded")
	}
	if len(ghost.Domains) != 1 || ghost.Domains[0] != "myblog.example.com" {
		t.Errorf("domains %v", ghost.Domains)
	}
	if mysql.Kind != "mysql" || envOf(mysql, "MYSQL_HOST") != "mysql" || len(mysql.Volumes) != 1 || mysql.Volumes[0].MountPath != "/var/lib/mysql" {
		t.Fatalf("mysql %+v", mysql)
	}
	for _, e := range mysql.Env {
		if e.Key == "MYSQL_PASSWORD" && !e.Exposed {
			t.Error("MYSQL_PASSWORD should be exposed (from export)")
		}
	}
	var pb bytes.Buffer
	PrintPlan(&pb, plan)
	if strings.Contains(pb.String(), "Myp4ssw0rd") {
		t.Fatal("plan summary leaks a secret")
	}
	if !strings.Contains(pb.String(), "shared cluster") {
		t.Error("plan should warn about the shared-cluster view")
	}
}

func TestDedicatedInventoryViaServerExec(t *testing.T) {
	cli := &fakeCLI{t: t, runs: map[string]string{
		"project list":                                       "project_list.json",
		"server get 66500000000000000000s001":                "server_get.json",
		"service list --project-id 66500000000000000000b001": "service_list_blog.json", // ids won't match k8s; fine
	}}
	cli.host = func(script string) string {
		switch {
		case strings.Contains(script, "get ns -l"):
			return "namespace/environment-66500000000000000000e001\n"
		case strings.Contains(script, "get deploy,sts"):
			return string(fixture(t, "ns_shop.yaml"))
		case strings.HasPrefix(script, "du -sb"):
			return string(fixture(t, "du.txt"))
		case strings.Contains(script, "images ls"):
			return string(fixture(t, "ctr.txt"))
		}
		t.Errorf("unexpected host script %q", script)
		return ""
	}
	inv, err := GatherInventory(context.Background(), cli, InventoryOptions{ProjectIDs: []string{"66500000000000000000b001"}})
	if err != nil {
		t.Fatal(err)
	}
	pi := &inv.Projects[0]
	if pi.Host == nil || pi.Namespace != "environment-66500000000000000000e001" || len(pi.K8s) != 4 || pi.Server == nil || pi.Server.IsManaged {
		t.Fatalf("%+v notes=%v", pi, pi.Notes)
	}
	plan := BuildPlan(pi, PlanOptions{TargetServer: "srv_x", BaseDomain: "example.com"})
	if len(plan.Services) != 3 {
		t.Fatalf("plan %d services", len(plan.Services))
	}
	var report bytes.Buffer
	PrintReport(&report, inv)
	if !strings.Contains(report.String(), "exportable") || strings.Contains(report.String(), "S3cretPassw0rd") {
		t.Fatalf("report:\n%s", report.String())
	}
	if AppServiceIDs(pi.K8s)[0] != "66500000000000000000a001" || len(AppServiceIDs(pi.K8s)) != 1 {
		t.Fatalf("app ids %v (databases/caches must never be suspended)", AppServiceIDs(pi.K8s))
	}
}

func TestNotLoggedIn(t *testing.T) {
	if !isUnauthorized([]byte("Error: 401 Unauthorized")) || isUnauthorized([]byte("ok")) {
		t.Fatal("isUnauthorized")
	}
	if !isGatewayTimeout([]byte("504 Gateway Timeout")) {
		t.Fatal("isGatewayTimeout")
	}
}

func TestBase64Filter(t *testing.T) {
	payload := bytes.Repeat([]byte{0, 1, 2, 250, 'x'}, 1000)
	enc := base64.StdEncoding.EncodeToString(payload)
	var in strings.Builder
	in.WriteString("INFO starting exec\n")
	for len(enc) > 76 {
		in.WriteString(enc[:76] + "\n")
		enc = enc[76:]
	}
	in.WriteString(enc + "\nERROR websocket closed\n")
	var out bytes.Buffer
	f := newBase64Filter(&out)
	io.Copy(f, strings.NewReader(in.String()))
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), payload) {
		t.Fatalf("decoded %d bytes, want %d", out.Len(), len(payload))
	}
}

func TestShq(t *testing.T) {
	if shq("it's") != `'it'"'"'s'` {
		t.Fatal(shq("it's"))
	}
}

func TestCountsAndVerify(t *testing.T) {
	src := ParseCounts("public.users|10\npublic.orders|5\nnoise\n")
	dst := ParseCounts("public.users\t10\npublic.orders\t4\n")
	rows := CompareCounts("db", src, dst)
	if len(rows) != 2 || rows[0].Table != "public.orders" || rows[0].OK() || !rows[1].OK() {
		t.Fatalf("%+v", rows)
	}
	var b bytes.Buffer
	if PrintCounts(&b, rows) {
		t.Fatal("mismatch must report false")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "x.pgdump")
	os.WriteFile(p, []byte("PGDMP..."), 0o600)
	if err := VerifyArchive("postgres", p); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p, []byte("-- MySQL dump\n"), 0o600)
	if err := VerifyArchive("mysql", p); err == nil {
		t.Fatal("truncated mysql dump must fail")
	}
	// volume tarball round trip
	f := filepath.Join(dir, "a.txt")
	os.WriteFile(f, []byte("hello"), 0o600)
	tgz := filepath.Join(dir, "v.tar.gz")
	out, _ := os.Create(tgz)
	if err := tarGz(out, map[string]string{"a.txt": f}); err != nil {
		t.Fatal(err)
	}
	out.Close()
	if err := VerifyArchive("volume", tgz); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareForApply(t *testing.T) {
	plan := api.ImportPlan{Services: []api.ImportService{
		{Name: "web", Domains: []string{"shop.example.com"}},
		{Name: "postgresql", Kind: "postgres", Volumes: []api.Volume{{Name: "data", MountPath: "/var/lib/postgresql/data"}}},
	}}
	man := &DataManifest{Entries: []DataEntry{{Service: "postgresql", Kind: "postgres", Database: "zeabur", File: "postgresql/zeabur.pgdump"}}}
	pending := PrepareForApply(&plan, man, false)
	if len(plan.Services[0].Domains) != 0 || pending["web"][0] != "shop.example.com" {
		t.Fatalf("domains must be held for cutover: %+v %v", plan.Services[0], pending)
	}
	if v := plan.Services[1].Volumes; len(v) != 2 || v[1].Name != importVolume {
		t.Fatalf("import volume missing: %+v", v)
	}
	cmd := RestoreCmd(man.Entries[0])
	if !strings.Contains(cmd, "pg_restore") || !strings.Contains(cmd, "/atoll-import/zeabur.pgdump") || !strings.Contains(cmd, "127.0.0.1") {
		t.Fatalf("restore cmd %q", cmd)
	}
}

func TestSecretFileMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "plan.json")
	if err := WriteSecretJSON(p, map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}

func TestParseDomainMap(t *testing.T) {
	m, err := ParseDomainMap([]string{"App.zeabur.app=app.example.com", "old.zeabur.app="})
	if err != nil || m["app.zeabur.app"] != "app.example.com" || m["old.zeabur.app"] != "" {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := ParseDomainMap([]string{"nope"}); err == nil {
		t.Fatal("want error")
	}
}
