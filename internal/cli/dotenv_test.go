package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestParseDotenv(t *testing.T) {
	in := "# comment\n\nexport A=1\nB = \"two words\\n\"\nC='raw \\n'\nD=x # note\nA=3\n"
	m, order, err := parseDotenv(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "A,B,C,D" {
		t.Fatalf("order %v", order)
	}
	want := map[string]string{"A": "3", "B": "two words\n", "C": `raw \n`, "D": "x"}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %q, want %q", k, m[k], v)
		}
	}
	if _, _, err := parseDotenv(strings.NewReader("oops\n")); err == nil {
		t.Fatal("want error for a line without =")
	}
}

func TestFormatDotenvRoundTrip(t *testing.T) {
	vals := []string{
		"", "plain", "${DB_HOST}:5432", "postgres://u:p@h/db?sslmode=disable", "two words", " lead", "trail ",
		"a # not a comment", "#hash", `"quoted"`, `'single'`, `it's`, `say "hi"`, `back\slash`, `ends\`, `\n literal`,
		"line1\nline2\n", "tab\there", "cr\r\nlf", "\u00a0nbsp\u00a0", "\ufeffbom", "mid\u3000space", "x=y=z", "é中文🙂", `"`, `\"`, `\\n`,
	}
	var vars []api.EnvVar
	for i, v := range vals {
		vars = append(vars, api.EnvVar{Key: fmt.Sprintf("K%d", i), Value: v})
	}
	vars = append(vars, api.EnvVar{Key: "API_TOKEN", Value: "", Secret: true})
	text, omitted := formatDotenv(vars, false)
	if omitted != 1 || !strings.Contains(text, "# API_TOKEN: secret") {
		t.Fatalf("secret not left out (%d):\n%s", omitted, text)
	}
	m, order, err := parseDotenv(strings.NewReader(text))
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if len(order) != len(vals) {
		t.Fatalf("read back %d keys, want %d:\n%s", len(order), len(vals), text)
	}
	for i, v := range vals {
		if got := m[fmt.Sprintf("K%d", i)]; got != v {
			t.Errorf("K%d: got %q, want %q (line %q)", i, got, v, strings.Split(text, "\n")[i])
		}
	}
	if _, ok := m["API_TOKEN"]; ok {
		t.Fatal("omitted secret was read back")
	}
	// with secrets the real value is written and read back
	text, omitted = formatDotenv([]api.EnvVar{{Key: "API_TOKEN", Value: "s3 cr#t\"\n", Secret: true}}, true)
	m, _, _ = parseDotenv(strings.NewReader(text))
	if omitted != 0 || m["API_TOKEN"] != "s3 cr#t\"\n" {
		t.Fatalf("include secrets: %q -> %q", text, m["API_TOKEN"])
	}
}

// atoll env export -o FILE, then atoll env import FILE: the same values go back to the panel.
func TestEnvExportImportCLI(t *testing.T) {
	f, srv := newFake(t)
	f.services = []api.Service{{ID: "svc_1", Name: "app", ProjectID: "prj_1", Kind: "image", Image: "nginx"}}
	stored := []api.EnvVar{
		{Key: "URL", Value: "https://x.example.com/?a=1#frag"},
		{Key: "MOTD", Value: "hello \"world\"\nline two\\n"},
		{Key: "REF", Value: "${DB_HOST}"},
		{Key: "API_TOKEN", Value: "tok en#1", Secret: true},
	}
	var reveals atomic.Int32
	f.extra = func(w http.ResponseWriter, r *http.Request, key string, body []byte) bool {
		if key != "GET /services/svc_1/env" {
			return false
		}
		if r.URL.Query().Get("reveal") == "1" {
			reveals.Add(1)
		}
		out := make([]api.EnvVar, len(stored))
		copy(out, stored)
		if r.URL.Query().Get("reveal") != "1" {
			for i := range out {
				if out[i].Secret {
					out[i].Value = ""
				}
			}
		}
		writeJSON(w, 200, out)
		return true
	}

	// default: stdout, secret left out as a comment
	r := runCLI(t, srv.URL, "", "env", "export", "-s", "app")
	if r.e != nil || !strings.Contains(r.out, "# API_TOKEN: secret") || strings.Contains(r.out, "tok en") {
		t.Fatalf("export: %v\n%s", r.e, r.out)
	}
	if reveals.Load() != 0 {
		t.Fatal("revealed without --include-secrets")
	}

	file := filepath.Join(t.TempDir(), "app.env")
	r = runCLI(t, srv.URL, "", "env", "export", "-s", "app", "--include-secrets", "-o", file)
	if r.e != nil || r.out != "" || !strings.Contains(r.err, "Wrote 4 variables") {
		t.Fatalf("export -o: %v out=%q err=%q", r.e, r.out, r.err)
	}
	if st, err := os.Stat(file); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", st, err)
	}

	r = runCLI(t, srv.URL, "", "env", "import", "-s", "app", "--no-redeploy", file)
	if r.e != nil {
		t.Fatalf("import: %v %s", r.e, r.err)
	}
	f.mu.Lock()
	body := f.bodies["PUT /services/svc_1/env"]
	f.mu.Unlock()
	var got []api.EnvVar
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("PUT body %q: %v", body, err)
	}
	if len(got) != len(stored) {
		t.Fatalf("PUT %+v", got)
	}
	for _, want := range stored {
		ok := false
		for _, g := range got {
			ok = ok || (g.Key == want.Key && g.Value == want.Value)
		}
		if !ok {
			t.Errorf("%s not round-tripped: %+v", want.Key, got)
		}
	}
}
