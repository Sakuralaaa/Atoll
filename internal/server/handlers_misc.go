package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/templates"
	"github.com/coder/websocket"
)

// ---------- confirmations ----------

func (s *Server) handleListConfirmations(w http.ResponseWriter, r *http.Request) error {
	s.st.ExpireConfirmations(r.Context(), 24*time.Hour)
	cs, err := s.st.ListConfirmations(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, cs)
	return nil
}

func (s *Server) handleDecideConfirmation(approve bool) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id := r.PathValue("id")
		if !s.lockJob(id) {
			return errf(409, "busy", "confirmation is being processed")
		}
		defer s.unlockJob(id)
		s.st.ExpireConfirmations(r.Context(), 24*time.Hour)
		c, payload, err := s.st.GetConfirmation(r.Context(), id)
		if err != nil {
			return err
		}
		if c.Status != "pending" {
			return errf(409, "not_pending", "confirmation is %s", c.Status)
		}
		actor := principalFrom(r.Context()).actor()
		if approve {
			if err := s.execConfirmed(r.Context(), r, c, payload, actor); err != nil {
				return err
			}
		}
		status := "rejected"
		if approve {
			status = "approved"
		}
		if _, err := s.st.DecideConfirmation(r.Context(), id, status, actor); err != nil {
			return err
		}
		c.Status = status
		s.st.Audit(r.Context(), actor, "confirmation."+status, id, map[string]string{"action": c.Action, "target": c.Target})
		s.publish("confirmation", c)
		writeJSON(w, 200, c)
		return nil
	}
}

func (s *Server) execConfirmed(ctx context.Context, r *http.Request, c *api.Confirmation, payload json.RawMessage, actor string) error {
	actor = actor + " (approving " + c.CreatedBy + ")"
	switch c.Action {
	case "service.delete":
		return s.execDeleteService(ctx, c.Target, actor)
	case "project.delete":
		return s.execDeleteProject(ctx, c.Target, actor)
	case "server.delete":
		return s.execDeleteServer(ctx, c.Target, actor)
	case "domain.add":
		var req domainRequest
		json.Unmarshal(payload, &req)
		_, err := s.execAddDomain(ctx, c.Target, req, actor)
		return err
	case "domain.delete":
		return s.execDeleteDomain(ctx, c.Target, actor)
	case "project.migrate":
		var req struct {
			ServerID string `json:"server_id"`
		}
		json.Unmarshal(payload, &req)
		_, err := s.startMigration(ctx, c.Target, req.ServerID, s.publicURL(ctx, r), actor)
		return err
	case "service.migrate":
		var req api.ServiceMigrateRequest
		json.Unmarshal(payload, &req)
		_, err := s.startServiceMigration(ctx, c.Target, req.ServerID, s.publicURL(ctx, r), actor)
		return err
	case "import.cutover":
		_, err := s.startCutover(ctx, c.Target, actor)
		return err
	case "backup.restore":
		var req api.BackupRestoreRequest
		json.Unmarshal(payload, &req)
		_, err := s.startRestore(ctx, c.Target, req, actor)
		return err
	}
	return errf(400, "unknown_action", "unknown confirmation action %q", c.Action)
}

// ---------- settings ----------

func (s *Server) settings(ctx context.Context) api.Settings {
	get := func(k string) string { v, _ := s.st.GetSetting(ctx, k); return v }
	st := api.Settings{
		BaseDomain:         get("base_domain"),
		CloudflareZoneID:   get("cloudflare_zone_id"),
		HasCloudflareToken: get("cloudflare_token") != "",
		TLSMode:            get("tls_mode"),
		PublicURL:          get("public_url"),
		HasOriginCert:      get("origin_cert_pem") != "" && get("origin_key_pem") != "",
		ACMEEmail:          get("acme_email"),
	}
	if st.TLSMode == "" {
		st.TLSMode = "cloudflare-origin"
	}
	if st.PublicURL == "" {
		st.PublicURL = s.cfg.PublicURL
	}
	st.ImageCheckIntervalHours = int(s.imageCheckInterval(ctx) / time.Hour)
	s.fillBackupSettings(ctx, &st)  // backups.go
	s.fillNetworkSettings(ctx, &st) // network.go
	return st
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, 200, s.settings(r.Context()))
	return nil
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) error {
	var req api.Settings
	if err := decode(r, &req); err != nil {
		return err
	}
	req.BaseDomain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(req.BaseDomain)), ".")
	if req.BaseDomain != "" && !hostnameRe.MatchString(req.BaseDomain) {
		return badRequest("invalid base_domain %q", req.BaseDomain)
	}
	if req.TLSMode == "" {
		req.TLSMode = "cloudflare-origin"
	}
	if req.TLSMode != "cloudflare-origin" && req.TLSMode != "acme" {
		return badRequest("tls_mode must be cloudflare-origin or acme")
	}
	req.PublicURL = strings.TrimRight(strings.TrimSpace(req.PublicURL), "/")
	if req.PublicURL != "" && !strings.HasPrefix(req.PublicURL, "http://") && !strings.HasPrefix(req.PublicURL, "https://") {
		return badRequest("public_url must start with http:// or https://")
	}
	if (req.OriginCertPEM == "") != (req.OriginKeyPEM == "") {
		return badRequest("origin_cert_pem and origin_key_pem must be set together")
	}
	if req.OriginCertPEM != "" && (!strings.Contains(req.OriginCertPEM, "BEGIN CERTIFICATE") || !strings.Contains(req.OriginKeyPEM, "PRIVATE KEY")) {
		return badRequest("origin certificate/key must be PEM encoded")
	}
	ctx := r.Context()
	set := map[string]string{
		"base_domain": req.BaseDomain, "cloudflare_zone_id": strings.TrimSpace(req.CloudflareZoneID),
		"tls_mode": req.TLSMode, "public_url": req.PublicURL, "acme_email": strings.TrimSpace(req.ACMEEmail),
	}
	if req.CloudflareToken != "" { // "" keeps the stored token
		set["cloudflare_token"] = strings.TrimSpace(req.CloudflareToken)
	}
	if req.ImageCheckIntervalHours < 0 || req.ImageCheckIntervalHours > 24*7 {
		return badRequest("image_check_interval_hours must be between 1 and 168")
	}
	if req.ImageCheckIntervalHours > 0 { // 0 keeps the stored value
		set["image_check_interval_hours"] = intStr(req.ImageCheckIntervalHours)
	}
	if req.OriginCertPEM != "" {
		set["origin_cert_pem"] = req.OriginCertPEM
		set["origin_key_pem"] = req.OriginKeyPEM
	}
	generatedBackupPassword, backupChanged, err := s.applyBackupSettings(ctx, req) // backups.go
	if err != nil {
		return err
	}
	networkChanged, err := s.applyNetworkSettings(ctx, req) // network.go
	if err != nil {
		return err
	}
	for k, v := range set {
		if err := s.st.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	s.st.Audit(ctx, principalFrom(ctx).actor(), "settings.update", "", nil)
	// push TLS + ingress to every online node
	nodes, _ := s.st.ListNodes(ctx)
	tls := s.tlsPayload(ctx)
	for _, n := range nodes {
		if tls != nil && req.OriginCertPEM != "" {
			s.hub.Fire(n.ID, api.MsgTLS, tls)
		}
		s.pushIngress(ctx, n.ID)
	}
	if backupChanged {
		s.pushBackupConfigAll(ctx, true)
	}
	if networkChanged {
		s.goBG(func(bg context.Context) { s.pushMeshAll(bg) })
	}
	out := s.settings(ctx)
	out.BackupRepoPassword = generatedBackupPassword // shown once
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, 200, templates.List())
	return nil
}

// ---------- logs & exec ----------

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	nodeID, err := s.serviceNode(r.Context(), svc)
	if err != nil {
		return err
	}
	if !s.hub.Online(nodeID) {
		return errf(409, "server_offline", "%v", ErrNodeOffline)
	}
	tail := 200
	if t := r.URL.Query().Get("tail"); t != "" {
		if n, err := parseInt(t); err == nil && n >= 0 && n <= 10000 {
			tail = n
		}
	}
	follow := r.URL.Query().Get("follow") != "0"
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: originPatterns(r)})
	if err != nil {
		return nil
	}
	defer c.CloseNow()
	ctx := c.CloseRead(r.Context())
	streamID := randString(16, idAlphabet)
	lines, closeStream := s.hub.openStream(nodeID, streamID)
	defer closeStream()
	open := api.LogsOpenPayload{StreamID: streamID, ServiceID: svc.ID, Tail: tail, Follow: follow}
	resCh := make(chan error, 1)
	go func() {
		_, err := s.hub.Call(ctx, nodeID, api.MsgLogsOpen, open, 0)
		resCh <- err
	}()
	defer s.hub.Fire(nodeID, api.MsgLogsClose, api.LogsOpenPayload{StreamID: streamID, ServiceID: svc.ID})
	var idle <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-resCh:
			if err != nil {
				c.Close(websocket.StatusInternalError, truncateReason(err.Error()))
				return nil
			}
			if !follow {
				// tail delivered before the result; allow stragglers then close
				idle = time.After(500 * time.Millisecond)
			}
			resCh = nil
		case <-idle:
			c.Close(websocket.StatusNormalClosure, "end of logs")
			return nil
		case l, ok := <-lines:
			if !ok {
				c.Close(websocket.StatusGoingAway, "agent disconnected")
				return nil
			}
			if l.ServiceID == "" {
				l.ServiceID = svc.ID
			}
			raw, _ := json.Marshal(l)
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Write(wctx, websocket.MessageText, raw)
			cancel()
			if err != nil {
				return nil
			}
		}
	}
}

func truncateReason(s string) string {
	if len(s) > 120 {
		return s[:120]
	}
	return s
}

func parseInt(s string) (int, error) {
	var n int
	err := json.Unmarshal([]byte(s), &n)
	return n, err
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var req struct {
		Cmd        []string `json:"cmd"`
		TimeoutSec int      `json:"timeout_sec"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if len(req.Cmd) == 0 {
		return badRequest("cmd required, e.g. [\"sh\",\"-c\",\"ls /\"]")
	}
	if req.TimeoutSec <= 0 || req.TimeoutSec > 600 {
		req.TimeoutSec = 60
	}
	nodeID, err := s.serviceNode(r.Context(), svc)
	if err != nil {
		return err
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "service.exec", svc.ID, req.Cmd)
	res, err := s.hub.Request(r.Context(), nodeID, api.MsgExec, api.ExecPayload{ServiceID: svc.ID, Cmd: req.Cmd, TimeoutSec: req.TimeoutSec},
		time.Duration(req.TimeoutSec+10)*time.Second)
	if err != nil {
		if err == ErrNodeOffline {
			return errf(409, "server_offline", "%v", err)
		}
		return errf(502, "agent_error", "%v", err)
	}
	writeJSON(w, 200, res)
	return nil
}
