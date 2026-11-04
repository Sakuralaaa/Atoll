// Package server is atoll-server: the control plane REST API, agent hub,
// DNS automation, migrations and imports. See docs/api.md.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/dns"
	"github.com/Sakuralaaa/Atoll/internal/server/secrets"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// Version is set at build time with -ldflags "-X github.com/Sakuralaaa/Atoll/internal/server.Version=v1.2.3".
var Version = "dev"

// Commit is the source revision, set at release time with -ldflags "-X …/internal/server.Commit=abc123".
// Release builds stamp it explicitly because the release tree is never byte-identical to the commit:
// sync.sh overwrites the tracked placeholder in internal/server/webui/dist with the built panel, which
// would make Go's own vcs stamp report "-dirty". Empty = fall back to the VCS info Go embedded.
var Commit = ""

// Config configures a Server. Zero durations get defaults.
type Config struct {
	DataDir   string
	PublicURL string // e.g. https://atoll.example.com; empty = derive from requests

	HeartbeatTimeout  time.Duration // agent considered offline after this silence (90s)
	RequestTimeout    time.Duration // default agent request timeout (30s)
	TransferTimeout   time.Duration // volume/image/db transfer timeout (60m)
	DeployWaitTimeout time.Duration // how long migrations/imports wait for a deployment to run (5m)
	VerifyTimeout     time.Duration // how long cutover waits for healthy containers (3m)

	CloudflareBaseURL string // override for tests
	// LookupHost resolves a hostname (default net.DefaultResolver); override for tests.
	LookupHost func(ctx context.Context, host string) ([]string, error)
	// ResolvePublic resolves a hostname through public resolvers (default DNS-over-HTTPS at
	// Cloudflare and Google), used to wait for a new record before Caddy asks for a cert.
	ResolvePublic func(ctx context.Context, host string) ([]string, error)
	// ProbeHTTPS checks that https://host serves a valid certificate (default: real request).
	ProbeHTTPS func(ctx context.Context, host string) error
	// DNSProvider overrides the provider built from settings (tests).
	DNSProvider     dns.Provider
	DNSWaitTimeout  time.Duration // how long a new domain waits to resolve publicly before its route is pushed (90s)
	CertWaitTimeout time.Duration // how long a new domain's HTTPS probe may take before it is marked active anyway (3m)
	ProvisionPoll   time.Duration // DNS poll interval while provisioning (3s; HTTPS probes run at twice this)
	Logger          *slog.Logger
	// TrustedProxies are peers whose X-Forwarded-For is believed (nil = loopback only).
	TrustedProxies []netip.Prefix
	// BuildTimeout bounds one build from source on a worker (20m); BuildKeep is how many
	// build images a service keeps on its server for rollback (3).
	BuildTimeout time.Duration
	BuildKeep    int

	// 中控 / control (control*.go)
	ListenAddr    string // main listener (health checks of the self-update helper)
	GitHubAPI     string // default https://api.github.com (tests)
	GitHubRepo    string // default Sakuralaaa/Atoll
	UnitName      string // systemd unit, default atoll-server
	ServerEnvPath string // default /etc/atoll/server.env (included in control backups)
	CaddyfilePath string // default /etc/caddy/Caddyfile (included when readable)
	BinaryPath    string // default os.Executable() (tests)
	// SpawnHelper starts the detached self-update helper (default systemd-run); tests stub it.
	SpawnHelper func(ctx context.Context, args []string) error
	// GeoLookup replaces the ipinfo.io / ip-api.com lookup of a public IP (tests).
	GeoLookup func(ctx context.Context, ip string) (*api.NodeGeo, error)
	// Journalctl runs journalctl with args (default exec); tests stub it.
	Journalctl func(ctx context.Context, args ...string) ([]byte, error)
	S3HTTP     *http.Client // HTTP client for the control backup bucket (tests)
	// AdoptPublicIP points the panel's DNS record (public URL host under the Cloudflare zone)
	// at this host's public IPv4 on start (--adopt-public-ip). PublicIP overrides detection.
	AdoptPublicIP bool
	// Problems & notifications (problems.go, notify.go)
	ProblemsTick time.Duration // how often conditions are evaluated (30s)
	TelegramAPI  string        // default https://api.telegram.org (tests)
	NotifyHTTP   *http.Client  // client for webhooks and Telegram (default 15s timeout)
	// Retention of history (audit log, decided confirmations, finished imports, old
	// deployments). Nil means store.DefaultRetention.
	Retention        *store.Retention
	PublicIP         string
	LookupPublicIPv4 func(ctx context.Context) (string, error)
}

// Server is the control plane.
type Server struct {
	cfg     Config
	st      *store.Store
	hub     *Hub
	events  *eventBus
	log     *slog.Logger
	geoKick chan struct{} // geoip.go
	lock    *lockout
	mfa     *pendingLogins

	jobMu   sync.Mutex
	jobBusy map[string]bool

	applyMu sync.Mutex
	applyQ  map[string]*applyQueue // per-service deploy queue (deploy.go)

	holdMu sync.Mutex
	held   map[string]int // hostnames kept out of pushed route tables (provision.go)

	cfCheckMu sync.Mutex
	cfCheck   cfCheckCache // last GET /cloudflare/token-check (cfcheck.go)

	deliveries deliverySet   // GitHub webhook X-GitHub-Delivery ids already handled (git.go)
	agentSums  agentSumCache // SHA-256 of hosted agent binaries for the join command (routes.go)
	agentUpg   agentUpgrades // panel-started agent upgrades (agent_upgrade.go)

	upd *updater      // image update detection (imageupdates.go)
	net *netState     // private network provider + mesh (network.go)
	ctl *controlState // 中控: the control server itself (control*.go)

	bg     context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// closing is set by Close; long-lived handlers (agent connections) join wg only
	// while it is false, so Close also waits for their cleanup writes.
	closingMu sync.Mutex
	closing   bool
}

// New opens the data dir (creating master.key and the database) and returns a Server.
func New(cfg Config) (*Server, error) {
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("data dir required")
	}
	def := func(d *time.Duration, v time.Duration) {
		if *d == 0 {
			*d = v
		}
	}
	def(&cfg.HeartbeatTimeout, 90*time.Second)
	def(&cfg.RequestTimeout, 30*time.Second)
	def(&cfg.TransferTimeout, 60*time.Minute)
	def(&cfg.DeployWaitTimeout, 5*time.Minute)
	def(&cfg.VerifyTimeout, 3*time.Minute)
	def(&cfg.DNSWaitTimeout, 90*time.Second)
	def(&cfg.CertWaitTimeout, 3*time.Minute)
	def(&cfg.ProvisionPoll, 3*time.Second)
	def(&cfg.BuildTimeout, 20*time.Minute)
	def(&cfg.ProblemsTick, 30*time.Second)
	if cfg.Retention == nil {
		r := store.DefaultRetention
		cfg.Retention = &r
	}
	if cfg.TelegramAPI == "" {
		cfg.TelegramAPI = "https://api.telegram.org"
	}
	if cfg.NotifyHTTP == nil {
		cfg.NotifyHTTP = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.BuildKeep <= 0 {
		cfg.BuildKeep = 3
	}
	if cfg.ResolvePublic == nil {
		cfg.ResolvePublic = dns.DoH{}.Resolve
	}
	if cfg.ProbeHTTPS == nil {
		cfg.ProbeHTTPS = probeHTTPS
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.LookupHost == nil {
		cfg.LookupHost = net.DefaultResolver.LookupHost
	}
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	for _, d := range []string{cfg.DataDir, filepath.Join(cfg.DataDir, "blobs")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	key, err := secrets.LoadOrCreateKey(filepath.Join(cfg.DataDir, "master.key"))
	if err != nil {
		return nil, err
	}
	box, err := secrets.New(key)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "atoll.db"), box)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, st: st, log: cfg.Logger, events: newEventBus(), lock: newLockout(), mfa: newPendingLogins(), jobBusy: map[string]bool{}, applyQ: map[string]*applyQueue{}, held: map[string]int{}, geoKick: make(chan struct{}, 1)}
	s.hub = newHub(s)
	s.upd = newUpdater()
	s.ctl = newControlState()
	s.bg, s.cancel = context.WithCancel(context.Background())
	if ups, err := st.ListAgentUpgrades(s.bg); err == nil {
		s.agentUpg.load(ups)
	} else {
		s.log.Warn("load agent upgrades", "err", err)
	}
	s.agentUpg.save = func(nodeID string, u api.AgentUpgrade) {
		if err := st.SaveAgentUpgrade(context.Background(), nodeID, u); err != nil {
			s.log.Warn("save agent upgrade", "node", nodeID, "err", err)
		}
	}
	// Nobody is connected right after start.
	nodes, _ := st.ListNodes(s.bg)
	for _, n := range nodes {
		if n.Status == "online" {
			st.SetNodeStatus(s.bg, n.ID, "offline", false)
		}
	}
	// Domains a restart caught mid-provisioning stay out of the routes until DNS is visible.
	var resume []api.Domain
	if doms, err := st.ListDomains(s.bg, "", ""); err == nil {
		for _, d := range doms {
			if d.Status == statusProvisioning {
				s.holdRoute(d.Hostname)
				resume = append(resume, d.Domain)
			}
		}
	}
	s.goBG(func(bg context.Context) { s.resumeProvisioning(bg, resume) })
	s.wg.Add(1)
	go s.housekeeping()
	s.wg.Add(1)
	go s.imageUpdateLoop()
	s.startBackupScheduler() // backups.go
	s.startProblemWatcher()  // problems.go
	s.startGeoIP()           // geoip.go
	// control*.go: own metrics, daily control backup, panel DNS after a move, update result
	s.controlSampleLoop()
	s.startControlBackupScheduler()
	s.startAdoptPublicIP()
	s.watchUpdateResult()
	return s, nil
}

// Close stops background work and closes the database.
func (s *Server) Close() error {
	s.closingMu.Lock()
	s.closing = true
	s.closingMu.Unlock()
	s.cancel()
	s.hub.closeAll()
	s.wg.Wait()
	return s.st.Close()
}

// Store exposes the store (tests, main).
func (s *Server) Store() *store.Store { return s.st }

// track registers a long-lived request handler with Close. It returns false once the server
// is closing; otherwise the caller must call s.wg.Done when it returns.
func (s *Server) track() bool {
	s.closingMu.Lock()
	defer s.closingMu.Unlock()
	if s.closing {
		return false
	}
	s.wg.Add(1)
	return true
}

// goBG runs fn in a tracked background goroutine.
func (s *Server) goBG(fn func(ctx context.Context)) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		fn(s.bg)
	}()
}

func (s *Server) housekeeping() {
	defer s.wg.Done()
	hb := time.NewTicker(s.cfg.HeartbeatTimeout / 6)
	prune := time.NewTicker(10 * time.Minute)
	var lastRetention time.Time
	defer hb.Stop()
	defer prune.Stop()
	for {
		select {
		case <-s.bg.Done():
			return
		case <-hb.C:
			s.hub.reapStale(s.cfg.HeartbeatTimeout)
		case <-prune.C:
			if err := s.st.PruneMetrics(s.bg, 7*24*time.Hour, 24*time.Hour); err != nil {
				s.log.Warn("prune metrics", "err", err)
			}
			s.st.ExpireConfirmations(s.bg, 24*time.Hour)
			if time.Since(lastRetention) > 6*time.Hour {
				lastRetention = time.Now()
				if n, err := s.st.PruneHistory(s.bg, time.Now(), *s.cfg.Retention); err != nil {
					s.log.Warn("prune history", "err", err)
				} else if n != (store.PruneResult{}) {
					s.log.Info("pruned history", "audit", n.Audit, "confirmations", n.Confirmations, "imports", n.Imports, "deployments", n.Deployments, "sessions", n.Sessions)
				}
			}
			s.pruneBlobs(24 * time.Hour)
			s.lock.prune()
			s.mfa.prune()
		}
	}
}

func (s *Server) pruneBlobs(age time.Duration) {
	dir := filepath.Join(s.cfg.DataDir, "blobs")
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > age {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// lockJob prevents two long operations on the same job/project at once.
func (s *Server) lockJob(id string) bool {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if s.jobBusy[id] {
		return false
	}
	s.jobBusy[id] = true
	return true
}

func (s *Server) unlockJob(id string) {
	s.jobMu.Lock()
	delete(s.jobBusy, id)
	s.jobMu.Unlock()
}

// publicURL is the base URL agents and the CLI should use.
func (s *Server) publicURL(ctx context.Context, r *http.Request) string {
	if v, _ := s.st.GetSetting(ctx, "public_url"); v != "" {
		return strings.TrimRight(v, "/")
	}
	if s.cfg.PublicURL != "" {
		return s.cfg.PublicURL
	}
	if r != nil {
		scheme := "http"
		if isTLS(r) {
			scheme = "https"
		}
		return scheme + "://" + r.Host
	}
	return "http://localhost:7300"
}

// dnsProvider builds the DNS provider from current settings.
func (s *Server) dnsProvider(ctx context.Context) dns.Provider {
	if s.cfg.DNSProvider != nil {
		return s.cfg.DNSProvider
	}
	tok, _ := s.st.GetSetting(ctx, "cloudflare_token")
	if tok == "" {
		return dns.Noop{}
	}
	zone, _ := s.st.GetSetting(ctx, "cloudflare_zone_id")
	base, _ := s.st.GetSetting(ctx, "base_domain")
	cf := &dns.Cloudflare{Token: tok, ZoneID: zone, BaseDomain: base, BaseURL: s.cfg.CloudflareBaseURL}
	if rs, err := s.st.ListRootDomains(ctx); err == nil {
		for _, r := range rs {
			id := r.ZoneID
			if r.Name == base && zone != "" {
				id = zone // the zone id in Settings keeps applying to the default root
			}
			cf.Roots = append(cf.Roots, dns.Root{Name: r.Name, ZoneID: id})
		}
	}
	return cf
}
