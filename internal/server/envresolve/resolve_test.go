package envresolve

import (
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func v(k, val string, exposed bool) api.EnvVar {
	return api.EnvVar{Key: k, Value: val, Exposed: exposed}
}

func project() []Service {
	return []Service{
		{
			Name:  "postgres",
			Ports: []api.Port{{Name: "database", ContainerPort: 5432, Protocol: "tcp"}},
			Vars: []api.EnvVar{
				v("PASSWORD", "s3cret", false),
				v("POSTGRES_HOST", "postgres", true),
				v("POSTGRES_PORT", "${DATABASE_PORT}", true),
				v("POSTGRES_USERNAME", "root", true),
				{Key: "POSTGRES_PASSWORD", Value: "${PASSWORD}", Exposed: true, Secret: true},
				v("POSTGRES_CONNECTION_STRING", "postgresql://${POSTGRES_USERNAME}:${POSTGRES_PASSWORD}@${POSTGRES_HOST}:${POSTGRES_PORT}/app", true),
			},
		},
		{
			Name:    "web-app",
			Ports:   []api.Port{{Name: "web", ContainerPort: 8080, Protocol: "http"}},
			Domains: map[string]string{"web": "app.example.com"},
			Vars: []api.EnvVar{
				v("DATABASE_URL", "${POSTGRES_CONNECTION_STRING}", false),
				v("PGPASS", "${postgres.POSTGRES_PASSWORD}", false),
				v("PGHOST", "${POSTGRES_HOST}", false),
				v("PG_EXPLICIT_HOST", "${postgres.HOST}:${postgres.PORT}", false),
				v("SELF", "http://${WEB_APP_HOST}:${WEB_PORT}", false),
				v("PUBLIC", "${ATOLL_WEB_URL}/api", false),
				v("LITERAL", "$${NOT_EXPANDED}", false),
				v("HIDDEN", "${postgres.PASSWORD}", false),
				v("MISSING", "a-${NOPE}-b", false),
				v("A", "${B}", false),
				v("B", "${A}", false),
			},
		},
	}
}

func TestResolve(t *testing.T) {
	res := Resolve(Input{Self: "web-app", Services: project()})
	want := map[string]string{
		"DATABASE_URL":     "postgresql://root:s3cret@postgres:5432/app",
		"PGPASS":           "s3cret",
		"PGHOST":           "postgres",
		"PG_EXPLICIT_HOST": "postgres:5432",
		"SELF":             "http://web-app:8080",
		"PUBLIC":           "https://app.example.com/api",
		"LITERAL":          "${NOT_EXPANDED}",
		"HIDDEN":           "${postgres.PASSWORD}",
		"MISSING":          "a-${NOPE}-b",
		"PORT":             "8080",
		"ATOLL_WEB_DOMAIN": "app.example.com",
		"ATOLL_WEB_URL":    "https://app.example.com",
	}
	for k, w := range want {
		if got := res.Env[k]; got != w {
			t.Errorf("%s = %q, want %q", k, got, w)
		}
	}
	joined := strings.Join(res.Warnings, "\n")
	for _, w := range []string{"${NOPE}", "cycle", "does not expose"} {
		if !strings.Contains(joined, w) {
			t.Errorf("missing warning about %s; got:\n%s", w, joined)
		}
	}
	// cycle: values stay finite and contain a literal reference
	if !strings.Contains(res.Env["A"]+res.Env["B"], "${") {
		t.Errorf("cycle values %q %q", res.Env["A"], res.Env["B"])
	}
}

func TestOwnVarShadowsSibling(t *testing.T) {
	svcs := project()
	svcs[1].Vars = append(svcs[1].Vars, v("POSTGRES_HOST", "override", false))
	res := Resolve(Input{Self: "web-app", Services: svcs})
	if res.Env["PGHOST"] != "override" {
		t.Fatalf("PGHOST = %q", res.Env["PGHOST"])
	}
}

func TestAmbiguousExposed(t *testing.T) {
	svcs := []Service{
		{Name: "a", Vars: []api.EnvVar{v("TOKEN", "from-a", true)}},
		{Name: "b", Vars: []api.EnvVar{v("TOKEN", "from-b", true)}},
		{Name: "c", Vars: []api.EnvVar{v("X", "${TOKEN}", false), v("Y", "${b.TOKEN}", false)}},
	}
	res := Resolve(Input{Self: "c", Services: svcs})
	if res.Env["X"] != "from-a" || res.Env["Y"] != "from-b" {
		t.Fatalf("got %v", res.Env)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "several services") {
		t.Fatalf("warnings %v", res.Warnings)
	}
}

func TestExpandAndReferences(t *testing.T) {
	in := Input{Self: "postgres", Services: project()}
	s, w := Expand(in, "--requirepass ${PASSWORD}")
	if s != "--requirepass s3cret" || len(w) != 0 {
		t.Fatalf("%q %v", s, w)
	}
	refs := References(Input{Self: "web-app", Services: project()})
	m := map[string]api.EnvVar{}
	for _, r := range refs {
		if r.Source == "" {
			t.Fatalf("source not set: %+v", r)
		}
		m[r.Source+"."+r.Key] = r
	}
	if r := m["postgres.POSTGRES_HOST"]; r.Value != "postgres" || !r.Exposed || r.Secret {
		t.Fatalf("POSTGRES_HOST ref %+v", r)
	}
	if r, ok := m["postgres.POSTGRES_PASSWORD"]; !ok || r.Value != "" || !r.Secret {
		t.Fatalf("secret exposed var must be listed but masked: %+v", r)
	}
	if _, ok := m["postgres.PASSWORD"]; ok {
		t.Fatal("non-exposed var listed")
	}
	if _, ok := m["web-app.SELF"]; ok {
		t.Fatal("own vars listed")
	}
}
