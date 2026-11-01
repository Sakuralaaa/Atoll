package mesh

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"
)

// The forwarder image is built locally from the agent's own (static) binary: no registry pull,
// so forwarders can be (re)created while the control server and the internet are unreachable.
// It is a docker-save archive loaded with the Runtime's LoadImage.

const imageRepo = "atoll/forwarder"

// BinaryPath is where the agent binary sits inside the forwarder image.
const BinaryPath = "/atoll-agent"

// Entrypoint of forwarder containers.
var Entrypoint = []string{BinaryPath, "forward"}

// FileHash returns the hex sha256 of a file.
func FileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ImageRef is the local forwarder image tag for a binary hash.
func ImageRef(binHash string) string {
	if len(binHash) > 12 {
		binHash = binHash[:12]
	}
	return imageRepo + ":" + binHash
}

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// writeLayer writes the single image layer (the binary plus /etc and /tmp) to w.
func writeLayer(w io.Writer, bin io.Reader, size int64) error {
	tw := tar.NewWriter(w)
	for _, d := range []struct {
		name string
		mode int64
	}{{"etc/", 0o755}, {"tmp/", 0o1777}} {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: d.name, Mode: d.mode, ModTime: epoch}); err != nil {
			return err
		}
	}
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: BinaryPath[1:], Mode: 0o755, Size: size, ModTime: epoch}); err != nil {
		return err
	}
	if _, err := io.CopyN(tw, bin, size); err != nil {
		return err
	}
	return tw.Close()
}

// BuildImageArchive writes a `docker load` archive tagged ref whose only file is the binary at
// binPath. tmpDir holds the layer while its digest is computed.
func BuildImageArchive(w io.Writer, binPath, ref, tmpDir string) error {
	bin, err := os.Open(binPath)
	if err != nil {
		return err
	}
	defer bin.Close()
	st, err := bin.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return err
	}
	layer, err := os.CreateTemp(tmpDir, "forwarder-layer-*.tar")
	if err != nil {
		return err
	}
	defer os.Remove(layer.Name())
	defer layer.Close()
	h := sha256.New()
	if err := writeLayer(io.MultiWriter(layer, h), bin, st.Size()); err != nil {
		return err
	}
	diffID := "sha256:" + hex.EncodeToString(h.Sum(nil))
	layerSize, err := layer.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := layer.Seek(0, io.SeekStart); err != nil {
		return err
	}
	cfg, _ := json.Marshal(map[string]any{
		"architecture": runtime.GOARCH,
		"os":           "linux",
		"created":      epoch.Format(time.RFC3339),
		"config": map[string]any{
			"Entrypoint": Entrypoint,
			"Labels":     map[string]string{"atoll.managed": "true", "atoll.role": "mesh"},
		},
		"rootfs":  map[string]any{"type": "layers", "diff_ids": []string{diffID}},
		"history": []map[string]any{{"created": epoch.Format(time.RFC3339), "created_by": "atoll-agent (forwarder image)"}},
	})
	cfgSum := sha256.Sum256(cfg)
	cfgName := hex.EncodeToString(cfgSum[:]) + ".json"
	layerName := diffID[len("sha256:"):] + "/layer.tar"
	manifest, _ := json.Marshal([]map[string]any{{"Config": cfgName, "RepoTags": []string{ref}, "Layers": []string{layerName}}})

	tw := tar.NewWriter(w)
	add := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o644, Size: int64(len(data)), ModTime: epoch}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	if err := add(cfgName, cfg); err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: layerName, Mode: 0o644, Size: layerSize, ModTime: epoch}); err != nil {
		return err
	}
	if _, err := io.CopyN(tw, layer, layerSize); err != nil {
		return fmt.Errorf("layer: %w", err)
	}
	if err := add("manifest.json", manifest); err != nil {
		return err
	}
	return tw.Close()
}
