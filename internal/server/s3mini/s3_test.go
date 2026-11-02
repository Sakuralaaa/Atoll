package s3mini

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Vectors from the AWS "Signature Version 4 (header-based auth)" examples for S3.
func TestSignAWSExamples(t *testing.T) {
	c := &Client{AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", Region: "us-east-1",
		Now: func() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) }}
	for _, tc := range []struct {
		query url.Values
		sig   string
	}{
		{url.Values{"lifecycle": {""}}, "fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543"},
		{url.Values{"max-keys": {"2"}, "prefix": {"J"}}, "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"},
	} {
		req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/", nil)
		req.URL.RawQuery = canonicalQuery(tc.query)
		c.sign(req, "/", emptyHash)
		if a := req.Header.Get("Authorization"); !strings.HasSuffix(a, "Signature="+tc.sig) {
			t.Fatalf("%v: %s", tc.query, a)
		}
	}
}

func TestEscapePath(t *testing.T) {
	if got := escapePath("/b/atoll/control/cb_1.atollbak"); got != "/b/atoll/control/cb_1.atollbak" {
		t.Fatal(got)
	}
	if got := escapePath("/b/a b+c"); got != "/b/a%20b%2Bc" {
		t.Fatal(got)
	}
}
