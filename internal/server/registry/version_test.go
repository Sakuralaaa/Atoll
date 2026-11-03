package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionFromIndexConfigLabel(t *testing.T) {
	var gets int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets++
		switch r.URL.Path {
		case "/v2/o/app/manifests/sha256:idx":
			w.Write([]byte(`{"manifests":[{"digest":"sha256:arm","platform":{"os":"linux","architecture":"arm64"}},{"digest":"sha256:amd","platform":{"os":"linux","architecture":"amd64"}}]}`))
		case "/v2/o/app/manifests/sha256:amd":
			w.Write([]byte(`{"config":{"digest":"sha256:cfg"}}`))
		case "/v2/o/app/blobs/sha256:cfg":
			w.Write([]byte(`{"config":{"Labels":{"org.opencontainers.image.version":"1.2.5"}}}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	c := New()
	c.PlainHTTP = func(string) bool { return true }
	ref, err := ParseRef(host + "/o/app:latest")
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Version(context.Background(), ref, "sha256:idx", Auth{})
	if err != nil || v != "1.2.5" {
		t.Fatalf("version = %q, %v", v, err)
	}
	n := gets
	if v, _ := c.Version(context.Background(), ref, "sha256:idx", Auth{}); v != "1.2.5" || gets != n {
		t.Fatalf("second lookup should be cached: %q gets %d->%d", v, n, gets)
	}
	if v, ok := CachedVersion("sha256:idx"); !ok || v != "1.2.5" {
		t.Fatalf("cached = %q %v", v, ok)
	}
}
