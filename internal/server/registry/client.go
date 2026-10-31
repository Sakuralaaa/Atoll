package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ManifestAccept lists the media types sent on manifest requests: OCI index and
// manifest, Docker manifest list and v2 manifest. Registries answer with the digest of
// what the agent's `docker pull` would record (the index for multi-arch images).
var ManifestAccept = []string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}

// Auth is a registry login (stored registry credential). Empty = anonymous.
type Auth struct {
	Username string
	Password string
}

// ErrRateLimited is returned (wrapped in *RateLimitError) when the registry answered 429
// or is still inside its backoff window.
var ErrRateLimited = errors.New("registry rate limit")

// RateLimitError carries when the registry may be asked again.
type RateLimitError struct {
	Host  string
	Until time.Time
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("%s: rate limited until %s", e.Host, e.Until.UTC().Format(time.RFC3339))
}
func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

// ErrNotFound means the repository or tag does not exist (or is hidden without auth).
var ErrNotFound = errors.New("manifest not found")

// ErrUnauthorized means the registry refused the credentials (or anonymous access).
var ErrUnauthorized = errors.New("registry denied access")

// Client talks to registries. The zero value is not usable; use New.
type Client struct {
	HTTP *http.Client
	// PlainHTTP selects http:// for a registry host (tests, insecure local registries).
	PlainHTTP func(host string) bool
	// Now is the clock (tests).
	Now func() time.Time
	// DigestTTL / TagsTTL cache successful answers so several services on the same
	// image cost one request.
	DigestTTL time.Duration
	TagsTTL   time.Duration
	// MaxTagPages bounds tag-list pagination.
	MaxTagPages int

	mu      sync.Mutex
	tokens  map[string]tokenEntry  // host|scope|user -> token
	digests map[string]cacheEntry  // key:tag|user -> digest
	tags    map[string]tagsEntry   // key|user -> tags
	backoff map[string]backoffInfo // host -> state
}

type tokenEntry struct {
	token   string
	expires time.Time
}
type cacheEntry struct {
	val string
	at  time.Time
}
type tagsEntry struct {
	val []string
	at  time.Time
}
type backoffInfo struct {
	until   time.Time
	strikes int
}

// New returns a client with sane defaults.
func New() *Client {
	return &Client{
		HTTP:        &http.Client{Timeout: 20 * time.Second},
		Now:         time.Now,
		DigestTTL:   10 * time.Minute,
		TagsTTL:     30 * time.Minute,
		MaxTagPages: 10,
		tokens:      map[string]tokenEntry{},
		digests:     map[string]cacheEntry{},
		tags:        map[string]tagsEntry{},
		backoff:     map[string]backoffInfo{},
	}
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) base(host string) string {
	if c.PlainHTTP != nil && c.PlainHTTP(host) {
		return "http://" + host
	}
	return "https://" + host
}

// BackoffUntil reports until when host is in backoff (zero when it is not).
func (c *Client) BackoffUntil(host string) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.backoff[host]
	if b.until.After(c.now()) {
		return b.until
	}
	return time.Time{}
}

func (c *Client) checkBackoff(host string) error {
	if u := c.BackoffUntil(host); !u.IsZero() {
		return &RateLimitError{Host: host, Until: u}
	}
	return nil
}

// strike records a 429: Retry-After when given, else 1m doubling up to 1h.
func (c *Client) strike(host string, retryAfter time.Duration) *RateLimitError {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.backoff[host]
	b.strikes++
	d := retryAfter
	if d <= 0 {
		d = time.Minute << min(b.strikes-1, 6)
	}
	if d > time.Hour {
		d = time.Hour
	}
	b.until = c.now().Add(d)
	c.backoff[host] = b
	return &RateLimitError{Host: host, Until: b.until}
}

func (c *Client) clearStrikes(host string) {
	c.mu.Lock()
	delete(c.backoff, host)
	c.mu.Unlock()
}

func parseRetryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return t.Sub(now)
	}
	return 0
}

// Digest resolves ref's tag to the manifest digest with a HEAD request.
// A digest-only ref returns its digest unchanged.
func (c *Client) Digest(ctx context.Context, ref Ref, auth Auth) (string, error) {
	if ref.Tag == "" {
		return ref.Digest, nil
	}
	key := ref.Key() + ":" + ref.Tag + "|" + auth.Username
	c.mu.Lock()
	if e, ok := c.digests[key]; ok && c.now().Sub(e.at) < c.DigestTTL {
		c.mu.Unlock()
		return e.val, nil
	}
	c.mu.Unlock()
	u := c.base(ref.apiHost()) + "/v2/" + ref.Repository + "/manifests/" + url.PathEscape(ref.Tag)
	resp, err := c.do(ctx, http.MethodHead, u, ref, auth)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	dg := resp.Header.Get("Docker-Content-Digest")
	if dg == "" {
		// Registry without the header on HEAD: fall back to GET and hash the body.
		resp, err = c.do(ctx, http.MethodGet, u, ref, auth)
		if err != nil {
			return "", err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return "", err
		}
		if dg = resp.Header.Get("Docker-Content-Digest"); dg == "" {
			sum := sha256.Sum256(body)
			dg = "sha256:" + hex.EncodeToString(sum[:])
		}
	}
	c.mu.Lock()
	c.digests[key] = cacheEntry{val: dg, at: c.now()}
	c.mu.Unlock()
	return dg, nil
}

// Tags lists the repository's tags (paginated with the Link header, bounded by MaxTagPages).
func (c *Client) Tags(ctx context.Context, ref Ref, auth Auth) ([]string, error) {
	key := ref.Key() + "|" + auth.Username
	c.mu.Lock()
	if e, ok := c.tags[key]; ok && c.now().Sub(e.at) < c.TagsTTL {
		c.mu.Unlock()
		return e.val, nil
	}
	c.mu.Unlock()
	base := c.base(ref.apiHost())
	next := base + "/v2/" + ref.Repository + "/tags/list?n=1000"
	var all []string
	pages := c.MaxTagPages
	if pages <= 0 {
		pages = 10
	}
	for i := 0; i < pages && next != ""; i++ {
		resp, err := c.do(ctx, http.MethodGet, next, ref, auth)
		if err != nil {
			return nil, err
		}
		var body struct {
			Tags []string `json:"tags"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("tags list: %w", err)
		}
		all = append(all, body.Tags...)
		next = nextLink(base, resp.Header.Get("Link"))
	}
	c.mu.Lock()
	c.tags[key] = tagsEntry{val: all, at: c.now()}
	c.mu.Unlock()
	return all, nil
}

// nextLink parses `Link: </v2/x/tags/list?n=1000&last=y>; rel="next"`.
func nextLink(base, h string) string {
	if h == "" || !strings.Contains(h, `rel="next"`) {
		return ""
	}
	i, j := strings.Index(h, "<"), strings.Index(h, ">")
	if i < 0 || j <= i {
		return ""
	}
	l := h[i+1 : j]
	if strings.HasPrefix(l, "/") {
		return base + l
	}
	return l
}

// do sends a request, answering a 401 challenge once (bearer token or basic auth).
func (c *Client) do(ctx context.Context, method, u string, ref Ref, auth Auth) (*http.Response, error) {
	host := ref.apiHost()
	if err := c.checkBackoff(host); err != nil {
		return nil, err
	}
	scope := "repository:" + ref.Repository + ":pull"
	tokKey := host + "|" + scope + "|" + auth.Username
	send := func(authz string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", strings.Join(ManifestAccept, ", "))
		req.Header.Set("User-Agent", "atoll-server (image update check)")
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		return c.HTTP.Do(req)
	}
	authz := ""
	c.mu.Lock()
	if t, ok := c.tokens[tokKey]; ok && c.now().Before(t.expires) {
		authz = "Bearer " + t.token
	}
	c.mu.Unlock()
	resp, err := send(authz)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		ch := resp.Header.Get("WWW-Authenticate")
		resp.Body.Close()
		authz, err = c.answerChallenge(ctx, ch, scope, tokKey, auth)
		if err != nil {
			return nil, err
		}
		if resp, err = send(authz); err != nil {
			return nil, err
		}
	}
	return c.check(resp, host)
}

func (c *Client) check(resp *http.Response, host string) (*http.Response, error) {
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		resp.Body.Close()
		return nil, c.strike(host, parseRetryAfter(resp.Header.Get("Retry-After"), c.now()))
	case resp.StatusCode == http.StatusNotFound:
		resp.Body.Close()
		return nil, ErrNotFound
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		resp.Body.Close()
		return nil, ErrUnauthorized
	case resp.StatusCode >= 300:
		resp.Body.Close()
		return nil, fmt.Errorf("%s: unexpected status %d", host, resp.StatusCode)
	}
	c.clearStrikes(host)
	return resp, nil
}

// answerChallenge turns a WWW-Authenticate header into an Authorization value.
func (c *Client) answerChallenge(ctx context.Context, ch, scope, tokKey string, auth Auth) (string, error) {
	scheme, params := parseChallenge(ch)
	switch strings.ToLower(scheme) {
	case "basic":
		if auth.Username == "" {
			return "", ErrUnauthorized
		}
		req, _ := http.NewRequest("GET", "http://x", nil)
		req.SetBasicAuth(auth.Username, auth.Password)
		return req.Header.Get("Authorization"), nil
	case "bearer":
	default:
		return "", fmt.Errorf("%w: unsupported auth challenge %q", ErrUnauthorized, ch)
	}
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("%w: bearer challenge without realm", ErrUnauthorized)
	}
	q := url.Values{}
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	if s := params["scope"]; s != "" {
		scope = s
	}
	q.Set("scope", scope)
	tu := realm
	if strings.Contains(tu, "?") {
		tu += "&" + q.Encode()
	} else {
		tu += "?" + q.Encode()
	}
	rq, err := url.Parse(tu)
	if err != nil {
		return "", err
	}
	if err := c.checkBackoff(rq.Host); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tu, nil)
	if err != nil {
		return "", err
	}
	if auth.Username != "" {
		req.SetBasicAuth(auth.Username, auth.Password)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	if resp, err = c.check(resp, rq.Host); err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok); err != nil {
		return "", fmt.Errorf("token response: %w", err)
	}
	t := tok.Token
	if t == "" {
		t = tok.AccessToken
	}
	if t == "" {
		return "", fmt.Errorf("%w: empty token", ErrUnauthorized)
	}
	ttl := time.Duration(tok.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	c.mu.Lock()
	c.tokens[tokKey] = tokenEntry{token: t, expires: c.now().Add(ttl - ttl/10)}
	c.mu.Unlock()
	return "Bearer " + t, nil
}

// parseChallenge parses `Bearer realm="...",service="...",scope="..."`.
func parseChallenge(h string) (string, map[string]string) {
	scheme, rest, _ := strings.Cut(strings.TrimSpace(h), " ")
	params := map[string]string{}
	for rest != "" {
		rest = strings.TrimLeft(rest, ", ")
		k, v, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		k = strings.ToLower(strings.TrimSpace(k))
		if strings.HasPrefix(v, `"`) {
			end := strings.Index(v[1:], `"`)
			if end < 0 {
				params[k] = v[1:]
				break
			}
			params[k] = v[1 : end+1]
			rest = v[end+2:]
		} else {
			val, r, _ := strings.Cut(v, ",")
			params[k] = strings.TrimSpace(val)
			rest = r
		}
	}
	return scheme, params
}
