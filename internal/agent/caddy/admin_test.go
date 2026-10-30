package caddy

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminLoadErrors(t *testing.T) {
	dir, err := os.MkdirTemp("", "caddy")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "admin.sock") // short path: unix socket limit
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("unix sockets unavailable:", err)
	}
	var got []byte
	var path, ctype, host string
	fail := false
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, ctype, host = r.URL.Path, r.Header.Get("Content-Type"), r.Host
		got, _ = io.ReadAll(r.Body)
		if fail {
			http.Error(w, "loading config: bad route", http.StatusBadRequest)
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()

	a := Admin{SocketPath: sock}
	cfg := []byte(`{"apps":{}}`)
	if err := a.Load(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if path != "/load" || ctype != "application/json" || string(got) != string(cfg) || host != "127.0.0.1" {
		t.Fatalf("path=%s ctype=%s host=%s body=%s", path, ctype, host, got)
	}
	fail = true
	if err := a.Load(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "bad route") {
		t.Fatalf("err %v", err)
	}
	if err := (Admin{SocketPath: filepath.Join(dir, "missing.sock")}).Load(context.Background(), cfg); err == nil {
		t.Fatal("missing socket must error")
	}
}
