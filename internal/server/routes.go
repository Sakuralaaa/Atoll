package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/server/webui"
)

// Handler returns the full HTTP handler: REST API, agent endpoints, downloads and the web UI.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	a := func(pattern string, need access, fn handlerFunc) { mux.Handle(pattern, s.authed(need, fn)) }
	open := func(pattern string, fn handlerFunc) { mux.Handle(pattern, s.wrap(fn)) }
	const v = "/api/v1"

	// health & downloads
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /downloads/{file}", s.handleDownload)
	mux.HandleFunc("GET /install-agent.sh", agentScript)
	mux.HandleFunc("GET /install-server.sh", serverScript)

	// auth
	open("GET "+v+"/auth/status", s.handleAuthStatus)
	open("POST "+v+"/auth/setup", s.handleSetup)
	open("POST "+v+"/auth/login", s.handleLogin)
	open("POST "+v+"/auth/logout", s.handleLogout)
	open("POST "+v+"/auth/login/2fa", s.handleLogin2FA)
	a("GET "+v+"/auth/me", needReadGlobal, s.handleMe)
	a("POST "+v+"/auth/password", lvlSession, s.handleChangePassword)
	a("POST "+v+"/auth/username", lvlSession, s.handleChangeUsername)
	a("GET "+v+"/auth/2fa", lvlSession, s.handle2FAStatus)
	a("POST "+v+"/auth/2fa/enroll", lvlSession, s.handle2FAEnroll)
	a("POST "+v+"/auth/2fa/enable", lvlSession, s.handle2FAEnable)
	a("POST "+v+"/auth/2fa/disable", lvlSession, s.handle2FADisable)
	a("POST "+v+"/auth/2fa/recovery-codes", lvlSession, s.handle2FARecoveryCodes)
	a("GET "+v+"/auth/sessions", lvlSession, s.handleListSessions)
	a("DELETE "+v+"/auth/sessions/{id}", lvlSession, s.handleDeleteSession)
	a("POST "+v+"/auth/sessions/revoke-others", lvlSession, s.handleRevokeOtherSessions)

	// servers
	a("GET "+v+"/servers", needReadGlobal, s.handleListServers)
	a("POST "+v+"/servers/join-token", needServers, s.handleJoinToken)
	a("GET "+v+"/servers/agent-install-command", needReadGlobal, s.handleAgentInstallCommand)
	a("GET "+v+"/servers/{id}", needReadGlobal, s.handleGetServer)
	a("POST "+v+"/servers/agent-upgrade", needServers, s.handleAgentUpgradeBatch)
	a("POST "+v+"/servers/{id}/agent-upgrade", needServers, s.handleAgentUpgrade)
	a("PATCH "+v+"/servers/{id}", needServers, s.handlePatchServer)
	a("DELETE "+v+"/servers/{id}", needServers, s.handleDeleteServer)
	a("GET "+v+"/servers/{id}/metrics", lvlRead, s.handleServerMetrics)
	a("GET "+v+"/servers/{id}/containers", lvlRead, s.handleServerContainers)
	a("GET "+v+"/servers/{id}/zeabur", lvlRead, s.handleZeaburScan)
	a("GET "+v+"/servers/{id}/docker", lvlRead, s.handleDockerScan)

	// projects
	a("GET "+v+"/projects", needReadList, s.handleListProjects)
	a("POST "+v+"/projects", needDeployGlobal, s.handleCreateProject)
	a("GET "+v+"/projects/{id}", lvlRead, s.handleGetProject)
	a("PATCH "+v+"/projects/{id}", lvlDeploy, s.handlePatchProject)
	a("DELETE "+v+"/projects/{id}", needDelete, s.handleDeleteProject)
	a("GET "+v+"/projects/{id}/services", lvlRead, s.handleListServices)
	a("POST "+v+"/projects/{id}/services", lvlDeploy, s.handleCreateService)
	a("POST "+v+"/projects/{id}/migrate", needMigrate, s.handleMigrate)
	a("GET "+v+"/migrations", lvlRead, s.handleListMigrations)
	a("GET "+v+"/migrations/{id}", lvlRead, s.handleGetMigration)

	// services
	a("GET "+v+"/services/{id}", lvlRead, s.handleGetService)
	a("PATCH "+v+"/services/{id}", lvlDeploy, s.handlePatchService)
	a("DELETE "+v+"/services/{id}", needDelete, s.handleDeleteService)
	a("POST "+v+"/services/{id}/actions", lvlDeploy, s.handleServiceAction)
	a("POST "+v+"/services/{id}/rollback", lvlDeploy, s.handleRollback)
	a("GET "+v+"/services/{id}/env", lvlRead, s.handleGetEnv)
	a("PUT "+v+"/services/{id}/env", lvlDeploy, s.handlePutEnv)
	a("GET "+v+"/services/{id}/env/references", lvlRead, s.handleEnvReferences)
	a("GET "+v+"/services/{id}/domains", lvlRead, s.handleListDomains)
	a("POST "+v+"/services/{id}/domains", needDomains, s.handleAddDomain)
	a("GET "+v+"/services/{id}/deployments", lvlRead, s.handleListDeployments)
	a("GET "+v+"/services/{id}/deployments/{dep}", lvlRead, s.handleGetDeployment)
	a("GET "+v+"/services/{id}/deployments/{dep}/logs", lvlRead, s.handleDeploymentLogs)
	a("GET "+v+"/services/{id}/webhook", lvlRead, s.handleGetWebhook)
	a("POST "+v+"/services/{id}/webhook/rotate", lvlSession, s.handleRotateWebhook)
	a("GET "+v+"/services/{id}/metrics", lvlRead, s.handleServiceMetrics)
	a("GET "+v+"/services/{id}/logs", lvlRead, s.handleLogs)
	a("POST "+v+"/services/{id}/exec", needExec, s.handleExec)

	// image updates (imageupdates.go)
	a("GET "+v+"/image-updates", needReadList, s.handleListImageUpdates)
	a("GET "+v+"/services/{id}/image-update", lvlRead, s.handleGetImageUpdate)
	a("POST "+v+"/services/{id}/image-update/check", lvlDeploy, s.handleCheckImageUpdate)
	a("PUT "+v+"/services/{id}/image-update/policy", lvlDeploy, s.handlePutImageUpdatePolicy)
	a("POST "+v+"/services/{id}/image-update/apply", lvlDeploy, s.handleApplyImageUpdate)

	// backups (backups.go)
	a("GET "+v+"/services/{id}/backup-policy", lvlRead, s.handleGetBackupPolicy)
	a("PUT "+v+"/services/{id}/backup-policy", lvlDeploy, s.handlePutBackupPolicy)
	a("GET "+v+"/services/{id}/backups", lvlRead, s.handleListBackups)
	a("POST "+v+"/services/{id}/backups", lvlDeploy, s.handleRunBackup)
	a("GET "+v+"/services/{id}/restores", lvlRead, s.handleListRestores)
	a("GET "+v+"/backups/{id}", lvlRead, s.handleGetBackup)
	a("POST "+v+"/backups/{id}/restore", needRestore, s.handleRestoreBackup)
	a("GET "+v+"/restores/{id}", lvlRead, s.handleGetRestore)
	a("POST "+v+"/settings/backup/test", needSettings, s.handleBackupTest)
	a("GET "+v+"/settings/backup/password", lvlSession, s.handleBackupPassword)
	a("DELETE "+v+"/settings/backup", needSettings, s.handleDeleteBackupSettings)
	// backup verification (backups_verify.go)
	a("GET "+v+"/backup-verify", needReadGlobal, s.handleBackupVerifyStatus)
	a("POST "+v+"/backup-verify/check", needDeployGlobal, s.handleRunCheck)
	a("GET "+v+"/backup-verifications", needReadList, s.handleListVerifications)
	a("GET "+v+"/services/{id}/backup-verify", lvlRead, s.handleServiceVerifyStatus)
	a("POST "+v+"/services/{id}/backup-drill", lvlDeploy, s.handleRunDrill)

	// domains
	a("GET "+v+"/domains", needReadList, s.handleListAllDomains)
	// root domains (multi-domain, rootdomains.go)
	a("GET "+v+"/root-domains", needReadGlobal, s.handleListRootDomains)
	a("POST "+v+"/root-domains", needSettings, s.handleCreateRootDomain)
	a("PATCH "+v+"/root-domains/{name}", needSettings, s.handlePatchRootDomain)
	a("POST "+v+"/root-domains/{name}/refresh", needSettings, s.handleRefreshRootDomain)
	a("DELETE "+v+"/root-domains/{name}", needSettings, s.handleDeleteRootDomain)
	a("DELETE "+v+"/domains/{id}", needDomains, s.handleDeleteDomain)

	// tokens
	a("GET "+v+"/tokens", needTokens, s.handleListTokens)
	a("POST "+v+"/tokens", needTokens, s.handleCreateToken)
	a("PATCH "+v+"/tokens/{id}", needTokens, s.handlePatchToken)
	a("DELETE "+v+"/tokens/{id}", needTokens, s.handleDeleteToken)

	// confirmations
	a("GET "+v+"/confirmations", lvlRead, s.handleListConfirmations)
	a("POST "+v+"/confirmations/{id}/approve", lvlSession, s.handleDecideConfirmation(true))
	a("POST "+v+"/confirmations/{id}/reject", lvlSession, s.handleDecideConfirmation(false))

	// settings & templates
	a("GET "+v+"/settings", needReadGlobal, s.handleGetSettings)
	a("GET "+v+"/settings/notifications", needSettings, s.handleGetNotifications)
	a("PUT "+v+"/settings/notifications", needSettings, s.handlePutNotifications)
	a("POST "+v+"/settings/notifications/test", needSettings, s.handleTestNotification)
	a("GET "+v+"/problems", lvlRead, s.handleListProblems)
	a("PUT "+v+"/settings", needSettings, s.handlePutSettings)
	a("GET "+v+"/templates", needReadGlobal, s.handleTemplates)

	// imports
	a("GET "+v+"/imports", lvlRead, s.handleListImports)
	a("POST "+v+"/imports", needDeployGlobal, s.handleCreateImport)
	a("POST "+v+"/imports/zeabur-local", needDeployGlobal, s.handleZeaburLocalImport)
	a("POST "+v+"/imports/docker", needDeployGlobal, s.handleDockerAdopt)
	a("GET "+v+"/imports/{id}", lvlRead, s.handleGetImport)
	a("PUT "+v+"/imports/{id}/volumes/{service_name}/{volume}", needDeployGlobal, s.handleImportVolume)
	a("POST "+v+"/imports/{id}/start", needDeployGlobal, s.handleStartImport)
	a("POST "+v+"/imports/{id}/rehearse", needDeployGlobal, s.handleRehearse)
	a("POST "+v+"/imports/{id}/cutover", needMigrate, s.handleCutover)
	a("POST "+v+"/imports/{id}/rollback", needMigrate, s.handleZeaburRollback)
	a("POST "+v+"/servers/{id}/zeabur/cutover", needMigrate, s.handleNodeCutover)
	a("POST "+v+"/servers/{id}/zeabur/rollback", needMigrate, s.handleNodeRollback)
	a("POST "+v+"/imports/{id}/cleanup", needMigrate, s.handleZeaburCleanup)

	// builds from source (git.go): GitHub push webhook, verified by HMAC instead of a login
	open("POST "+v+"/hooks/github/{service_id}", s.handleGitHubWebhook)

	// live events
	a("GET "+v+"/events", lvlRead, s.handleEvents)

	// 中控 / control server itself (control*.go)
	s.registerControlRoutes(a)

	// unknown API paths answer JSON, not the SPA
	open(v+"/", func(w http.ResponseWriter, r *http.Request) error {
		return errf(404, "not_found", "no such endpoint: %s %s", r.Method, r.URL.Path)
	})

	// private network (network.go): server network actions, service migrate, bootstrap/ping
	s.registerNetworkRoutes(mux, a, open)

	// agent
	open("POST /agent/v1/join", s.handleAgentJoin)
	mux.HandleFunc("GET /agent/v1/connect", s.handleAgentConnect)
	open("PUT /agent/v1/blobs/{token}", s.handleBlobPut)
	open("GET /agent/v1/blobs/{token}", s.handleBlobGet)

	// unknown API and agent paths are JSON 404s, never the panel's HTML
	notFound := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, map[string]string{"error": "not found", "code": "not_found"})
	}
	mux.HandleFunc("/api/", notFound)
	mux.HandleFunc("/agent/", notFound)

	// web UI (SPA)
	mux.Handle("/", spaHandler(webui.FS()))
	return securityHeaders(s.agentGate(mux))
}

// panelCSP locks the embedded SPA to its own origin. The bundle has no inline scripts;
// inline style attributes (React style props) still need 'unsafe-inline' for styles.
const panelCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; " +
	"object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/agent/") {
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Content-Security-Policy", panelCSP)
		}
		if isTLS(r) {
			// The panel holds root on every server: once reached over HTTPS, never downgrade.
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DB.PingContext(r.Context()); err != nil {
		writeJSON(w, 503, map[string]string{"status": "error", "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok", "version": Version})
}

// agentSumCache remembers the SHA-256 of each hosted agent binary by size and mtime.
type agentSumCache struct {
	mu sync.Mutex
	m  map[string]agentSum
}

type agentSum struct {
	size  int64
	mtime time.Time
	hex   string
}

// hostedAgentSum returns the SHA-256 of <data>/downloads/atoll-agent-linux-<arch>, or "" when
// this server hosts no such binary.
func (s *Server) hostedAgentSum(arch string) string {
	p := filepath.Join(s.cfg.DataDir, "downloads", "atoll-agent-linux-"+arch)
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return ""
	}
	c := &s.agentSums
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[arch]; ok && e.size == st.Size() && e.mtime.Equal(st.ModTime()) {
		return e.hex
	}
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	if c.m == nil {
		c.m = map[string]agentSum{}
	}
	sum := hex.EncodeToString(h.Sum(nil))
	c.m[arch] = agentSum{size: st.Size(), mtime: st.ModTime(), hex: sum}
	return sum
}

// handleDownload serves agent binaries placed in <data>/downloads (atoll-agent-linux-amd64|arm64).
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	switch name {
	case "atoll-agent-linux-amd64", "atoll-agent-linux-arm64":
	default:
		writeJSON(w, 404, map[string]string{"error": "not found", "code": "not_found"})
		return
	}
	p := filepath.Join(s.cfg.DataDir, "downloads", name)
	f, err := os.Open(p)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": name + " is not hosted on this server; download it from GitHub releases", "code": "not_found"})
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		writeJSON(w, 404, map[string]string{"error": "not found", "code": "not_found"})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeContent(w, r, name, st.ModTime(), f)
}

// spaHandler serves the embedded web app; unknown paths get index.html so client-side
// routes work on reload. Hashed assets are cached for a year, index.html never.
func spaHandler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeJSON(w, 405, map[string]string{"error": "method not allowed"})
			return
		}
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(fsys, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			// The panel routes by URL hash, so any other path that names a file (a script,
			// a binary, an old asset) is a real 404: answering it with index.html and 200
			// would let `curl …/x.sh | sh` run HTML.
			if path.Ext(p) != "" || strings.HasPrefix(p, "assets/") {
				http.NotFound(w, r)
				return
			}
		}
		b, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			http.Error(w, "web UI not built", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	})
}
