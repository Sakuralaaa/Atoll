package dns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OriginCert is a Cloudflare Origin CA certificate with its locally generated key.
type OriginCert struct {
	ID       string
	CertPEM  string
	KeyPEM   string
	NotAfter time.Time
}

// originValidityDays is the longest validity Cloudflare offers (15 years).
const originValidityDays = 5475

// IssueOriginCert asks Cloudflare's Origin CA for a certificate covering root and *.root.
// The private key never leaves this process. The token needs the zone permission
// "SSL and Certificates: Edit"; without it Cloudflare answers with an auth error.
func (c *Cloudflare) IssueOriginCert(ctx context.Context, root string) (*OriginCert, error) {
	if c.Token == "" {
		return nil, ErrNotConfigured
	}
	root = strings.ToLower(strings.Trim(root, "."))
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	hosts := []string{root, "*." + root}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: root},
		DNSNames: hosts,
	}, key)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"hostnames":          hosts,
		"requested_validity": originValidityDays,
		"request_type":       "origin-ecc",
		"csr":                string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})),
	}
	var out struct {
		ID          string `json:"id"`
		Certificate string `json:"certificate"`
	}
	if err := c.do(ctx, http.MethodPost, "/certificates", body, &out); err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(out.Certificate))
	if block == nil {
		return nil, errors.New("cloudflare: origin CA returned no PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: origin CA certificate: %w", err)
	}
	return &OriginCert{
		ID:       out.ID,
		CertPEM:  strings.TrimSpace(out.Certificate) + "\n",
		KeyPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})),
		NotAfter: cert.NotAfter,
	}, nil
}

// RevokeOriginCert revokes an Origin CA certificate (best effort; 404 is not an error).
func (c *Cloudflare) RevokeOriginCert(ctx context.Context, id string) error {
	if c.Token == "" || id == "" {
		return nil
	}
	err := c.do(ctx, http.MethodDelete, "/certificates/"+id, nil, nil)
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		return nil
	}
	return err
}
