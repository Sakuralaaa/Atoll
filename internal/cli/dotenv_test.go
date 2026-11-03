package cli

import (
	"strings"
	"testing"
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
