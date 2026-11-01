package server

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

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

	// auth
	open("GET "+v+"/auth/status", s.handleAuthStatus)
	open("POST "+v+"/auth/setup", s.handleSetup)
	open("POST "+v+"/auth/login", s.handleLogin)
	open("POST "+v+"/auth/logout", s.handleLogout)
	open("POST "+v+"/auth/login/2fa", s.handleLogin2FA)
	a("GET "+v+"/auth/me", needReadGlobal, s.handleMe)
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
	a("GET "+v+"/servers/{id}", needReadGlobal, s.handleGetServer)
	a("PATCH "+v+"/servers/{id}", needServers, s.handlePatchServer)
	a("DELETE "+v+"/servers/{id}", needServers, s.handleDeleteServer)
	a("GET "+v+"/servers/{id}/metrics", lvlRead, s.handleServerMetrics)
	a("GET "+v+"/servers/{id}/containers", lvlRead, s.handleServerContainers)
	a("GET "+v+"/servers/{id}/zeabur", lvlRead, s.handleZeaburScan)

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
	a("GET "+v+"/services/{id}/metrics", lvlRead, s.handleServiceMetrics)
	a("GET "+v+"/services/{id}/logs", lvlRead, s.handleLogs)
	a("POST "+v+"/services/{id}/exec", lvlDeploy, s.handleExec)

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

	// domains
	a("GET "+v+"/domains", needReadList, s.handleListAllDomains)
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
	a("PUT "+v+"/settings", needSettings, s.handlePutSettings)
	a("GET "+v+"/templates", needReadGlobal, s.handleTemplates)

	// imports
	a("GET "+v+"/imports", lvlRead, s.handleListImports)
	a("POST "+v+"/imports", needDeployGlobal, s.handleCreateImport)
	a("POST "+v+"/imports/zeabur-local", needDeployGlobal, s.handleZeaburLocalImport)
	a("GET "+v+"/imports/{id}", lvlRead, s.handleGetImport)
	a("PUT "+v+"/imports/{id}/volumes/{service_name}/{volume}", needDeployGlobal, s.handleImportVolume)
	a("POST "+v+"/imports/{id}/start", needDeployGlobal, s.handleStartImport)
	a("POST "+v+"/imports/{id}/rehearse", needDeployGlobal, s.handleRehearse)
	a("POST "+v+"/imports/{id}/cutover", needMigrate, s.handleCutover)
	a("POST "+v+"/imports/{id}/rollback", needMigrate, s.handleZeaburRollback)
	a("POST "+v+"/imports/{id}/cleanup", needMigrate, s.handleZeaburCleanup)

	// live events
	a("GET "+v+"/events", lvlRead, s.handleEvents)

	// unknown API paths answer JSON, not the SPA
	open(v+"/", func(w http.ResponseWriter, r *http.Request) error {
		return errf(404, "not_found", "no such endpoint: %s %s", r.Method, r.URL.Path)
	})

	// agent
	open("POST /agent/v1/join", s.handleAgentJoin)
	mux.HandleFunc("GET /agent/v1/connect", s.handleAgentConnect)
	open("PUT /agent/v1/blobs/{token}", s.handleBlobPut)
	open("GET /agent/v1/blobs/{token}", s.handleBlobGet)

	// web UI (SPA)
	mux.Handle("/", spaHandler(webui.FS()))
	return securityHeaders(mux)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/agent/") {
			w.Header().Set("X-Frame-Options", "DENY")
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
			if path.Ext(p) != "" && strings.HasPrefix(p, "assets/") {
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
