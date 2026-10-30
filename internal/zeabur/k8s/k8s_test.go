package k8s

import (
	"os"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

const (
	nsName = "environment-66500000000000000000e001"
	webID  = "66500000000000000000a001"
	pgID   = "66500000000000000000a002"
	rdID   = "66500000000000000000a003"
)

func load(t *testing.T) []api.ZeaburProject {
	t.Helper()
	data, err := os.ReadFile("testdata/namespace.yaml")
	if err != nil {
		t.Fatal(err)
	}
	objs, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	du, _ := os.ReadFile("testdata/du.txt")
	ctr, _ := os.ReadFile("testdata/ctr.txt")
	return ScanNamespaces(objs, HostFacts{DirBytes: ParseDu(string(du)), CachedImages: ParseCtrImages(string(ctr))})
}

func find(t *testing.T, svcs []api.ZeaburService, id string) api.ZeaburService {
	t.Helper()
	for _, s := range svcs {
		if s.ServiceID == id {
			return s
		}
	}
	t.Fatalf("service %s not found", id)
	return api.ZeaburService{}
}

func envVal(env []api.EnvVar, k string) (string, bool) {
	for _, e := range env {
		if e.Key == k {
			return e.Value, true
		}
	}
	return "", false
}

func TestScanNamespace(t *testing.T) {
	projects := load(t)
	if len(projects) != 1 {
		t.Fatalf("want 1 project, got %d", len(projects))
	}
	p := projects[0]
	if p.Namespace != nsName || p.ProjectName != "shop" || p.EnvironmentName != "production" || p.ProjectID != "66500000000000000000b001" {
		t.Fatalf("project meta %+v", p)
	}
	if len(p.Services) != 4 {
		t.Fatalf("want 4 services (3 + suspended), got %d", len(p.Services))
	}

	web := find(t, p.Services, webID)
	if web.Name != "web" || web.Workload != "deployment/service-"+webID || !web.ZeaburRegistry || !web.ImageCached || web.Kind != "image" || web.Role != "app" {
		t.Fatalf("web: %+v", web)
	}
	if len(web.Ports) != 1 || web.Ports[0].Protocol != "http" || web.Ports[0].ContainerPort != 8080 || web.Ports[0].Name != "web" {
		t.Fatalf("web ports %+v", web.Ports)
	}
	if len(web.Domains) != 1 || web.Domains[0] != "myshop.zeabur.app" {
		t.Fatalf("web domains %+v", web.Domains)
	}
	if len(web.Volumes) != 1 || web.Volumes[0].MountPath != "/app/uploads" || web.Volumes[0].Bytes != 1048576 ||
		web.Volumes[0].HostDir != StorageRoot+"/pvc-1111_"+nsName+"_uploads-service-"+webID {
		t.Fatalf("web volumes %+v", web.Volumes)
	}
	if len(web.Configs) != 1 || web.Configs[0].Path != "/app/config.json" || !strings.Contains(web.Configs[0].Content, "redis.zeabur.internal") {
		t.Fatalf("web configs %+v", web.Configs)
	}
	if _, ok := envVal(web.Env, "POD_IP"); ok {
		t.Fatal("valueFrom env must be skipped")
	}
	if !hasWarning(web.Warnings, "valueFrom") || !hasWarning(web.Warnings, "zeabur.app") {
		t.Fatalf("web warnings %v", web.Warnings)
	}
	for _, e := range web.Env {
		if e.Key == "DATABASE_URL" && !e.Secret {
			t.Fatal("DATABASE_URL must be secret")
		}
	}

	pg := find(t, p.Services, pgID)
	if pg.Kind != "postgres" || pg.Role != "database" || pg.Name != "postgresql" || pg.ImageID == "" {
		t.Fatalf("pg: %+v", pg)
	}
	if len(pg.NodePorts) != 1 || pg.NodePorts[0] != 30123 || pg.Ports[0].PublicPort != 30123 || pg.Ports[0].Protocol != "tcp" {
		t.Fatalf("pg ports %+v %+v", pg.Ports, pg.NodePorts)
	}
	if pg.Volumes[0].Bytes != 52428800 {
		t.Fatalf("pg volume %+v", pg.Volumes)
	}

	rd := find(t, p.Services, rdID)
	if rd.Kind != "redis" || rd.Role != "cache" || rd.Workload != "statefulset/service-"+rdID || len(rd.Args) != 2 {
		t.Fatalf("redis %+v", rd)
	}

	sus := find(t, p.Services, "66500000000000000000a009")
	if sus.Workload != "" || len(sus.Volumes) != 1 || sus.Volumes[0].Bytes != 4096 || !hasWarning(sus.Warnings, "suspended") {
		t.Fatalf("suspended %+v", sus)
	}
}

func hasWarning(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestBuildPlan(t *testing.T) {
	p := load(t)[0]
	plan := BuildPlan(p.Services, PlanOptions{
		SourceProject: p.ProjectID, ProjectName: "shop", TargetServer: "srv_aaaaaaaaaaaa",
		ServerIPv4: "198.51.100.10", BaseDomain: "example.com",
		DomainMap: map[string]string{},
		Exposed:   map[string][]string{pgID: {"POSTGRES_CONNECTION_STRING"}},
		Received:  map[string]map[string]string{webID: {"REDIS_PASSWORD": "r3dispw", "PORT": "9999"}},
	})
	if len(plan.Services) != 3 {
		t.Fatalf("suspended service must be skipped: %d", len(plan.Services))
	}
	if !hasWarning(plan.Warnings, "no running workload") {
		t.Fatal("missing suspended warning")
	}
	var web, pg, rd api.ImportService
	for _, s := range plan.Services {
		switch s.SourceServiceID {
		case webID:
			web = s
		case pgID:
			pg = s
		case rdID:
			rd = s
		}
	}
	if !web.ImageNeedsPush || web.Kind != "image" {
		t.Fatalf("web %+v", web)
	}
	want := map[string]string{
		"DATABASE_URL":       "postgresql://root:S3cretPassw0rd@postgresql:5432/zeabur",
		"REDIS_HOST":         "redis",
		"ZEABUR_WEB_URL":     "${ATOLL_WEB_URL}",
		"ZEABUR_WEB_DOMAIN":  "${ATOLL_WEB_DOMAIN}",
		"PUBLIC_URL":         "https://myshop.example.com/api",
		"OAUTH_CALLBACK":     "https://myshop.example.com/auth/callback",
		"CONTAINER_HOSTNAME": "web",
		"PORT":               "8080", // pod spec wins over received
		"REDIS_PASSWORD":     "r3dispw",
	}
	for k, v := range want {
		got, ok := envVal(web.Env, k)
		if !ok || got != v {
			t.Errorf("web env %s = %q (present %v), want %q", k, got, ok, v)
		}
	}
	for _, k := range []string{"ZEABUR_SERVICE_ID", "ZEABUR_PROJECT_ID"} {
		if _, ok := envVal(web.Env, k); ok {
			t.Errorf("%s must be dropped", k)
		}
	}
	if len(web.Domains) != 1 || web.Domains[0] != "myshop.example.com" {
		t.Fatalf("web domains %v", web.Domains)
	}
	if web.Configs[0].Content != `{"api":"https://myshop.example.com","cache":"redis:6379"}` {
		t.Fatalf("config rewrite: %s", web.Configs[0].Content)
	}
	if len(web.Volumes) != 1 || web.Volumes[0].Name != "uploads" || web.DataBytes != 1048576 {
		t.Fatalf("web volumes %+v", web.Volumes)
	}

	if pg.Kind != "postgres" || pg.Image != "docker.io/library/postgres@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" && pg.Image != "postgres@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("pg image %q kind %q", pg.Image, pg.Kind)
	}
	if v, _ := envVal(pg.Env, "POSTGRES_CONNECTION_STRING"); v != "postgresql://root:S3cretPassw0rd@postgresql:5432/zeabur" {
		t.Fatalf("pg conn %q", v)
	}
	for _, e := range pg.Env {
		if e.Key == "POSTGRES_CONNECTION_STRING" && (!e.Exposed || !e.Secret) {
			t.Fatalf("conn string flags %+v", e)
		}
	}
	if v, _ := envVal(pg.Env, "PORT_FORWARDED_HOSTNAME"); v != "198.51.100.10" {
		t.Fatalf("PORT_FORWARDED_HOSTNAME %q", v)
	}
	if pg.Ports[0].PublicPort != 30123 {
		t.Fatalf("pg public port %+v", pg.Ports)
	}
	if rd.Image != "redis:7.2" || len(rd.Args) != 2 {
		t.Fatalf("redis %+v / warnings %v", rd, plan.Warnings)
	}
	for _, w := range plan.Warnings {
		if strings.Contains(w, "S3cretPassw0rd") || strings.Contains(w, "r3dispw") {
			t.Fatalf("warning leaks a secret: %s", w)
		}
	}
}

func TestDomainMapOverridesAndDrop(t *testing.T) {
	p := load(t)[0]
	plan := BuildPlan(p.Services, PlanOptions{DomainMap: map[string]string{"myshop.zeabur.app": "shop.my.dev"}})
	for _, s := range plan.Services {
		if s.SourceServiceID == webID {
			if v, _ := envVal(s.Env, "PUBLIC_URL"); v != "https://shop.my.dev/api" {
				t.Fatalf("override: %q", v)
			}
		}
	}
	plan = BuildPlan(p.Services, PlanOptions{}) // no base domain: dropped with warning
	for _, s := range plan.Services {
		if s.SourceServiceID == webID && len(s.Domains) != 0 {
			t.Fatalf("want no domains, got %v", s.Domains)
		}
	}
	if !hasWarning(plan.Warnings, "no replacement") {
		t.Fatal("missing no-replacement warning")
	}
}

func TestReplaceHost(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://app.zeabur.app/cb", "https://app.example.com/cb"},
		{"app.zeabur.app", "app.example.com"},
		{"APP.ZEABUR.APP:443", "app.example.com:443"},
		{"https://x-app.zeabur.app", "https://x-app.zeabur.app"},
		{"https://app.zeabur.app.evil.com", "https://app.zeabur.app.evil.com"},
		{"a,app.zeabur.app,b", "a,app.example.com,b"},
		{"see app.zeabur.app.", "see app.example.com."},
	}
	for _, c := range cases {
		if got := ReplaceHost(c.in, "app.zeabur.app", "app.example.com"); got != c.want {
			t.Errorf("ReplaceHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDefaultDomainMap(t *testing.T) {
	m := DefaultDomainMap([]string{"myshop.zeabur.app", "app.example.org", "Blog.zeabur.app."}, "example.com", map[string]string{"blog.zeabur.app": ""})
	if m["myshop.zeabur.app"] != "myshop.example.com" {
		t.Fatalf("%v", m)
	}
	if _, ok := m["app.example.org"]; ok {
		t.Fatal("custom domains must not be mapped")
	}
	if v, ok := m["blog.zeabur.app"]; !ok || v != "" {
		t.Fatalf("override to drop: %v", m)
	}
}

func TestExpand(t *testing.T) {
	own := map[string]string{
		"PASSWORD":          "pw",
		"POSTGRES_PASSWORD": "${PASSWORD}",
		"URL":               "postgres://u:${POSTGRES_PASSWORD}@${POSTGRES_HOST}:${MISSING}/db",
		"SELF":              "${SELF}x",
	}
	recv := map[string]string{"POSTGRES_HOST": "postgresql.zeabur.internal"}
	out, unresolved := Expand(own, recv)
	if out["POSTGRES_PASSWORD"] != "pw" {
		t.Fatalf("%v", out)
	}
	if out["URL"] != "postgres://u:pw@postgresql.zeabur.internal:${MISSING}/db" {
		t.Fatalf("url %q", out["URL"])
	}
	if strings.Join(unresolved, ",") != "MISSING,SELF" {
		t.Fatalf("unresolved %v", unresolved)
	}
}

func TestRewriteInternal(t *testing.T) {
	rw := Rewriter{ByDNSName: map[string]string{"postgresql": "db"}, ByServiceID: map[string]string{pgID: "db"}}
	cases := map[string]string{
		"postgresql.zeabur.internal:5432":                                 "db:5432",
		"postgresql.environment-66500000000000000000e001.zeabur.internal": "db",
		"redis.zeabur.internal":                                           "redis",
		"service-" + pgID:                                                 "db",
		"service-66500000000000000000ffff":                                "service-66500000000000000000ffff",
	}
	for in, want := range cases {
		if got := rw.Rewrite(in); got != want {
			t.Errorf("Rewrite(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMaskAndSecrets(t *testing.T) {
	if m := Mask("DB_PASSWORD", "hunter2"); strings.Contains(m, "hunter2") || !strings.Contains(m, Hash("hunter2")) {
		t.Fatalf("mask %q", m)
	}
	if m := Mask("APP_URL", "mysql://u:p@h/db"); strings.Contains(m, "u:p") {
		t.Fatalf("credential URL must be masked: %q", m)
	}
	if Mask("NODE_ENV", "production") != "production" {
		t.Fatal("non-secret should show")
	}
	if IsSecret("PORT", "8080") || !IsSecret("JWT_SECRET", "x") || !IsSecret("STRIPE_API_KEY", "x") {
		t.Fatal("IsSecret heuristics")
	}
}

func TestImages(t *testing.T) {
	if !IsZeaburRegistry("registry-oci-iad1.zeabur.cloud/e-x/s-y:d-z") || IsZeaburRegistry("ghcr.io/me/app:v1") {
		t.Fatal("IsZeaburRegistry")
	}
	if got := PinImage("nginx", "docker.io/library/nginx@sha256:"+strings.Repeat("a", 64)); got != "nginx@sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("pin %q", got)
	}
	if got := PinImage("nginx:1.27", "x@sha256:"+strings.Repeat("a", 64)); got != "nginx:1.27" {
		t.Fatalf("non-floating must stay %q", got)
	}
	if repo, tag, _ := SplitImage("localhost:5000/app:v2"); repo != "localhost:5000/app" || tag != "v2" {
		t.Fatalf("split %q %q", repo, tag)
	}
	for img, kind := range map[string]string{"postgres:16": "postgres", "bitnami/mariadb:11": "mysql", "mongo:7": "mongo", "valkey/valkey:8": "redis", "ghcr.io/me/app": "image"} {
		if k, _ := DetectKind(img); k != kind {
			t.Errorf("DetectKind(%s)=%s want %s", img, k, kind)
		}
	}
}
