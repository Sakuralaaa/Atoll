package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// VersionLabel is the OCI annotation/label most images use for their release version.
const VersionLabel = "org.opencontainers.image.version"

// versions caches digest -> version label. A digest's content never changes, so entries
// never expire; "" is cached too so an image without the label costs one lookup.
var (
	versionsMu sync.Mutex
	versions   = map[string]string{}
)

// CachedVersion returns the version label already looked up for digest (no network).
func CachedVersion(digest string) (string, bool) {
	versionsMu.Lock()
	defer versionsMu.Unlock()
	v, ok := versions[digest]
	return v, ok
}

// Version reads the image version label of ref's repository at digest: the manifest (for an
// index, its linux/amd64 entry, else the first), then the config blob's Labels. Results are
// cached forever per digest, so a check calls this at most once per new digest.
func (c *Client) Version(ctx context.Context, ref Ref, digest string, auth Auth) (string, error) {
	if !strings.HasPrefix(digest, "sha256:") {
		return "", nil
	}
	if v, ok := CachedVersion(digest); ok {
		return v, nil
	}
	base := c.base(ref.apiHost()) + "/v2/" + ref.Repository
	var m struct {
		Annotations map[string]string `json:"annotations"`
		Manifests   []struct {
			Digest   string `json:"digest"`
			Platform struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
			} `json:"platform"`
		} `json:"manifests"`
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
	}
	get := func(u string, into any) error {
		resp, err := c.do(ctx, http.MethodGet, u, ref, auth)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(into); err != nil {
			return fmt.Errorf("decode %s: %w", u, err)
		}
		return nil
	}
	if err := get(base+"/manifests/"+digest, &m); err != nil {
		return "", err
	}
	v := m.Annotations[VersionLabel]
	if v == "" && len(m.Manifests) > 0 {
		pick := m.Manifests[0].Digest
		for _, e := range m.Manifests {
			if e.Platform.OS == "linux" && e.Platform.Architecture == "amd64" {
				pick = e.Digest
				break
			}
		}
		m.Annotations, m.Manifests = nil, nil
		if err := get(base+"/manifests/"+pick, &m); err != nil {
			return "", err
		}
		v = m.Annotations[VersionLabel]
	}
	if v == "" && m.Config.Digest != "" {
		var cfg struct {
			Config struct {
				Labels map[string]string `json:"Labels"`
			} `json:"config"`
		}
		if err := get(base+"/blobs/"+m.Config.Digest, &cfg); err != nil {
			return "", err
		}
		v = cfg.Config.Labels[VersionLabel]
	}
	if len(v) > 64 {
		v = v[:64]
	}
	versionsMu.Lock()
	versions[digest] = v
	versionsMu.Unlock()
	return v, nil
}
