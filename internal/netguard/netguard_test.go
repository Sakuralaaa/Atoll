package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestCheckHost(t *testing.T) {
	bad := []string{"localhost", "LOCALHOST.", "api.localhost", "metadata.google.internal", "metadata.google.internal:80",
		"127.0.0.1", "127.8.9.10:8080", "[::1]", "::1", "0.0.0.0", "0.1.2.3", "::", "169.254.169.254", "169.254.1.1",
		"100.100.100.200", "fd00:ec2::254", "[fd00:ec2::254]:80", "fe80::1", "224.0.0.1", "ff02::1", "::ffff:127.0.0.1",
		"::ffff:10.0.0.1", "255.255.255.255", ""}
	for _, h := range bad {
		if err := CheckHost(h); !errors.Is(err, ErrBlocked) {
			t.Errorf("CheckHost(%q) = %v, want blocked", h, err)
		}
	}
	good := []string{"example.com", "s3.example.com:9000", "10.0.0.5", "192.168.1.10:443", "100.64.1.2", "8.8.8.8", "2001:4860:4860::8888"}
	for _, h := range good {
		if err := CheckHost(h); err != nil {
			t.Errorf("CheckHost(%q) = %v, want ok", h, err)
		}
	}
}

func TestCheckPublicIP(t *testing.T) {
	for _, s := range []string{"10.0.0.1", "172.16.0.1", "192.168.0.1", "100.64.0.1", "fc00::1", "64:ff9b::7f00:1", "127.0.0.1", "169.254.169.254", "::ffff:192.168.0.1"} {
		if err := CheckPublicIP(netip.MustParseAddr(s)); err == nil {
			t.Errorf("CheckPublicIP(%s) allowed", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if err := CheckPublicIP(netip.MustParseAddr(s)); err != nil {
			t.Errorf("CheckPublicIP(%s) = %v", s, err)
		}
	}
}

func TestMetadataOnly(t *testing.T) {
	for _, h := range []string{"169.254.169.254", "[fd00:ec2::254]:80", "100.100.100.200:80", "metadata.google.internal", "::ffff:169.254.169.254"} {
		if err := CheckMetadataHost(h); err == nil {
			t.Errorf("CheckMetadataHost(%q) allowed", h)
		}
	}
	for _, h := range []string{"127.0.0.1:9000", "10.0.0.8", "minio.lan", "localhost:9000"} {
		if err := CheckMetadataHost(h); err != nil {
			t.Errorf("CheckMetadataHost(%q) = %v (private S3/registry must stay allowed)", h, err)
		}
	}
}

func TestDialersEnforceAtConnect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	addr := srv.Listener.Addr().String() // 127.0.0.1:port
	try := func(d *net.Dialer) error {
		c, err := d.DialContext(context.Background(), "tcp", addr)
		if err == nil {
			c.Close()
		}
		return err
	}
	if err := try(PublicOnlyDialer()); err == nil {
		t.Fatal("PublicOnlyDialer reached loopback")
	}
	if err := try(Dialer()); err == nil {
		t.Fatal("Dialer reached loopback")
	}
	if err := try(NoMetadataDialer()); err != nil {
		t.Fatalf("NoMetadataDialer must allow loopback (local MinIO): %v", err)
	}
}
