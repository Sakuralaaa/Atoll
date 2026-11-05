package templates

import (
	"strings"
	"testing"
)

func TestAppsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Apps() {
		if a.ID == "" || seen[a.ID] || a.Name == "" || a.Description == "" || a.DescriptionEn == "" || !strings.HasPrefix(a.Color, "#") {
			t.Errorf("%q: bad metadata", a.ID)
		}
		seen[a.ID] = true
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
			for _, e := range s.Env {
				if strings.Contains(e.Value, "ATOLL_WEB_") {
					t.Errorf("%s/%s: %s uses the domain outside domain_env", a.ID, s.Name, e.Key)
				}
			}
		}
		if mains != 1 {
			t.Errorf("%s: %d main services", a.ID, mains)
		}
	}
	if v := FillRandom("a{{RANDOM}}b{{RANDOM}}"); len(v) != 66 || strings.Contains(v, "{{") || v[1:33] == v[34:66] {
		t.Errorf("FillRandom: %q", v)
	}
}
