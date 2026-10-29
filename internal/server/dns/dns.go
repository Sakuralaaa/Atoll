// Package dns manages public DNS records for bound domains behind a small
// provider interface. Cloudflare is the only implementation for now.
package dns

import (
	"context"
	"errors"
)

// ErrNotConfigured means no provider credentials are set; callers should treat
// DNS as manual and surface a clear status instead of failing.
var ErrNotConfigured = errors.New("dns: provider not configured (set a Cloudflare API token and zone in Settings)")

// Provider creates and removes A records.
type Provider interface {
	// UpsertA points hostname at ipv4, creating or updating the record. It returns the record ID.
	UpsertA(ctx context.Context, hostname, ipv4 string, proxied bool) (string, error)
	// Delete removes the record by ID, or by hostname lookup when id is empty. Missing records are not an error.
	Delete(ctx context.Context, hostname, id string) error
}

// Noop is used when nothing is configured.
type Noop struct{}

func (Noop) UpsertA(context.Context, string, string, bool) (string, error) {
	return "", ErrNotConfigured
}
func (Noop) Delete(context.Context, string, string) error { return ErrNotConfigured }
