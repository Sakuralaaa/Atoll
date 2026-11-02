package ctlbundle

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func init() { logN = 10 } // fast tests

func TestEncryptRoundTripSizes(t *testing.T) {
	for _, n := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 1, 3*chunkSize + 17} {
		pt := make([]byte, n)
		rand.Read(pt)
		var buf bytes.Buffer
		w, err := NewEncrypter(&buf, "correct horse battery")
		if err != nil {
			t.Fatal(err)
		}
		// odd write sizes
		for i := 0; i < len(pt); i += 7777 {
			w.Write(pt[i:min(i+7777, len(pt))])
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		r, err := NewDecrypter(bytes.NewReader(buf.Bytes()), "correct horse battery")
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		got, err := io.ReadAll(r)
		if err != nil || !bytes.Equal(got, pt) {
			t.Fatalf("n=%d: round trip mismatch (%v)", n, err)
		}
		// wrong passphrase fails before any plaintext
		if _, err := NewDecrypter(bytes.NewReader(buf.Bytes()), "wrong passphrase!"); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("n=%d wrong passphrase: %v", n, err)
		}
		// truncation (drop the final chunk or part of it) is detected
		if n > chunkSize {
			cut := buf.Bytes()[:buf.Len()-20]
			r, err := NewDecrypter(bytes.NewReader(cut), "correct horse battery")
			if err == nil {
				_, err = io.ReadAll(r)
			}
			if !errors.Is(err, ErrDecrypt) {
				t.Fatalf("n=%d truncated: %v", n, err)
			}
		}
	}
	if _, err := NewDecrypter(bytes.NewReader([]byte("PK\x03\x04 not a bundle at all......")), "x"); !errors.Is(err, ErrNotBundle) {
		t.Fatalf("not a bundle: %v", err)
	}
}

func TestTamperDetected(t *testing.T) {
	var buf bytes.Buffer
	w, _ := NewEncrypter(&buf, "passphrase-123456")
	w.Write(bytes.Repeat([]byte("a"), 2*chunkSize+5))
	w.Close()
	b := buf.Bytes()
	b[len(b)-3] ^= 1
	r, err := NewDecrypter(bytes.NewReader(b), "passphrase-123456")
	if err == nil {
		_, err = io.ReadAll(r)
	}
	if !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered: %v", err)
	}
}

func TestBundleWriteExtract(t *testing.T) {
	src := t.TempDir()
	db := filepath.Join(src, "snap.db")
	os.WriteFile(db, bytes.Repeat([]byte("sqlite"), 50000), 0o600)
	var buf bytes.Buffer
	m := api.ControlBundleManifest{Version: "v9.9.9", CreatedAt: time.Now().UTC(), Hostname: "box"}
	err := Write(&buf, "a long passphrase", m, []Entry{
		{Name: FileDB, Path: db},
		{Name: FileMasterKey, Data: []byte("KEY")},
		{Name: FileServerEnv, Data: []byte("ATOLL_PUBLIC_URL=https://panel.example.com\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	got, err := Extract(bytes.NewReader(buf.Bytes()), "a long passphrase", out)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "v9.9.9" || len(got.Files) != 3 {
		t.Fatalf("manifest %+v", got)
	}
	a, _ := os.ReadFile(db)
	b, _ := os.ReadFile(filepath.Join(out, FileDB))
	if !bytes.Equal(a, b) {
		t.Fatal("db differs")
	}
	if k, _ := os.ReadFile(filepath.Join(out, FileMasterKey)); string(k) != "KEY" {
		t.Fatal("key differs")
	}
	if _, err := Extract(bytes.NewReader(buf.Bytes()), "not the passphrase", t.TempDir()); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	if err := Write(io.Discard, "a long passphrase", m, []Entry{{Name: "../etc/passwd", Data: []byte("x")}}); err == nil {
		t.Fatal("unexpected entry accepted")
	}
}
