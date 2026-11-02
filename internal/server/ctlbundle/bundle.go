package ctlbundle

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// File names inside a bundle (flat, no directories).
const (
	FileManifest  = "manifest.json"
	FileDB        = "atoll.db"
	FileMasterKey = "master.key"
	FileServerEnv = "server.env"
	FileCaddyfile = "Caddyfile"
)

// Known lists every name a bundle may contain; anything else is rejected on extract.
var Known = []string{FileManifest, FileDB, FileMasterKey, FileServerEnv, FileCaddyfile}

// Entry is one file to put in a bundle: from Path on disk, or Data.
type Entry struct {
	Name string
	Path string
	Data []byte
	Mode int64
}

// Write writes an encrypted bundle of entries (plus manifest.json) to w.
func Write(w io.Writer, passphrase string, m api.ControlBundleManifest, entries []Entry) error {
	ew, err := NewEncrypter(w, passphrase)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(ew)
	tw := tar.NewWriter(gz)
	m.Format = 1
	m.Files = nil
	for _, e := range entries {
		m.Files = append(m.Files, e.Name)
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	all := append([]Entry{{Name: FileManifest, Data: mb, Mode: 0o600}}, entries...)
	for _, e := range all {
		if !slices.Contains(Known, e.Name) {
			return fmt.Errorf("bundle: unexpected entry %q", e.Name)
		}
		if err := writeEntry(tw, e, m.CreatedAt); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return ew.Close()
}

func writeEntry(tw *tar.Writer, e Entry, mt time.Time) error {
	mode := e.Mode
	if mode == 0 {
		mode = 0o600
	}
	if e.Path == "" {
		if err := tw.WriteHeader(&tar.Header{Name: e.Name, Mode: mode, Size: int64(len(e.Data)), ModTime: mt, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write(e.Data)
		return err
	}
	f, err := os.Open(e.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: e.Name, Mode: mode, Size: st.Size(), ModTime: mt, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err = io.CopyN(tw, f, st.Size())
	return err
}

// Extract decrypts r and writes the bundle's files into dir (mode 0600). It returns the manifest.
// Only the Known flat names are accepted; the manifest must come first.
func Extract(r io.Reader, passphrase, dir string) (*api.ControlBundleManifest, error) {
	dr, err := NewDecrypter(r, passphrase)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(dr)
	if err != nil {
		return nil, fmt.Errorf("bundle: %w", err)
	}
	tr := tar.NewReader(gz)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var m *api.ControlBundleManifest
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("bundle: %w", err)
		}
		if h.Typeflag != tar.TypeReg || !slices.Contains(Known, h.Name) || seen[h.Name] {
			return nil, fmt.Errorf("bundle: unexpected entry %q", h.Name)
		}
		seen[h.Name] = true
		if h.Name == FileManifest {
			var mm api.ControlBundleManifest
			if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&mm); err != nil {
				return nil, fmt.Errorf("bundle: manifest: %w", err)
			}
			if mm.Format != 1 {
				return nil, fmt.Errorf("bundle: unsupported format %d (newer Atoll?)", mm.Format)
			}
			m = &mm
			continue
		}
		if m == nil {
			return nil, errors.New("bundle: manifest missing")
		}
		f, err := os.OpenFile(filepath.Join(dir, h.Name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		_, cerr := io.Copy(f, tr)
		if err := f.Close(); cerr == nil {
			cerr = err
		}
		if cerr != nil {
			return nil, fmt.Errorf("bundle: %s: %w", h.Name, cerr)
		}
	}
	// gzip/tar can stop before the encrypted stream ends; drain it so a truncated or
	// tampered tail is still detected
	if _, err := io.Copy(io.Discard, dr); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("bundle: manifest missing")
	}
	for _, need := range []string{FileDB, FileMasterKey} {
		if !seen[need] {
			return nil, fmt.Errorf("bundle: %s missing", need)
		}
	}
	return m, nil
}
