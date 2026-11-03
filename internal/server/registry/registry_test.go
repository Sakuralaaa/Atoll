package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRegistry is a minimal OCI distribution server with a token endpoint.
type fakeRegistry struct {
	mu        sync.Mutex
	digests   map[string]string // "repo:tag" -> digest
	tags      map[string][]string
	user      string // when set, the token endpoint requires basic auth
	pass      string
	basicOnly bool // answer with a Basic challenge instead of Bearer
	rateLimit int  // answer the next N registry requests with 429
	heads     int
	gets      int
	tokens    int
	srv       *httptest.Server
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	f := &fakeRegistry{digests: map[string]string{}, tags: map[string][]string{}}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRegistry) host() string { return strings.TrimPrefix(f.srv.URL, "http://") }

func (f *fakeRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/token" {
		f.tokens++
		if f.user != "" {
			u, p, ok := r.BasicAuth()
			if !ok || u != f.user || p != f.pass {
				w.WriteHeader(401)
				return
			}
		}
		if r.URL.Query().Get("service") != "fake" || !strings.HasPrefix(r.URL.Query().Get("scope"), "repository:") {
			w.WriteHeader(400)
			return
		}
		fmt.Fprintf(w, `{"token":"tok-%s","expires_in":300}`, r.URL.Query().Get("scope"))
		return
	}
	if f.rateLimit > 0 {
		f.rateLimit--
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(429)
		return
	}
	if f.basicOnly {
		if u, p, ok := r.BasicAuth(); !ok || u != f.user || p != f.pass {
			w.Header().Set("WWW-Authenticate", `Basic realm="fake"`)
			w.WriteHeader(401)
			return
		}
	} else if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer tok-repository:") {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="fake",scope="repository:%s:pull"`, f.srv.URL, repoOf(r.URL.Path)))
		w.WriteHeader(401)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v2/")
	switch {
	case strings.Contains(path, "/manifests/"):
		if !strings.Contains(r.Header.Get("Accept"), "application/vnd.oci.image.index.v1+json") {
			w.WriteHeader(406)
			return
		}
		repo, tag, _ := strings.Cut(path, "/manifests/")
		dg, ok := f.digests[repo+":"+tag]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if r.Method == http.MethodHead {
			f.heads++
		} else {
			f.gets++
		}
		w.Header().Set("Docker-Content-Digest", dg)
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
	case strings.HasSuffix(path, "/tags/list"):
		repo := strings.TrimSuffix(path, "/tags/list")
		all := f.tags[repo]
		// paginate by 2 to exercise Link
		start := 0
		if last := r.URL.Query().Get("last"); last != "" {
			for i, t := range all {
				if t == last {
					start = i + 1
				}
			}
		}
		end := min(start+2, len(all))
		if end < len(all) {
			w.Header().Set("Link", fmt.Sprintf(`</v2/%s/tags/list?n=2&last=%s>; rel="next"`, repo, all[end-1]))
		}
		q := []string{}
		for _, t := range all[start:end] {
			q = append(q, `"`+t+`"`)
		}
		fmt.Fprintf(w, `{"name":%q,"tags":[%s]}`, repo, strings.Join(q, ","))
	default:
		w.WriteHeader(404)
	}
}

func repoOf(p string) string {
	p = strings.TrimPrefix(p, "/v2/")
	if i := strings.Index(p, "/manifests/"); i >= 0 {
		return p[:i]
	}
	return strings.TrimSuffix(p, "/tags/list")
}

func testClient(f *fakeRegistry) *Client {
	c := New()
	c.PlainHTTP = func(string) bool { return true }
	return c
}

func TestParseRef(t *testing.T) {
	cases := map[string]Ref{
		"nginx":                              {Registry: DockerHub, Repository: "library/nginx", Tag: "latest", Name: "nginx"},
		"nginx:1.27":                         {Registry: DockerHub, Repository: "library/nginx", Tag: "1.27", Name: "nginx"},
		"bitnami/redis:7.2":                  {Registry: DockerHub, Repository: "bitnami/redis", Tag: "7.2", Name: "bitnami/redis"},
		"ghcr.io/acme/acme-api:v1.2.0":       {Registry: "ghcr.io", Repository: "acme/acme-api", Tag: "v1.2.0", Name: "ghcr.io/acme/acme-api"},
		"localhost:5000/app":                 {Registry: "localhost:5000", Repository: "app", Tag: "latest", Name: "localhost:5000/app"},
		"reg.example.com:5000/a/b@sha256:ab": {Registry: "reg.example.com:5000", Repository: "a/b", Digest: "sha256:ab", Name: "reg.example.com:5000/a/b"},
		"nginx:1.27@sha256:cd":               {Registry: DockerHub, Repository: "library/nginx", Tag: "1.27", Digest: "sha256:cd", Name: "nginx"},
		"docker.io/library/nginx:1":          {Registry: DockerHub, Repository: "library/nginx", Tag: "1", Name: "docker.io/library/nginx"},
	}
	for in, want := range cases {
		got, err := ParseRef(in)
		if err != nil || got != want {
			t.Errorf("ParseRef(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "Nginx:1", "a b", "http://x/y"} {
		if _, err := ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q) should fail", bad)
		}
	}
	r, _ := ParseRef("nginx:1.27")
	if r.Pinned("sha256:x") != "nginx@sha256:x" || r.ChangelogURL() != "https://hub.docker.com/_/nginx" {
		t.Fatal(r.Pinned("sha256:x"), r.ChangelogURL())
	}
	r, _ = ParseRef("ghcr.io/acme/acme-api:v1")
	if r.ChangelogURL() != "https://github.com/acme/acme-api/releases" {
		t.Fatal(r.ChangelogURL())
	}
	r, _ = ParseRef("quay.io/x/y:1")
	if r.ChangelogURL() != "" {
		t.Fatal(r.ChangelogURL())
	}
}

func TestDigestWithAnonymousToken(t *testing.T) {
	f := newFakeRegistry(t)
	f.digests["acme/acme-api:1.2"] = "sha256:aaa"
	c := testClient(f)
	ref, _ := ParseRef(f.host() + "/acme/acme-api:1.2")
	dg, err := c.Digest(context.Background(), ref, Auth{})
	if err != nil || dg != "sha256:aaa" {
		t.Fatalf("digest %q %v", dg, err)
	}
	// cached: no new request
	if dg, err = c.Digest(context.Background(), ref, Auth{}); err != nil || dg != "sha256:aaa" || f.heads != 1 || f.tokens != 1 {
		t.Fatalf("cache: %q %v heads=%d tokens=%d", dg, err, f.heads, f.tokens)
	}
	if f.gets != 0 {
		t.Fatal("digest must use HEAD only")
	}
	// token reused for another tag of the same repo
	f.digests["acme/acme-api:1.3"] = "sha256:bbb"
	ref2, _ := ParseRef(f.host() + "/acme/acme-api:1.3")
	if dg, err := c.Digest(context.Background(), ref2, Auth{}); err != nil || dg != "sha256:bbb" || f.tokens != 1 {
		t.Fatalf("second tag %q %v tokens=%d", dg, err, f.tokens)
	}
	missing, _ := ParseRef(f.host() + "/acme/acme-api:nope")
	if _, err := c.Digest(context.Background(), missing, Auth{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing tag: %v", err)
	}
}

func TestDigestWithCredentials(t *testing.T) {
	f := newFakeRegistry(t)
	f.user, f.pass = "bot", "s3cret"
	f.digests["private/app:v2"] = "sha256:ccc"
	c := testClient(f)
	ref, _ := ParseRef(f.host() + "/private/app:v2")
	if _, err := c.Digest(context.Background(), ref, Auth{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("anonymous should be denied: %v", err)
	}
	dg, err := c.Digest(context.Background(), ref, Auth{Username: "bot", Password: "s3cret"})
	if err != nil || dg != "sha256:ccc" {
		t.Fatalf("with creds %q %v", dg, err)
	}
	// basic-auth-only registry
	f2 := newFakeRegistry(t)
	f2.basicOnly, f2.user, f2.pass = true, "bot", "pw"
	f2.digests["x/y:1"] = "sha256:ddd"
	ref, _ = ParseRef(f2.host() + "/x/y:1")
	if dg, err := testClient(f2).Digest(context.Background(), ref, Auth{Username: "bot", Password: "pw"}); err != nil || dg != "sha256:ddd" {
		t.Fatalf("basic %q %v", dg, err)
	}
}

func TestTagsPaginated(t *testing.T) {
	f := newFakeRegistry(t)
	f.tags["acme/acme-api"] = []string{"1.0.0", "1.1.0", "1.2.0", "latest", "2.0.0"}
	c := testClient(f)
	ref, _ := ParseRef(f.host() + "/acme/acme-api:1.0.0")
	tags, err := c.Tags(context.Background(), ref, Auth{})
	if err != nil || strings.Join(tags, ",") != "1.0.0,1.1.0,1.2.0,latest,2.0.0" {
		t.Fatalf("tags %v %v", tags, err)
	}
}

func TestRateLimitBackoff(t *testing.T) {
	f := newFakeRegistry(t)
	f.digests["acme/mail-relay:3"] = "sha256:eee"
	c := testClient(f)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }
	ref, _ := ParseRef(f.host() + "/acme/mail-relay:3")
	// first attempt gets a token, then 429 on the HEAD
	f.mu.Lock()
	f.rateLimit = 1
	f.mu.Unlock()
	_, err := c.Digest(context.Background(), ref, Auth{})
	var rl *RateLimitError
	if !errors.As(err, &rl) || !errors.Is(err, ErrRateLimited) || !rl.Until.Equal(now.Add(30*time.Second)) {
		t.Fatalf("want rate limit until +30s, got %v", err)
	}
	// during backoff no request reaches the registry
	f.mu.Lock()
	before := f.heads + f.tokens
	f.mu.Unlock()
	if _, err := c.Digest(context.Background(), ref, Auth{}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("still in backoff: %v", err)
	}
	f.mu.Lock()
	after := f.heads + f.tokens
	f.mu.Unlock()
	if after != before {
		t.Fatal("request sent during backoff")
	}
	// after the window it succeeds and the strike is cleared
	now = now.Add(31 * time.Second)
	if dg, err := c.Digest(context.Background(), ref, Auth{}); err != nil || dg != "sha256:eee" {
		t.Fatalf("after backoff %q %v", dg, err)
	}
	if !c.BackoffUntil(ref.apiHost()).IsZero() {
		t.Fatal("backoff not cleared")
	}
	// without Retry-After: exponential 1m, 2m
	if d := c.strike("h", 0).Until.Sub(now); d != time.Minute {
		t.Fatal(d)
	}
	if d := c.strike("h", 0).Until.Sub(now); d != 2*time.Minute {
		t.Fatal(d)
	}
}

func TestSemver(t *testing.T) {
	ok := []string{"1", "1.27", "v1.2.3", "1.27.2-alpine", "16-bookworm"}
	for _, s := range ok {
		if _, good := ParseVersion(s); !good {
			t.Errorf("%q should parse", s)
		}
	}
	for _, s := range []string{"latest", "main", "sha-abc123", "1.2.3.4", "20240101", "2024a", "v"} {
		if _, good := ParseVersion(s); good {
			t.Errorf("%q should not parse", s)
		}
	}
	tags := []string{"1.26", "1.27", "1.27.3", "1.28", "1.29-alpine", "1.30-rc1", "2.0", "2.1", "latest", "1.28-alpine", "v1.99"}
	cases := []struct{ cur, same, major string }{
		{"1.27", "1.28", "2.1"},
		{"1.28", "", "2.1"},
		{"1.27-alpine", "1.29-alpine", ""},
		{"2.1", "", ""},
		{"latest", "", ""},
		{"1", "", "2"},
	}
	tags = append(tags, "2", "1")
	for _, c := range cases {
		s, m := NewerTags(c.cur, tags)
		if s != c.same || m != c.major {
			t.Errorf("NewerTags(%q) = %q,%q want %q,%q", c.cur, s, m, c.same, c.major)
		}
	}
	a, _ := ParseVersion("1.10.0")
	b, _ := ParseVersion("1.9.9")
	if Compare(a, b) != 1 || Compare(b, a) != -1 || Compare(a, a) != 0 {
		t.Fatal("numeric compare")
	}
	if _, m := NewerTags("v1.2.0", []string{"v1.3.0", "1.4.0", "v2.0.0"}); m != "v2.0.0" {
		t.Fatal(m)
	}
}

func TestParseChallenge(t *testing.T) {
	s, p := parseChallenge(`Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/nginx:pull"`)
	if s != "Bearer" || p["realm"] != "https://auth.docker.io/token" || p["service"] != "registry.docker.io" || p["scope"] != "repository:library/nginx:pull" {
		t.Fatal(s, p)
	}
}
