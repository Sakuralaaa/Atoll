package templates

import (
	"encoding/base64"
	"path"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestAppsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Apps() {
		if a.ID == "" || seen[a.ID] || a.Name == "" || a.Description == "" || a.DescriptionEn == "" || !strings.HasPrefix(a.Color, "#") {
			t.Errorf("%q: bad metadata", a.ID)
		}
		seen[a.ID] = true
		used := map[string]bool{}
		mark := func(v string) {
			for _, in := range a.Inputs {
				if strings.Contains(v, InputRef(in.Key)) {
					used[in.Key] = true
				}
				v = strings.ReplaceAll(v, InputRef(in.Key), "")
			}
			v = strings.ReplaceAll(v, RandomBase64Placeholder, "")
			v = strings.ReplaceAll(v, RandomPlaceholder, "")
			if strings.Contains(v, "{{") {
				t.Errorf("%s: unknown placeholder in %q", a.ID, v)
			}
		}
		for _, in := range a.Inputs {
			if in.Key == "" || in.Label == "" || in.LabelEn == "" {
				t.Errorf("%s: bad input %+v", a.ID, in)
			}
		}
		mains, names := 0, map[string]bool{}
		for _, s := range a.Services {
			if names[s.Name] {
				t.Errorf("%s: duplicate service %s", a.ID, s.Name)
			}
			names[s.Name] = true
			if s.Template != "" {
				if _, ok := Get(s.Template); !ok {
					t.Errorf("%s/%s: unknown template %s", a.ID, s.Name, s.Template)
				}
			} else if s.Image == "" {
				t.Errorf("%s/%s: no image", a.ID, s.Name)
			}
			if s.Main {
				mains++
				if len(s.Ports) == 0 || s.Ports[0].Name != "web" || s.Ports[0].Protocol != "http" {
					t.Errorf("%s/%s: main service needs an http port named web", a.ID, s.Name)
				}
			}
			for _, e := range append(append([]api.EnvVar(nil), s.Env...), s.DomainEnv...) {
				mark(e.Value)
			}
			for _, c := range s.Configs {
				mark(c.Content)
				if !path.IsAbs(c.Path) || c.Content == "" {
					t.Errorf("%s/%s: bad config %q", a.ID, s.Name, c.Path)
				}
				if strings.Contains(c.Content, "ATOLL_WEB_") || strings.Contains(c.Content, "${") {
					t.Errorf("%s/%s: config %s has a reference; configs are not expanded", a.ID, s.Name, c.Path)
				}
			}
			for _, e := range s.Env {
				if strings.Contains(e.Value, "ATOLL_WEB_") {
					t.Errorf("%s/%s: %s uses the domain outside domain_env", a.ID, s.Name, e.Key)
				}
			}
		}
		for _, in := range a.Inputs {
			if !used[in.Key] {
				t.Errorf("%s: input %s is never used", a.ID, in.Key)
			}
		}
		if mains != 1 {
			t.Errorf("%s: %d main services", a.ID, mains)
		}
	}
	if v := FillRandom("a{{RANDOM}}b{{RANDOM}}"); len(v) != 66 || strings.Contains(v, "{{") || v[1:33] == v[34:66] {
		t.Errorf("FillRandom: %q", v)
	}
	k := FillRandom("{{RANDOM_BASE64}}")
	if b, err := base64.StdEncoding.DecodeString(k); err != nil || len(b) != 32 || k == FillRandom("{{RANDOM_BASE64}}") {
		t.Errorf("FillRandom base64: %q", k)
	}
}

func TestResolveInputs(t *testing.T) {
	a, _ := App("outlook-email")
	got, err := ResolveInputs(a, map[string]string{"LOGIN_PASSWORD": "typed-pass-1"})
	if err != nil || got["LOGIN_PASSWORD"] != "typed-pass-1" {
		t.Fatalf("given: %v %v", got, err)
	}
	if v := Fill("x="+InputRef("LOGIN_PASSWORD"), got); v != "x=typed-pass-1" {
		t.Errorf("Fill: %q", v)
	}
	for _, given := range []map[string]string{nil, {"LOGIN_PASSWORD": ""}} {
		got, err := ResolveInputs(a, given)
		if err != nil || len(got["LOGIN_PASSWORD"]) != 32 {
			t.Errorf("empty -> random: %v %v", got, err)
		}
	}
	for _, bad := range []map[string]string{{"NOPE": "whatever1"}, {"LOGIN_PASSWORD": "short"}, {"LOGIN_PASSWORD": `has"quote1`}, {"LOGIN_PASSWORD": "x${HOME}xxxx"}, {"LOGIN_PASSWORD": "line\nbreak1"}} {
		if _, err := ResolveInputs(a, bad); err == nil {
			t.Errorf("accepted %q", bad)
		} else if v := bad["LOGIN_PASSWORD"]; v != "" && strings.Contains(err.Error(), v) {
			t.Errorf("error leaks the value: %v", err)
		}
	}
}
