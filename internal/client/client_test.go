package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/atoll-sh/atoll/internal/api"
)

func newFake(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer atl_pat_test" {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(api.ErrorBody{Error: "unauthorized"})
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "atl_pat_test")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func TestListServersAndAuth(t *testing.T) {
	c := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/servers" || r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		writeJSON(w, 200, []api.Server{{ID: "srv_aaaaaaaaaaaa", Name: "fra-1", Status: "online"}})
	})
	got, err := c.ListServers(context.Background())
	if err != nil || len(got) != 1 || got[0].Name != "fra-1" {
		t.Fatalf("got %+v %v", got, err)
	}
	bad := New(c.BaseURL+"/api/v1/", "wrong")
	_, err = bad.ListServers(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want 401, got %v", err)
	}
}

func TestErrorBody(t *testing.T) {
	c := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, api.ErrorBody{Error: "project not found", Code: "not_found"})
	})
	_, err := c.GetProject(context.Background(), "prj_x")
	if !IsNotFound(err) || !strings.Contains(err.Error(), "project not found") {
		t.Fatalf("got %v", err)
	}
}

func TestConfirmationAndWait(t *testing.T) {
	var polls int32
	c := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "DELETE" && r.URL.Path == "/api/v1/projects/prj_1":
			writeJSON(w, 202, api.Confirmation{ID: "cfm_1", Action: "project.delete", Summary: "delete project web", Status: "pending"})
		case r.Method == "GET" && r.URL.Path == "/api/v1/confirmations":
			st := "pending"
			if atomic.AddInt32(&polls, 1) >= 2 {
				st = "approved"
			}
			writeJSON(w, 200, []api.Confirmation{{ID: "cfm_1", Status: st}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	err := c.DeleteProject(context.Background(), "prj_1")
	cf, ok := AsConfirmation(err)
	if !ok || cf.ID != "cfm_1" {
		t.Fatalf("want confirmation, got %v", err)
	}
	if !strings.Contains(err.Error(), "cfm_1") || !strings.Contains(err.Error(), "--wait") {
		t.Fatalf("message should name id and --wait: %v", err)
	}
	got, err := c.WaitConfirmation(context.Background(), "cfm_1", 10*time.Millisecond)
	if err != nil || got.Status != "approved" {
		t.Fatalf("wait: %+v %v", got, err)
	}
}

func TestWaitConfirmationRejected(t *testing.T) {
	c := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, []api.Confirmation{{ID: "cfm_2", Status: "rejected"}})
	})
	if _, err := c.WaitConfirmation(context.Background(), "cfm_2", time.Millisecond); err == nil {
		t.Fatal("want error for rejected")
	}
}

func TestResolveServiceByNameAcrossProjects(t *testing.T) {
	c := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects":
			writeJSON(w, 200, []api.Project{{ID: "prj_a", Name: "a"}, {ID: "prj_b", Name: "b"}})
		case "/api/v1/projects/prj_a/services":
			writeJSON(w, 200, []api.Service{{ID: "svc_1", Name: "web"}, {ID: "svc_2", Name: "db"}})
		case "/api/v1/projects/prj_b/services":
			writeJSON(w, 200, []api.Service{{ID: "svc_3", Name: "web"}})
		case "/api/v1/projects/prj_b":
			writeJSON(w, 200, api.Project{ID: "prj_b", Name: "b", Services: []api.Service{{ID: "svc_3", Name: "web"}}})
		default:
			writeJSON(w, 404, api.ErrorBody{Error: "nope"})
		}
	})
	ctx := context.Background()
	s, err := c.ResolveService(ctx, "", "db")
	if err != nil || s.ID != "svc_2" {
		t.Fatalf("db: %+v %v", s, err)
	}
	if _, err := c.ResolveService(ctx, "", "web"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("want ambiguous, got %v", err)
	}
	s, err = c.ResolveService(ctx, "b", "WEB")
	if err != nil || s.ID != "svc_3" {
		t.Fatalf("b/web: %+v %v", s, err)
	}
}

func TestEnvMergeAndPut(t *testing.T) {
	cur := []api.EnvVar{{Key: "A", Value: "1"}, {Key: "SECRET", Value: "", Secret: true}, {Key: "OLD", Value: "x"}}
	got := MergeEnv(cur, []api.EnvVar{{Key: "A", Value: "2"}, {Key: "B", Value: "3"}}, []string{"OLD"})
	if len(got) != 3 || got[0].Value != "2" || got[1].Key != "SECRET" || got[1].Value != "" || got[2].Key != "B" {
		t.Fatalf("merge: %+v", got)
	}
	c := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		var in []api.EnvVar
		json.NewDecoder(r.Body).Decode(&in)
		if r.Method != "PUT" || len(in) != 3 {
			t.Errorf("put body %+v", in)
		}
		writeJSON(w, 200, in)
	})
	if _, err := c.PutEnv(context.Background(), "svc_1", got); err != nil {
		t.Fatal(err)
	}
}

func TestImportFlow(t *testing.T) {
	var uploaded int64
	var gets int32
	c := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/imports":
			var p api.ImportPlan
			json.NewDecoder(r.Body).Decode(&p)
			writeJSON(w, 200, api.ImportJob{ID: "imp_1", Plan: p, Status: "planned", ProjectID: "prj_n"})
		case r.Method == "PUT" && r.URL.Path == "/api/v1/imports/imp_1/volumes/web/data":
			if r.Header.Get("Content-Type") != "application/gzip" {
				t.Errorf("content type %q", r.Header.Get("Content-Type"))
			}
			n, _ := io.Copy(io.Discard, r.Body)
			uploaded = n
			w.WriteHeader(204)
		case r.Method == "POST" && r.URL.Path == "/api/v1/imports/imp_1/start":
			writeJSON(w, 200, api.ImportJob{ID: "imp_1", Status: "deploying"})
		case r.Method == "GET" && r.URL.Path == "/api/v1/imports/imp_1":
			st := "deploying"
			if atomic.AddInt32(&gets, 1) > 1 {
				st = "done"
			}
			writeJSON(w, 200, api.ImportJob{ID: "imp_1", Status: st})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	ctx := context.Background()
	job, err := c.CreateImport(ctx, api.ImportPlan{Source: "zeabur", ProjectName: "x"})
	if err != nil || job.ID != "imp_1" || job.Plan.ProjectName != "x" {
		t.Fatalf("%+v %v", job, err)
	}
	if err := c.UploadImportVolume(ctx, "imp_1", "web", "data", strings.NewReader("tarball"), 7); err != nil || uploaded != 7 {
		t.Fatalf("upload %v %d", err, uploaded)
	}
	if _, err := c.StartImport(ctx, "imp_1"); err != nil {
		t.Fatal(err)
	}
	job, err = c.WaitImport(ctx, "imp_1", time.Millisecond, nil)
	if err != nil || job.Status != "done" {
		t.Fatalf("%+v %v", job, err)
	}
}

func TestStreamLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer atl_pat_test" || r.URL.Query().Get("tail") != "2" {
			w.WriteHeader(401)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		for _, l := range []string{"one", "two"} {
			wsjson.Write(r.Context(), conn, api.LogLine{ServiceID: "svc_1", Line: l, Stream: "stdout"})
		}
		conn.Close(websocket.StatusNormalClosure, "")
	}))
	defer srv.Close()
	c := New(srv.URL, "atl_pat_test")
	lines, err := c.TailLogs(context.Background(), "svc_1", 2)
	if err != nil || len(lines) != 2 || lines[1].Line != "two" {
		t.Fatalf("%+v %v", lines, err)
	}
}

func TestPreviousDeployment(t *testing.T) {
	now := time.Now()
	deps := []api.Deployment{
		{ID: "dep_3", Status: "running", StartedAt: now},
		{ID: "dep_2", Status: "failed", StartedAt: now.Add(-time.Hour)},
		{ID: "dep_1", Status: "superseded", StartedAt: now.Add(-2 * time.Hour)},
	}
	d, err := PreviousDeployment(deps)
	if err != nil || d.ID != "dep_1" {
		t.Fatalf("%+v %v", d, err)
	}
}
