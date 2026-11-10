package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// Member notifications (sub-accounts B5). A member keeps its own channels (webhook or
// Telegram; email needs an SMTP server and stays with the admin) and gets the problems it
// can see (memberSeesProblem): its projects' services on its servers, and server-level
// problems of its servers. The admin's channels (settings/notifications) keep getting
// everything. A member's message goes out once, in the pass that marks the problem notified;
// a failed member channel is logged, not retried.

func memberNotifyKey(userID int64) string {
	return "notifications.user." + strconv.FormatInt(userID, 10)
}

func (s *Server) memberChannels(ctx context.Context, userID int64) ([]api.NotificationChannel, error) {
	var mn api.MemberNotifications
	err := s.st.GetSecretBlob(ctx, memberNotifyKey(userID), &mn)
	if errors.Is(err, store.ErrNotFound) {
		err = nil
	}
	if mn.Channels == nil {
		mn.Channels = []api.NotificationChannel{}
	}
	return mn.Channels, err
}

// memberRecipient is one active member with enabled channels and its current scope.
type memberRecipient struct {
	user  store.User
	chans []api.NotificationChannel
	scope store.MemberScope
}

// memberRecipients reads, once per delivery pass, the members that have something to send to.
func (s *Server) memberRecipients(ctx context.Context) []memberRecipient {
	users, err := s.st.ListUsers(ctx)
	if err != nil {
		s.log.Warn("member notifications: users", "err", err)
		return nil
	}
	var out []memberRecipient
	for _, u := range users {
		if !u.IsMember() || u.Disabled {
			continue
		}
		chans, err := s.memberChannels(ctx, u.ID)
		if err != nil {
			s.log.Warn("member notifications: channels", "user", u.Username, "err", err)
			continue
		}
		var on []api.NotificationChannel
		for _, c := range chans {
			if c.Enabled && memberChannelType(c.Type) {
				on = append(on, c)
			}
		}
		if len(on) == 0 {
			continue
		}
		sc, err := s.st.MemberScopeOf(ctx, u.ID)
		if err != nil {
			continue
		}
		out = append(out, memberRecipient{user: u, chans: on, scope: sc})
	}
	return out
}

// notifyMembers sends msg to every member that may see p.
func (s *Server) notifyMembers(ctx context.Context, rcpts []memberRecipient, p api.Problem, msg notifyMessage) {
	for _, m := range rcpts {
		if !memberSeesProblem(m.scope.Servers, m.scope.Projects, p) {
			continue
		}
		for _, ch := range m.chans {
			if err := s.sendMemberNotification(ctx, ch, msg); err != nil {
				s.log.Warn("member notification failed", "user", m.user.Username, "channel", ch.Type, "err", err)
			}
		}
	}
}

func memberChannelType(t string) bool { return t == "webhook" || t == "telegram" }

// sendMemberNotification is sendNotification with the member's webhook going through
// MemberNotifyHTTP, which refuses private and loopback addresses: a member must not make the
// control server reach into its own network.
func (s *Server) sendMemberNotification(ctx context.Context, ch api.NotificationChannel, msg notifyMessage) error {
	if !memberChannelType(ch.Type) {
		return fmt.Errorf("channel type %q is not available to sub-accounts", ch.Type)
	}
	if ch.Type == "webhook" {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return s.postJSONWith(ctx, s.cfg.MemberNotifyHTTP, ch.URL, mustJSONBytes(msg))
	}
	return s.sendNotification(ctx, ch, msg)
}

// ---------- API: /me/notifications (members) ----------

func requireMemberSession(r *http.Request) (*principal, error) {
	p := principalFrom(r.Context())
	if !p.Member {
		return nil, badRequest("admins set notifications under Settings → Notifications (/settings/notifications)")
	}
	return p, nil
}

// GET /me/notifications
func (s *Server) handleGetMyNotifications(w http.ResponseWriter, r *http.Request) error {
	p, err := requireMemberSession(r)
	if err != nil {
		return err
	}
	chans, err := s.memberChannels(r.Context(), p.User.ID)
	if err != nil {
		return err
	}
	for i := range chans {
		chans[i] = maskChannel(chans[i])
	}
	writeJSON(w, 200, api.MemberNotifications{Channels: chans})
	return nil
}

// PUT /me/notifications
func (s *Server) handlePutMyNotifications(w http.ResponseWriter, r *http.Request) error {
	p, err := requireMemberSession(r)
	if err != nil {
		return err
	}
	var req api.MemberNotifications
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	cur, err := s.memberChannels(ctx, p.User.ID)
	if err != nil {
		return err
	}
	old := map[string]api.NotificationChannel{}
	for _, c := range cur {
		old[c.ID] = c
	}
	if len(req.Channels) > 5 {
		return badRequest("at most 5 channels")
	}
	for i := range req.Channels {
		c := &req.Channels[i]
		if c.ID == "" || old[c.ID].ID == "" {
			c.ID = newID("nch")
		}
		mergeChannelSecrets(c, old[c.ID])
		if err := validateMemberChannel(*c); err != nil {
			return err
		}
	}
	if req.Channels == nil {
		req.Channels = []api.NotificationChannel{}
	}
	if err := s.st.SetSecretBlob(ctx, memberNotifyKey(p.User.ID), req); err != nil {
		return err
	}
	s.st.Audit(ctx, p.actor(), "me.notifications", p.User.Username, map[string]int{"channels": len(req.Channels)})
	for i := range req.Channels {
		req.Channels[i] = maskChannel(req.Channels[i])
	}
	writeJSON(w, 200, req)
	return nil
}

func validateMemberChannel(c api.NotificationChannel) error {
	if !memberChannelType(c.Type) {
		return badRequest("sub-accounts can use webhook or telegram channels")
	}
	if c.Type == "webhook" && !strings.HasPrefix(c.URL, "https://") && !strings.HasPrefix(c.URL, "http://") {
		return badRequest("webhook url must start with http:// or https://")
	}
	return validateChannel(c)
}

// POST /me/notifications/test
func (s *Server) handleTestMyNotification(w http.ResponseWriter, r *http.Request) error {
	p, err := requireMemberSession(r)
	if err != nil {
		return err
	}
	var req api.NotificationTestRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	cur, err := s.memberChannels(ctx, p.User.ID)
	if err != nil {
		return err
	}
	c := req.Channel
	for _, o := range cur {
		if o.ID == c.ID && c.ID != "" {
			mergeChannelSecrets(&c, o)
		}
	}
	if err := validateMemberChannel(c); err != nil {
		return err
	}
	now := time.Now()
	pr := api.Problem{ID: "prb_test", Kind: "test", SubjectName: "测试通知", Message: "这是一条来自 Atoll 的测试通知。", Since: now, FiringAt: &now}
	msg := notifyMessage{Event: "test", Problem: pr, Text: "[Atoll] 🔔 测试通知\n通知渠道配置正确。"}
	if err := s.sendMemberNotification(ctx, c, msg); err != nil {
		return errf(502, "notify_failed", "%v", err)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
