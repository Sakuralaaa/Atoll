// Package server is atoll-server: the control plane REST API, agent hub,
// DNS automation, migrations and imports. See docs/api.md.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/server/dns"
	"github.com/Sakuralaaa/Atoll/internal/server/secrets"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// Version is set at build time with -ldflags "-X github.com/Sakuralaaa/Atoll/internal/server.Version=v1.2.3".
var Version = "dev"

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
	Logger            *slog.Logger
}

// Server is the control plane.
type Server struct {
	cfg    Config
	st     *store.Store
	hub    *Hub
	events *eventBus
	log    *slog.Logger
	limit  *loginLimiter

	jobMu   sync.Mutex
	jobBusy map[string]bool

	upd *updater // image update detection (imageupdates.go)

	bg     context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
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
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
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
	s := &Server{cfg: cfg, st: st, log: cfg.Logger, events: newEventBus(), limit: newLoginLimiter(10, 15*time.Minute), jobBusy: map[string]bool{}}
	s.hub = newHub(s)
	s.upd = newUpdater()
	s.bg, s.cancel = context.WithCancel(context.Background())
	// Nobody is connected right after start.
	nodes, _ := st.ListNodes(s.bg)
	for _, n := range nodes {
		if n.Status == "online" {
			st.SetNodeStatus(s.bg, n.ID, "offline", false)
		}
	}
	s.wg.Add(1)
	go s.housekeeping()
	s.wg.Add(1)
	go s.imageUpdateLoop()
	s.startBackupScheduler() // backups.go
	return s, nil
}

// Close stops background work and closes the database.
func (s *Server) Close() error {
	s.cancel()
	s.hub.closeAll()
	s.wg.Wait()
	return s.st.Close()
}

// Store exposes the store (tests, main).
func (s *Server) Store() *store.Store { return s.st }

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
			s.pruneBlobs(24 * time.Hour)
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
	tok, _ := s.st.GetSetting(ctx, "cloudflare_token")
	if tok == "" {
		return dns.Noop{}
	}
	zone, _ := s.st.GetSetting(ctx, "cloudflare_zone_id")
	base, _ := s.st.GetSetting(ctx, "base_domain")
	return &dns.Cloudflare{Token: tok, ZoneID: zone, BaseDomain: base, BaseURL: s.cfg.CloudflareBaseURL}
}
