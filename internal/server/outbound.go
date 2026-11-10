package server

import (
	"net"
	"net/http"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/netguard"
)

// Outbound HTTP clients for URLs users configure. Each dials through a netguard dialer, so
// the check applies to the resolved address of every connection, redirects included
// (docs/security-review-2026-11.md explains the levels).

// guardedClient builds an http.Client dialing through d. Proxies from the environment are
// ignored: a proxy would make the guard check the proxy, not the target.
func guardedClient(d *net.Dialer, timeout time.Duration, followRedirects bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = d.DialContext
	hc := &http.Client{Timeout: timeout, Transport: tr}
	if !followRedirects {
		hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return hc
}

// adminNotifyHTTP is the default NotifyHTTP (admin webhooks, Telegram): no loopback,
// link-local or cloud metadata; private networks stay reachable for a LAN webhook.
func adminNotifyHTTP() *http.Client {
	return guardedClient(netguard.Dialer(), 15*time.Second, true)
}

// publicOnlyHTTP is the default MemberNotifyHTTP: a sub-account's webhook dials public
// unicast addresses only and never follows redirects.
func publicOnlyHTTP() *http.Client {
	return guardedClient(netguard.PublicOnlyDialer(), 15*time.Second, false)
}

// adminEndpointHTTP is for endpoints an admin configures that may live on a private network
// or the same host (MinIO, a private registry): only cloud metadata addresses are refused.
func adminEndpointHTTP(timeout time.Duration) *http.Client {
	return guardedClient(netguard.NoMetadataDialer(), timeout, true)
}
