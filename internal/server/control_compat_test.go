package server

import "testing"

// docs/compatibility.md: one-click update installs stable releases only unless a tag is named,
// and never moves the control server to an older version.
func TestCheckUpdateTarget(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "v0.1.45"
	for _, tc := range []struct {
		tag        string
		prerelease bool
		explicit   bool
		want       string // "" = allowed, else the error code
	}{
		{"v0.1.46", false, false, ""},
		{"v0.2.0", false, true, ""},
		{"v0.2.0-beta.1", true, false, "prerelease"},
		{"v0.2.0-beta.1", false, false, "prerelease"}, // mislabelled on GitHub: the tag decides too
		{"v0.2.0-beta.1", true, true, ""},             // beta channel: opt-in by tag
		{"v0.1.44", false, true, "downgrade_unsupported"},
		{"v0.1.45-beta.3", true, true, "downgrade_unsupported"},
	} {
		err := checkUpdateTarget(&ghRelease{TagName: tc.tag, Prerelease: tc.prerelease}, tc.explicit)
		got := ""
		if err != nil {
			got = errCodeOf(err)
		}
		if got != tc.want {
			t.Errorf("%s prerelease=%v explicit=%v: got %q (%v), want %q", tc.tag, tc.prerelease, tc.explicit, got, err, tc.want)
		}
	}
	// a beta control server is offered the stable release of the same version
	Version = "v0.2.0-beta.2"
	if err := checkUpdateTarget(&ghRelease{TagName: "v0.2.0"}, false); err != nil {
		t.Fatalf("beta -> stable: %v", err)
	}
	if err := checkUpdateTarget(&ghRelease{TagName: "v0.1.46"}, false); err == nil {
		t.Fatal("beta -> older stable must be refused")
	}
	// development builds are not version-checked
	Version = "dev"
	if err := checkUpdateTarget(&ghRelease{TagName: "v0.1.0"}, true); err != nil {
		t.Fatalf("dev build: %v", err)
	}
}

func errCodeOf(err error) string {
	if ae, ok := err.(*apiError); ok {
		return ae.code
	}
	return err.Error()
}
