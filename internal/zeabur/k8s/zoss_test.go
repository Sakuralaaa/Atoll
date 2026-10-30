// Fixture tests adapted from zeabur-oss/zeabur (Apache-2.0):
// crates/zeabur-graph/fixtures/{graph,projection}/* (copied under testdata/zoss) and the unit
// tests of crates/zeabur-model/src/adapters/images.rs, crates/zeabur-gaps/src/durability.rs and
// crates/zeabur-graph/src/connections.rs. See THIRD_PARTY_NOTICES.md.

package k8s

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

type zossCase struct {
	Description string            `yaml:"description"`
	Operation   string            `yaml:"operation"`
	Params      map[string]string `yaml:"params"`
}

func TestZossFixtures(t *testing.T) {
	dirs, err := filepath.Glob("testdata/zoss/*/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, dir := range dirs {
		dir := dir
		t.Run(filepath.Base(dir), func(t *testing.T) {
			var c zossCase
			readYAML(t, filepath.Join(dir, "case.yaml"), &c)
			raw, err := os.ReadFile(filepath.Join(dir, "input.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			objs, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := os.ReadFile(filepath.Join(dir, "expected.json"))
			if err != nil {
				t.Fatal(err)
			}
			switch c.Operation {
			case "graph.attribution":
				var want NamespaceAttribution
				mustJSON(t, expected, &want)
				got := Attribute(objs)
				normalize(&want)
				normalize(&got)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s\n got %+v\nwant %+v", c.Description, got, want)
				}
			case "graph.isMachinerySecret":
				var want bool
				mustJSON(t, expected, &want)
				if got := IsMachinerySecret(objs[0]); got != want {
					t.Fatalf("%s: got %v", c.Description, got)
				}
			case "graph.deploymentPhase":
				var want string
				mustJSON(t, expected, &want)
				if got := DeploymentPhase(objs[0]); string(got) != want {
					t.Fatalf("%s: got %s want %s", c.Description, got, want)
				}
			case "projection.connections":
				var want []Connection
				mustJSON(t, expected, &want)
				got := Connections(c.Params["namespace"], objs)
				if !reflect.DeepEqual(got, want) {
					g, _ := json.Marshal(got)
					t.Fatalf("%s\n got %s\nwant %s", c.Description, g, expected)
				}
			case "projection.mounts":
				// Adapted: atoll keeps no Mount view; check the claim-template mount is
				// persistent and, once provisioned, attributed to the set by ordinal name.
				w := Workloads(objs)[0]
				if mountCategory(w, "data") != "persistent" {
					t.Fatal("claim template mount must be persistent")
				}
				provisioned := false
				for _, r := range Relations(objs) {
					provisioned = provisioned || (r.Kind == RelClaimTemplate && r.Object.Name == "data-postgres-0")
				}
				if want := strings.Contains(filepath.Base(dir), "-provisioned"); provisioned != want {
					t.Fatalf("claim-template relation = %v, want %v", provisioned, want)
				}
			default:
				t.Fatalf("unported operation %s", c.Operation)
			}
		})
	}
}

func readYAML(t *testing.T, p string, v any) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

func normalize(a *NamespaceAttribution) {
	for _, m := range []*map[string]Attribution{&a.ConfigMaps, &a.Secrets, &a.PersistentVolumeClaims, &a.Services, &a.Ingresses} {
		if *m == nil {
			*m = map[string]Attribution{}
		}
		for k, v := range *m {
			if v.Owners == nil {
				v.Owners = []WorkloadRef{}
			}
			(*m)[k] = v
		}
	}
	if a.Workloads == nil {
		a.Workloads = []WorkloadRef{}
	}
}

func TestClaimTemplateOrdinal(t *testing.T) {
	cases := []struct {
		claim, tmpl, set string
		want             bool
	}{
		{"data-db-0", "data", "db", true},
		{"data-db-12", "data", "db", true},
		{"data-db-replica-0", "data", "db", false},
		{"data-db-", "data", "db", false},
		{"data-db-0a", "data", "db", false},
	}
	for _, c := range cases {
		if got := CreatedByClaimTemplate(c.claim, c.tmpl, c.set); got != c.want {
			t.Errorf("%s: got %v", c.claim, got)
		}
	}
}

func TestDataStoreImages(t *testing.T) {
	for img, want := range map[string]string{
		"ghcr.io/acme/shop-api:2.1": "shop-api", "postgres": "postgres", "redis@sha256:abc": "redis", "localhost:5000/redis:7": "redis",
	} {
		if got := RepositoryName(img); got != want {
			t.Errorf("RepositoryName(%s)=%s", img, got)
		}
	}
	for img, want := range map[string]DataStore{
		"postgres:16": StorePostgres, "docker.io/bitnami/postgresql:16.2.0": StorePostgres, "valkey/valkey:8": StoreRedis,
		"clickhouse/clickhouse-server:25.8": StoreSearch, "couchdb:3": StoreOther, "ghcr.io/acme/postgres-exporter:1": "", "nginx:1.27": "",
	} {
		if got := DataStoreImage(img); got != want {
			t.Errorf("DataStoreImage(%s)=%q want %q", img, got, want)
		}
	}
	// atoll kinds: exporter/admin tools are apps; memcached/cockroach have no dump.
	for img, want := range map[string]string{
		"ghcr.io/acme/postgres-exporter:1": "image", "mongo-express:1": "image", "memcached:1.6": "image",
		"cockroachdb/cockroach:v24": "image", "postgis/postgis:16-3.4": "postgres", "bitnami/postgresql:16": "postgres", "mariadb:11": "mysql",
	} {
		if got, _ := DetectKind(img); got != want {
			t.Errorf("DetectKind(%s)=%s want %s", img, got, want)
		}
	}
}

func TestDataDirectory(t *testing.T) {
	cases := map[string][2]string{
		"postgres:16":                 {"postgres", "/var/lib/postgresql/data"},
		"postgres:18-alpine":          {"postgres", "/var/lib/postgresql"},
		"postgres":                    {"postgres", "/var/lib/postgresql"},
		"docker.io/library/mysql:8.4": {"mysql", "/var/lib/mysql"},
		"valkey/valkey:8":             {"valkey", "/data"},
		"bitnami/postgresql:16":       {"", ""},
		"ghcr.io/acme/redis:1":        {"", ""},
		"registry.local:5000/redis":   {"", ""},
	}
	for img, want := range cases {
		if s, d := DataDirectory(img); s != want[0] || d != want[1] {
			t.Errorf("DataDirectory(%s)=%s,%s", img, s, d)
		}
	}
	if !pathsOverlap("/var/lib/postgresql/data", "/var/lib/postgresql") || !pathsOverlap("/var/lib/postgresql", "/var/lib/postgresql/data") ||
		!pathsOverlap("/data/", "/data") || pathsOverlap("/data-old", "/data") || pathsOverlap("/var/lib/mysql-files", "/var/lib/mysql") {
		t.Fatal("pathsOverlap")
	}
}

func TestDurabilityWarnings(t *testing.T) {
	mk := func(volumes, mounts string) Object {
		objs, err := Parse([]byte(`apiVersion: apps/v1
kind: Deployment
metadata: {name: db}
spec:
  template:
    spec:
      volumes: ` + volumes + `
      containers:
      - name: postgres
        image: postgres:17
        volumeMounts: ` + mounts + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		return objs[0]
	}
	if w := DurabilityWarnings(mk("[]", "[]")); len(w) != 1 || !strings.Contains(w[0], "/var/lib/postgresql/data") || !strings.Contains(w[0], "dump") {
		t.Fatalf("none: %v", w)
	}
	if w := DurabilityWarnings(mk(`[{name: data, emptyDir: {}}]`, `[{name: data, mountPath: /var/lib/postgresql/data}]`)); len(w) != 1 || !strings.Contains(w[0], "emptyDir") {
		t.Fatalf("emptyDir: %v", w)
	}
	if w := DurabilityWarnings(mk(`[{name: data, persistentVolumeClaim: {claimName: db-data}}]`, `[{name: data, mountPath: /var/lib/postgresql/data}]`)); len(w) != 0 {
		t.Fatalf("kept: %v", w)
	}
}

func TestMentionedServices(t *testing.T) {
	known := map[string]bool{"redis": true, "api": true, "db": true}
	cases := map[string][]string{
		"redis://redis:6379/0":                   {"redis"},
		"http://api.shop.svc.cluster.local:8080": {"api"},
		"db":                                     {"db"},
		"postgres://u@db.shop:5432/x":            {"db"},
		"http://api.other.svc.cluster.local":     {},
		"https://api.example.com":                {},
		"redis-cache":                            {},
		"http://api:8080,postgres://db.shop.svc:5432/x,other.example.com,cache": {"api", "db"},
	}
	for v, want := range cases {
		if got := MentionedServices(v, "shop", known); !reflect.DeepEqual(got, want) {
			t.Errorf("MentionedServices(%q)=%v want %v", v, got, want)
		}
	}
}

func TestWorkloadPhaseAndPods(t *testing.T) {
	objs, err := Parse([]byte(`apiVersion: v1
kind: Pod
metadata: {name: p}
status:
  phase: Running
  containerStatuses:
  - name: c
    ready: false
    state: {waiting: {reason: ImagePullBackOff}}
`))
	if err != nil {
		t.Fatal(err)
	}
	if !PodFailing(objs[0]) {
		t.Fatal("ImagePullBackOff while not ready is failing")
	}
	zero := 0
	if p := WorkloadPhase(Object{Kind: "StatefulSet", Spec: Spec{Replicas: &zero}}); p != PhasePaused {
		t.Fatalf("scaled to zero: %s", p)
	}
	if ScanStatus(nil, nil) != StatusSuspended {
		t.Fatal("no workload is suspended")
	}
}

const (
	cloudNS = "environment-67000000000000000000e001"
	appID   = "67000000000000000000a001"
	pgID2   = "67000000000000000000a002"
	cacheID = "67000000000000000000a003"
)

// TestZeaburCloudExtract proves end-to-end Extract on a Zeabur-Cloud-shaped namespace.
func TestZeaburCloudExtract(t *testing.T) {
	raw, err := os.ReadFile("testdata/zeabur-cloud.yaml")
	if err != nil {
		t.Fatal(err)
	}
	objs, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if o.Kind == "Secret" && o.Data != nil {
			t.Fatal("Secret data must be dropped at parse time")
		}
	}
	du := ParseDu("5242880\t" + StorageRoot + "/pvc-aaaa_" + cloudNS + "_media-service-" + appID + "\n" +
		"104857600\t" + StorageRoot + "/pvc-bbbb_" + cloudNS + "_data-service-" + pgID2 + "-0\n")
	projects := ScanNamespaces(objs, HostFacts{DirBytes: du})
	if len(projects) != 1 {
		t.Fatalf("projects %d", len(projects))
	}
	p := projects[0]
	if p.ProjectName != "blog" || len(p.Services) != 4 {
		t.Fatalf("project %+v", p)
	}
	if !hasWarning(p.Warnings, "cronjob/backup is not a Zeabur service") || hasWarning(p.Warnings, "backup-29001") {
		t.Fatalf("cronjob run must be hidden, cronjob reported: %v", p.Warnings)
	}
	if !hasWarning(p.Warnings, "PVC old-uploads") {
		t.Fatalf("orphan PVC not reported: %v", p.Warnings)
	}
	for _, w := range p.Warnings {
		if strings.Contains(w, "c3VwZXIt") || strings.Contains(w, "super-secret") {
			t.Fatal("secret value leaked")
		}
	}

	app := find(t, p.Services, appID)
	if app.Name != "app" || app.Status != StatusRunning || app.Kind != "image" || app.Role != "app" || app.DataStore != "" {
		t.Fatalf("app %+v", app)
	}
	if !reflect.DeepEqual(app.Domains, []string{"blog.example.com", "myblog.zeabur.app"}) {
		t.Fatalf("domains %v", app.Domains)
	}
	if len(app.Ports) != 1 || app.Ports[0].Protocol != "http" || app.Ports[0].ContainerPort != 3000 {
		t.Fatalf("ports %+v", app.Ports)
	}
	if len(app.Volumes) != 1 || app.Volumes[0].MountPath != "/app/media" || app.Volumes[0].Bytes != 5242880 {
		t.Fatalf("volumes %+v", app.Volumes)
	}
	if v, _ := envVal(app.Env, "LOG_LEVEL"); v != "info" {
		t.Fatalf("configMapKeyRef not resolved: %q", v)
	}
	if v, _ := envVal(app.Env, "APP_region"); v != "hkg" {
		t.Fatalf("envFrom ConfigMap with prefix not resolved: %q", v)
	}
	if _, ok := envVal(app.Env, "DB_PASSWORD"); ok || !hasWarning(app.Warnings, "Secret pg-credentials") {
		t.Fatalf("secret env must be a warning, not a value: %v", app.Warnings)
	}
	wantRefs := []api.ZeaburReference{
		{Key: "CACHE_URL", Target: cacheID, Via: ViaZeaburInternal, Object: "cache.zeabur.internal", Inferred: true},
		{Key: "DB_HOST", Target: pgID2, Via: ViaEnvValue, Object: "service-" + pgID2, Inferred: true},
		{Key: "DB_PASSWORD", Target: pgID2, TargetKey: "POSTGRES_PASSWORD", Via: ViaSharedSecret, Object: "Secret/pg-credentials"},
	}
	if !reflect.DeepEqual(app.References, wantRefs) {
		t.Fatalf("references\n got %+v\nwant %+v", app.References, wantRefs)
	}

	pg := find(t, p.Services, pgID2)
	if pg.Name != "postgresql" || pg.Kind != "postgres" || pg.DataStore != "postgres" || pg.Workload != "statefulset/service-"+pgID2 || pg.Status != StatusRunning {
		t.Fatalf("pg %+v", pg)
	}
	if len(pg.Volumes) != 1 || pg.Volumes[0].PVC != "data-service-"+pgID2+"-0" || pg.Volumes[0].Bytes != 104857600 ||
		pg.Volumes[0].HostDir != StorageRoot+"/pvc-bbbb_"+cloudNS+"_data-service-"+pgID2+"-0" {
		t.Fatalf("claim-template volume %+v", pg.Volumes)
	}
	if hasWarning(pg.Warnings, "持久卷") {
		t.Fatalf("pg data is on a PVC: %v", pg.Warnings)
	}

	cache := find(t, p.Services, cacheID)
	if cache.Name != "cache" || cache.Kind != "redis" || cache.Status != StatusFailed || !hasWarning(cache.Warnings, "数据目录 /data 不在持久卷上") {
		t.Fatalf("cache %+v", cache)
	}

	sus := find(t, p.Services, "67000000000000000000a009")
	if sus.Workload != "" || sus.Status != StatusSuspended || len(sus.Volumes) != 1 {
		t.Fatalf("suspended %+v", sus)
	}

	// The plan rewrites cluster DNS and .zeabur.internal hosts and turns the shared Secret
	// key into a reference to the store's variable.
	plan := BuildPlan(p.Services, PlanOptions{ProjectName: "blog", Namespace: p.Namespace, BaseDomain: "example.org"})
	var papp, ppg api.ImportService
	for _, s := range plan.Services {
		switch s.SourceServiceID {
		case appID:
			papp = s
		case pgID2:
			ppg = s
		}
	}
	if v, _ := envVal(papp.Env, "DB_HOST"); v != "postgresql" {
		t.Fatalf("DB_HOST = %q", v)
	}
	if v, _ := envVal(papp.Env, "CACHE_URL"); v != "redis://cache:6379/0" {
		t.Fatalf("CACHE_URL = %q", v)
	}
	if v, _ := envVal(papp.Env, "DB_PASSWORD"); v != "${postgresql.POSTGRES_PASSWORD}" {
		t.Fatalf("DB_PASSWORD = %q", v)
	}
	if ppg.Name != "postgresql" || !hasWarning(plan.Warnings, "POSTGRES_PASSWORD comes from a Kubernetes Secret") {
		t.Fatalf("store key warning missing: %v", plan.Warnings)
	}
}

func TestRewriteClusterDNS(t *testing.T) {
	rw := Rewriter{ByServiceID: map[string]string{pgID: "db"}, ByK8sService: map[string]string{"cache": "redis", "service-" + pgID: "db"}, Namespace: nsName}
	cases := map[string]string{
		"service-" + pgID + "." + nsName + ".svc.cluster.local:5432": "db:5432",
		"postgres://u:p@service-" + pgID + "." + nsName + ".svc/x":   "postgres://u:p@db/x",
		"redis://cache:6379":               "redis://redis:6379",
		"cache":                            "redis",
		"cache.other-ns.svc.cluster.local": "cache.other-ns.svc.cluster.local",
		"use the cache wisely":             "use the cache wisely",
	}
	for in, want := range cases {
		if got := rw.Rewrite(in); got != want {
			t.Errorf("Rewrite(%q) = %q, want %q", in, got, want)
		}
	}
}
