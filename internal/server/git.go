package server

// Builds from source (docs/api.md "Builds from source"). A service with a git source has no
// registry image: every build runs on the service's own server (never on the control
// server), which clones the repository, runs `docker build` and deploys the local image
// through the normal apply path. The deployment is "building" while that runs; its output
// is stored per deployment (GET /services/{id}/deployments/{dep}/logs).

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// BuildImageRepo prefixes every image built from source (atoll-build/<service_id>:<commit12>).
const BuildImageRepo = "atoll-build/"

var (
	ghPartRe    = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
	branchRe    = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	buildArgRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	commitShaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// normalizeRepo accepts https://github.com/OWNER/REPO(.git)(/) and returns https://github.com/OWNER/REPO.
func normalizeRepo(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") {
		return "", badRequest("git repo must be a GitHub https URL like https://github.com/OWNER/REPO (got %q)", raw)
	}
	if u.User != nil {
		return "", badRequest("do not put credentials in the repo URL; give a GitHub token in git.token (stored encrypted)")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", badRequest("git repo URL must not have a query or fragment")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return "", badRequest("git repo must be https://github.com/OWNER/REPO (got %q)", raw)
	}
	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
	if !ghPartRe.MatchString(owner) || !ghPartRe.MatchString(repo) || repo == "." || repo == ".." {
		return "", badRequest("git repo must be https://github.com/OWNER/REPO (got %q)", raw)
	}
	return "https://github.com/" + owner + "/" + repo, nil
}

// cleanRelPath validates a path inside the repository ("" stays "").
func cleanRelPath(field, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || p == "." || p == "./" {
		return "", nil
	}
	if strings.ContainsAny(p, "\\\x00\n\r") || path.IsAbs(p) {
		return "", badRequest("git.%s must be a relative path inside the repository", field)
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", badRequest("git.%s must stay inside the repository", field)
	}
	return c, nil
}

// validateGitSource normalizes g in place (defaults, cleaned paths).
func validateGitSource(g *api.GitSource) error {
	u, err := normalizeGitSourceURL(g) // git_source.go
	if err != nil {
		return err
	}
	g.Branch = strings.TrimSpace(g.Branch)
	if g.Branch == "" {
		g.Branch = "main"
	}
	g.Branch = strings.TrimPrefix(g.Branch, "refs/heads/")
	if !branchRe.MatchString(g.Branch) || strings.Contains(g.Branch, "..") || strings.HasPrefix(g.Branch, "-") ||
		strings.HasPrefix(g.Branch, "/") || strings.HasSuffix(g.Branch, "/") || strings.HasSuffix(g.Branch, ".lock") {
		return badRequest("git.branch %q is not a valid branch name", g.Branch)
	}
	if g.Context, err = cleanRelPath("context", g.Context); err != nil {
		return err
	}
	if g.Dockerfile, err = cleanRelPath("dockerfile", g.Dockerfile); err != nil {
		return err
	}
	if g.Dockerfile == "" {
		g.Dockerfile = "Dockerfile"
	}
	if len(g.BuildArgs) > 100 {
		return badRequest("at most 100 build args")
	}
	for k, v := range g.BuildArgs {
		if !buildArgRe.MatchString(k) {
			return badRequest("build arg name %q must match %s", k, buildArgRe)
		}
		if strings.ContainsRune(v, 0) || len(v) > 4096 {
			return badRequest("build arg %s: value too long or contains NUL", k)
		}
	}
	g.Token = strings.TrimSpace(g.Token)
	if len(g.Token) > 512 || strings.ContainsAny(g.Token, " \t\r\n") {
		return badRequest("git.token is not a valid access token")
	}
	g.HasToken, g.LastCommit = false, ""
	return validateGitExtras(g, u)
}

// secretUpdate turns a write-only field and its clear flag into a PutServiceGit argument.
func secretUpdate(v string, clear bool) *string {
	switch {
	case v != "":
		return &v
	case clear:
		e := ""
		return &e
	}
	return nil
}

func newWebhookSecret() string { return randString(40, tokenAlphabet) }

func (s *Server) webhookInfo(ctx context.Context, r *http.Request, svcID string, g *store.GitRow, withSecret bool) api.GitWebhook {
	kind := g.Provider
	if kind != "gitlab" && kind != "gitea" {
		kind = "github" // GitHub, and any server that signs like it (X-Hub-Signature-256)
	}
	w := api.GitWebhook{
		URL:         s.publicURL(ctx, r) + "/api/v1/hooks/" + kind + "/" + svcID,
		Provider:    g.Provider,
		HasSecret:   g.WebhookSecret != "",
		ContentType: "application/json",
		Events:      "push",
		Branch:      g.Branch,
	}
	if withSecret {
		w.Secret = g.WebhookSecret
	}
	return w
}

// saveGitSource stores a validated source of a service; first-time sources get a webhook
// secret, returned so the caller can show it once.
func (s *Server) saveGitSource(ctx context.Context, svcID string, g api.GitSource, isNew bool) (secret string, err error) {
	if isNew {
		secret = newWebhookSecret()
	}
	sec := store.GitSecrets{Token: secretUpdate(g.Token, g.ClearToken), SSHKey: secretUpdate(g.SSHKey, g.ClearSSHKey)}
	e := ""
	if isNew && sec.Token == nil {
		sec.Token = &e
	}
	if isNew && sec.SSHKey == nil {
		sec.SSHKey = &e
	}
	g.Token, g.ClearToken, g.SSHKey, g.ClearSSHKey = "", false, "", false
	return secret, s.st.PutServiceGit(ctx, svcID, g, sec, secret)
}

// ---------- deploy integration ----------

// userBuildTrigger: a deploy a user (or a push) asked for builds the branch head (or the
// pushed commit). Other deploys (env change, migration, restore...) reuse the last image when
// it is on the target server, and otherwise rebuild the commit that was running.
func userBuildTrigger(trigger string) bool {
	return trigger == "create" || trigger == "redeploy" || trigger == "webhook" || trigger == "poll"
}

// prepareBuild decides how a git service's deployment gets its image. It either fills spec
// with the last built image (no build) or returns the build to run first (spec.Image empty
// until the build finished).
func (s *Server) prepareBuild(ctx context.Context, svc *api.Service, spec *api.ResolvedSpec, node, trigger, commit string) (*api.BuildPayload, error) {
	g, err := s.st.GetServiceGit(ctx, svc.ID)
	if err != nil {
		return nil, err
	}
	spec.LocalImage = true
	spec.RegistryAuth = nil
	if commit == "" && !userBuildTrigger(trigger) {
		if g.LastImage != "" && g.LastNode == node {
			spec.Image = g.LastImage
			return nil, nil
		}
		commit = g.LastCommit // rebuild what ran, not whatever the branch has now
	}
	spec.Image = ""
	timeout := int(s.cfg.BuildTimeout / time.Second)
	b := &api.BuildPayload{
		DeploymentID: spec.DeploymentID, ServiceID: svc.ID,
		Repo: g.Repo, Branch: g.Branch, Commit: commit, Context: g.Context, Dockerfile: g.Dockerfile,
		BuildArgs: g.BuildArgs, Token: g.Token,
		TimeoutSec: timeout, QueueSec: timeout, Keep: s.cfg.BuildKeep,
	}
	if s.agentBuildSources(node) { // git_source.go; older agents get the payload they know
		fillBuildSources(b, g, firstPort(*spec))
	} else if gitNeedsSources(g.GitSource, g.HasSSHKey) {
		b.Provider = g.Provider // marks the payload for runBuild's agent check
	}
	return b, nil
}

// runBuild runs a deployment's build on its server and returns the spec completed with the
// built image. On failure the deployment is failed and ok is false.
func (s *Server) runBuild(ctx context.Context, p pendingApply) (api.ResolvedSpec, bool) {
	b := p.build
	what := b.Repo + "@" + b.Branch
	if b.Commit != "" {
		what = b.Repo + "@" + shortSHA(b.Commit)
	}
	s.st.UpdateDeployment(ctx, p.depID, "building", "building "+what, "")
	s.st.SetServiceStatus(ctx, p.serviceID, "deploying", "")
	if d, err := s.st.GetDeployment(ctx, p.depID); err == nil {
		s.publish("deployment", d)
	}
	s.publishService(ctx, p.serviceID)
	s.appendBuildLog(ctx, p.depID, p.serviceID, []string{"atoll: building " + what + " on " + p.nodeID})

	wait := time.Duration(b.TimeoutSec+b.QueueSec)*time.Second + 2*time.Minute
	var res api.ResultPayload
	var err error
	msg := ""
	if b.Builder == "" && b.Provider != "" { // the source needs a newer agent (git_source.go)
		if g, gerr := s.st.GetServiceGit(ctx, p.serviceID); gerr == nil && s.agentBuildSources(p.nodeID) {
			fillBuildSources(b, g, firstPort(p.spec))
		} else {
			msg = agentTooOldBuildMsg
		}
	}
	if msg == "" {
		res, err = s.hub.Request(ctx, p.nodeID, api.MsgBuild, *b, wait)
	}
	switch {
	case msg != "":
	case err != nil:
		msg = err.Error()
	case !res.OK:
		msg = res.Error
		if strings.Contains(msg, "unsupported message type") {
			msg = "this server's atoll-agent is too old to build from source; upgrade it"
		} else {
			msg = agentUpgradeHint(msg)
		}
	}
	var out api.BuildResult
	if msg == "" {
		if json.Unmarshal([]byte(res.Output), &out) != nil || out.Image == "" {
			msg = "agent returned no image"
		}
	}
	if msg != "" {
		s.appendBuildLog(ctx, p.depID, p.serviceID, []string{"atoll: build failed: " + msg})
		s.failDeployment(ctx, p.depID, p.serviceID, "build failed: "+msg)
		return api.ResolvedSpec{}, false
	}
	spec := p.spec
	spec.Image = out.Image
	spec.LocalImage = true
	if err := s.st.SetDeploymentBuild(ctx, p.depID, spec, out.Commit); err != nil {
		s.failDeployment(ctx, p.depID, p.serviceID, "store build result: "+err.Error())
		return api.ResolvedSpec{}, false
	}
	s.st.SetGitBuilt(ctx, p.serviceID, out.Commit, out.Image, p.nodeID)
	if b.Builder != "" { // new agents report how they built it (nil = the repo's Dockerfile)
		s.st.SetGitDetected(ctx, p.serviceID, out.Detected)
	}
	s.st.SetServiceImage(ctx, p.serviceID, out.Image)
	s.st.PruneDeploymentLogs(ctx, p.serviceID, 10)
	s.appendBuildLog(ctx, p.depID, p.serviceID, []string{"atoll: built " + out.Image + " (commit " + out.Commit + "), deploying"})
	if d, err := s.st.GetDeployment(ctx, p.depID); err == nil {
		s.publish("deployment", d)
	}
	return spec, true
}

func shortSHA(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// appendBuildLog stores build output lines and publishes them on /events ("build_log").
func (s *Server) appendBuildLog(ctx context.Context, depID, serviceID string, lines []string) {
	if len(lines) == 0 {
		return
	}
	if err := s.st.AppendDeploymentLog(ctx, depID, lines); err != nil {
		s.log.Warn("store build log", "deployment", depID, "err", err)
		return
	}
	s.publish("build_log", api.BuildLogEvent{DeploymentID: depID, ServiceID: serviceID, Lines: lines})
}

// handleBuildLog stores a MsgBuildLog frame from an agent (only for its own deployments).
func (s *Server) handleBuildLog(ctx context.Context, nodeID string, ev api.BuildLogEvent) {
	d, err := s.st.GetDeployment(ctx, ev.DeploymentID)
	if err != nil || d.ServerID != nodeID || d.ServiceID != ev.ServiceID {
		return
	}
	const maxLine = 4000
	for i, l := range ev.Lines {
		if len(l) > maxLine {
			ev.Lines[i] = l[:maxLine] + "…"
		}
	}
	s.appendBuildLog(ctx, d.ID, d.ServiceID, ev.Lines)
}

// ---------- HTTP ----------

// GET /services/{id}/deployments/{dep}/logs
func (s *Server) handleDeploymentLogs(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	d, err := s.st.GetDeployment(r.Context(), r.PathValue("dep"))
	if err != nil || d.ServiceID != svc.ID {
		return errf(404, "not_found", "deployment %q not found on this service", r.PathValue("dep"))
	}
	var after int64
	if v := r.URL.Query().Get("after"); v != "" {
		if after, err = strconv.ParseInt(v, 10, 64); err != nil || after < 0 {
			return badRequest("after must be a line sequence number")
		}
	}
	lines, trunc, err := s.st.DeploymentLogs(r.Context(), d.ID, after)
	if err != nil {
		return err
	}
	writeJSON(w, 200, api.DeploymentLogs{DeploymentID: d.ID, Status: d.Status, Lines: lines, Truncated: trunc})
	return nil
}

// GET /services/{id}/webhook: URL for anyone who can read the service; the secret only for
// a panel session that may deploy (the secret starts builds; a read-only member gets none).
func (s *Server) handleGetWebhook(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	g, err := s.st.GetServiceGit(r.Context(), svc.ID)
	if err == store.ErrNotFound {
		return errf(409, "not_git", "service %s is not built from git (it has no webhook)", svc.Name)
	}
	if err != nil {
		return err
	}
	p := principalFrom(r.Context())
	writeJSON(w, 200, s.webhookInfo(r.Context(), r, svc.ID, g, p.Session && (!p.Member || p.Perms[api.PermDeploy])))
	return nil
}

// POST /services/{id}/webhook/rotate (session only): new secret, returned once.
func (s *Server) handleRotateWebhook(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	g, err := s.st.GetServiceGit(r.Context(), svc.ID)
	if err == store.ErrNotFound {
		return errf(409, "not_git", "service %s is not built from git (it has no webhook)", svc.Name)
	}
	if err != nil {
		return err
	}
	g.WebhookSecret = newWebhookSecret()
	if err := s.st.SetWebhookSecret(r.Context(), svc.ID, g.WebhookSecret); err != nil {
		return err
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "service.webhook_rotate", svc.ID, nil)
	writeJSON(w, 200, s.webhookInfo(r.Context(), r, svc.ID, g, true))
	return nil
}

// verifyGitHubSignature checks X-Hub-Signature-256 ("sha256=<hex HMAC-SHA256 of the body>").
func verifyGitHubSignature(secret string, body []byte, header string) bool {
	if secret == "" || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// githubPush is the part of a GitHub push event we use.
type githubPush struct {
	Ref     string `json:"ref"`
	After   string `json:"after"`
	Deleted bool   `json:"deleted"`
}

// POST /api/v1/hooks/github/{service_id}: unauthenticated, verified by HMAC. Push events on
// the configured branch build and deploy the pushed commit; everything else is acknowledged
// and ignored.
func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20+1))
	if err != nil {
		return badRequest("read body: %v", err)
	}
	if len(body) > 10<<20 {
		return errf(413, "too_large", "payload too large")
	}
	svcID := r.PathValue("service_id")
	g, err := s.st.GetServiceGit(ctx, svcID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errf(404, "not_found", "no git service %q", svcID)
		}
		return err
	}
	if !verifyGitHubSignature(g.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		s.log.Warn("github webhook: bad signature", "service", svcID, "remote", r.RemoteAddr)
		return errf(401, "bad_signature", "X-Hub-Signature-256 does not match this service's webhook secret")
	}
	event := r.Header.Get("X-GitHub-Event")
	if id := r.Header.Get("X-GitHub-Delivery"); id != "" && !s.deliveries.first(svcID+"/"+id, time.Now()) {
		// a replayed (or redelivered) signed payload: building the same commit again only burns the server
		writeJSON(w, 202, map[string]any{"ignored": true, "reason": "delivery " + id + " was already handled"})
		return nil
	}
	switch event {
	case "ping":
		writeJSON(w, 200, map[string]any{"ok": true, "event": "ping"})
		return nil
	case "push":
	default:
		writeJSON(w, 202, map[string]any{"ignored": true, "reason": fmt.Sprintf("event %q is not a push", event)})
		return nil
	}
	raw := body
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		vals, err := url.ParseQuery(string(body))
		if err != nil {
			return badRequest("bad form payload")
		}
		raw = []byte(vals.Get("payload"))
	}
	var push githubPush
	if err := json.Unmarshal(raw, &push); err != nil {
		return badRequest("bad push payload: %v", err)
	}
	if push.Ref != "refs/heads/"+g.Branch {
		writeJSON(w, 202, map[string]any{"ignored": true, "reason": fmt.Sprintf("push to %s, service builds refs/heads/%s", push.Ref, g.Branch)})
		return nil
	}
	if push.Deleted || !commitShaRe.MatchString(push.After) || strings.Trim(push.After, "0") == "" {
		writeJSON(w, 202, map[string]any{"ignored": true, "reason": "branch deleted or no commit"})
		return nil
	}
	d, err := s.deployCommit(ctx, svcID, "webhook", push.After)
	if err != nil {
		return err
	}
	s.st.Audit(ctx, "github-webhook", "service.redeploy", svcID, map[string]string{"commit": push.After, "deployment": d.ID})
	writeJSON(w, 202, map[string]any{"deployment_id": d.ID, "commit": push.After})
	return nil
}

// deliverySet remembers recent webhook delivery ids so a captured, validly signed payload
// cannot be replayed to queue builds. Bounded: entries expire after deliveryTTL and the
// set never holds more than deliveryMax ids.
type deliverySet struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

const (
	deliveryTTL = 24 * time.Hour
	deliveryMax = 10000
)

// first records id and reports whether it had not been seen within deliveryTTL.
func (d *deliverySet) first(id string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen == nil {
		d.seen = map[string]time.Time{}
	}
	if at, ok := d.seen[id]; ok && now.Sub(at) < deliveryTTL {
		return false
	}
	if len(d.seen) >= deliveryMax {
		oldestID, oldest := "", now
		for k, at := range d.seen {
			if now.Sub(at) >= deliveryTTL {
				delete(d.seen, k)
				continue
			}
			if at.Before(oldest) {
				oldestID, oldest = k, at
			}
		}
		if len(d.seen) >= deliveryMax && oldestID != "" {
			delete(d.seen, oldestID)
		}
	}
	d.seen[id] = now
	return true
}
