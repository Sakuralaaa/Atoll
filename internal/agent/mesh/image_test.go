package mesh

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildImageArchive(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "atoll-agent")
	os.WriteFile(bin, []byte("#!fake-binary-content"), 0o755)
	sum, err := FileHash(bin)
	if err != nil {
		t.Fatal(err)
	}
	ref := ImageRef(sum)
	if len(ref) != len("atoll/forwarder:")+12 {
		t.Fatal(ref)
	}
	var buf bytes.Buffer
	if err := BuildImageArchive(&buf, bin, ref, dir); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		files[h.Name] = b
	}
	var manifest []struct {
		Config   string
		RepoTags []string
		Layers   []string
	}
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil || len(manifest) != 1 {
		t.Fatalf("manifest %s %v", files["manifest.json"], err)
	}
	m := manifest[0]
	if len(m.RepoTags) != 1 || m.RepoTags[0] != ref || len(m.Layers) != 1 {
		t.Fatalf("manifest %+v", m)
	}
	var cfg struct {
		OS     string `json:"os"`
		Config struct{ Entrypoint []string }
		RootFS struct {
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	if err := json.Unmarshal(files[m.Config], &cfg); err != nil {
		t.Fatal(err)
	}
	cs := sha256.Sum256(files[m.Config])
	if m.Config != hex.EncodeToString(cs[:])+".json" {
		t.Fatal("config name must be its digest")
	}
	layer := files[m.Layers[0]]
	ls := sha256.Sum256(layer)
	if cfg.OS != "linux" || len(cfg.RootFS.DiffIDs) != 1 || cfg.RootFS.DiffIDs[0] != "sha256:"+hex.EncodeToString(ls[:]) {
		t.Fatalf("config %+v", cfg)
	}
	if len(cfg.Config.Entrypoint) != 2 || cfg.Config.Entrypoint[0] != "/atoll-agent" || cfg.Config.Entrypoint[1] != "forward" {
		t.Fatalf("entrypoint %v", cfg.Config.Entrypoint)
	}
	lt := tar.NewReader(bytes.NewReader(layer))
	found := false
	for {
		h, err := lt.Next()
		if err != nil {
			break
		}
		if h.Name == "atoll-agent" {
			b, _ := io.ReadAll(lt)
			found = string(b) == "#!fake-binary-content" && h.Mode == 0o755
		}
	}
	if !found {
		t.Fatal("binary missing from layer")
	}
}
