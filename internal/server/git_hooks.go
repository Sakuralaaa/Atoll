package server

// Push webhooks of GitLab and Gitea/Forgejo (GitHub's is in git.go). Unauthenticated like
// GitHub's, verified with the service's webhook secret: GitLab sends it as a token header
// (compared in constant time), Gitea/Forgejo sign the body with HMAC-SHA256 (hex).

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// readHook reads a webhook body (10 MiB max) and the service's git source.
func (s *Server) readHook(r *http.Request) ([]byte, *store.GitRow, string, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20+1))
	if err != nil {
		return nil, nil, "", badRequest("read body: %v", err)
	}
	if len(body) > 10<<20 {
		return nil, nil, "", errf(413, "too_large", "payload too large")
	}
	svcID := r.PathValue("service_id")
	g, err := s.st.GetServiceGit(r.Context(), svcID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, "", errf(404, "not_found", "no git service %q", svcID)
		}
		return nil, nil, "", err
	}
	return body, g, svcID, nil
}

// hookPush deploys a verified push of ref to after (ignored unless it is the service's branch).
func (s *Server) hookPush(ctx context.Context, w http.ResponseWriter, svcID string, g *store.GitRow, actor, ref, after string) error {
	if ref != "refs/heads/"+g.Branch {
		writeJSON(w, 202, map[string]any{"ignored": true, "reason": fmt.Sprintf("push to %s, service builds refs/heads/%s", ref, g.Branch)})
		return nil
	}
	if !commitShaRe.MatchString(after) || strings.Trim(after, "0") == "" {
		writeJSON(w, 202, map[string]any{"ignored": true, "reason": "branch deleted or no commit"})
		return nil
	}
	d, err := s.deployCommit(ctx, svcID, "webhook", after)
	if err != nil {
		return err
	}
	s.st.Audit(ctx, actor, "service.redeploy", svcID, map[string]string{"commit": after, "deployment": d.ID})
	writeJSON(w, 202, map[string]any{"deployment_id": d.ID, "commit": after})
	return nil
}

// duplicateDelivery answers a replayed delivery id (already handled) and reports it.
func (s *Server) duplicateDelivery(w http.ResponseWriter, svcID, id string) bool {
	if id == "" || s.deliveries.first(svcID+"/"+id, time.Now()) {
		return false
	}
	writeJSON(w, 202, map[string]any{"ignored": true, "reason": "delivery " + id + " was already handled"})
	return true
}

type pushRefAfter struct {
	ObjectKind string `json:"object_kind"`
	Ref        string `json:"ref"`
	After      string `json:"after"`
}

// POST /api/v1/hooks/gitlab/{service_id}: GitLab "Push Hook" with the secret token.
func (s *Server) handleGitLabWebhook(w http.ResponseWriter, r *http.Request) error {
	body, g, svcID, err := s.readHook(r)
	if err != nil {
		return err
	}
	tok := r.Header.Get("X-Gitlab-Token")
	if g.WebhookSecret == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(g.WebhookSecret)) != 1 {
		s.log.Warn("gitlab webhook: bad token", "service", svcID, "remote", r.RemoteAddr)
		return errf(401, "bad_signature", "X-Gitlab-Token does not match this service's webhook secret")
	}
	if s.duplicateDelivery(w, svcID, r.Header.Get("X-Gitlab-Event-UUID")) {
		return nil
	}
	if ev := r.Header.Get("X-Gitlab-Event"); ev != "Push Hook" {
		writeJSON(w, 202, map[string]any{"ignored": true, "reason": fmt.Sprintf("event %q is not a Push Hook", ev)})
		return nil
	}
	var push pushRefAfter
	if err := json.Unmarshal(body, &push); err != nil {
		return badRequest("bad push payload: %v", err)
	}
	return s.hookPush(r.Context(), w, svcID, g, "gitlab-webhook", push.Ref, push.After)
}

// verifyHexHMAC checks a hex HMAC-SHA256 of body (Gitea/Forgejo/Gogs signatures).
func verifyHexHMAC(secret string, body []byte, header string) bool {
	if secret == "" || header == "" {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(header), "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// POST /api/v1/hooks/gitea/{service_id}: Gitea or Forgejo push, signed with the secret.
func (s *Server) handleGiteaWebhook(w http.ResponseWriter, r *http.Request) error {
	body, g, svcID, err := s.readHook(r)
	if err != nil {
		return err
	}
	sig := r.Header.Get("X-Gitea-Signature")
	if sig == "" {
		sig = r.Header.Get("X-Forgejo-Signature")
	}
	if sig == "" {
		sig = r.Header.Get("X-Gogs-Signature")
	}
	if !verifyHexHMAC(g.WebhookSecret, body, sig) {
		s.log.Warn("gitea webhook: bad signature", "service", svcID, "remote", r.RemoteAddr)
		return errf(401, "bad_signature", "X-Gitea-Signature / X-Forgejo-Signature does not match this service's webhook secret")
	}
	id := r.Header.Get("X-Gitea-Delivery")
	if id == "" {
		id = r.Header.Get("X-Forgejo-Delivery")
	}
	if s.duplicateDelivery(w, svcID, id) {
		return nil
	}
	ev := r.Header.Get("X-Gitea-Event")
	if ev == "" {
		ev = r.Header.Get("X-Forgejo-Event")
	}
	if ev == "" {
		ev = r.Header.Get("X-Gogs-Event")
	}
	if ev != "push" {
		writeJSON(w, 202, map[string]any{"ignored": true, "reason": fmt.Sprintf("event %q is not a push", ev)})
		return nil
	}
	var push pushRefAfter
	if err := json.Unmarshal(body, &push); err != nil {
		return badRequest("bad push payload: %v", err)
	}
	return s.hookPush(r.Context(), w, svcID, g, "gitea-webhook", push.Ref, push.After)
}
