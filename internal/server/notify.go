package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

const notifyKey = "notifications" // settings blob (encrypted): api.NotificationSettings with secrets

func (s *Server) notificationSettings(ctx context.Context) (api.NotificationSettings, error) {
	ns := api.NotificationSettings{NotifyResolved: true}
	err := s.st.GetSecretBlob(ctx, notifyKey, &ns)
	if errors.Is(err, store.ErrNotFound) {
		err = nil
	}
	if ns.Channels == nil {
		ns.Channels = []api.NotificationChannel{}
	}
	return ns, err
}

// deliverNotifications sends what ProblemsToNotify returns to every enabled channel. A message
// counts as sent when at least one channel took it (or none is configured, so nothing piles up
// for a channel added later); failures are logged and retried on the next tick.
func (s *Server) deliverNotifications(ctx context.Context, now time.Time) {
	todo, err := s.st.ProblemsToNotify(ctx)
	if err != nil || len(todo) == 0 {
		return
	}
	ns, err := s.notificationSettings(ctx)
	if err != nil {
		s.log.Warn("notification settings", "err", err)
		return
	}
	var chans []api.NotificationChannel
	for _, c := range ns.Channels {
		if c.Enabled {
			chans = append(chans, c)
		}
	}
	quiet := inQuietHours(ns, now)
	base := s.publicURL(ctx, nil)
	for _, c := range todo {
		p := c.Problem
		if c.Resolved && !ns.NotifyResolved {
			s.st.MarkProblemNotified(ctx, p.ID, true, now)
			continue
		}
		if quiet && !c.Resolved && p.Kind != api.ProblemNodeOffline {
			continue // sent when the quiet hours end, if it still fires
		}
		if quiet && c.Resolved {
			continue
		}
		ok := len(chans) == 0
		msg := notifyMessage{Event: "firing", Problem: p, Text: problemText(p, c.Resolved, base)}
		if c.Resolved {
			msg.Event = "resolved"
		}
		for _, ch := range chans {
			if err := s.sendNotification(ctx, ch, msg); err != nil {
				s.log.Warn("notification failed", "channel", ch.Type, "name", ch.Name, "err", err)
				continue
			}
			ok = true
		}
		if ok {
			s.st.MarkProblemNotified(ctx, p.ID, c.Resolved, now)
		}
	}
}

type notifyMessage struct {
	Event   string      `json:"event"` // firing | resolved | test
	Problem api.Problem `json:"problem"`
	Text    string      `json:"text"`
}

func problemText(p api.Problem, resolved bool, base string) string {
	head := "🔴 " + problemKindText(p.Kind) + "：" + p.SubjectName
	if resolved {
		head = "✅ 已恢复 · " + problemKindText(p.Kind) + "：" + p.SubjectName
	}
	lines := []string{head}
	if !resolved && p.Message != "" {
		lines = append(lines, p.Message)
	}
	if resolved && p.FiringAt != nil && p.ResolvedAt != nil {
		lines = append(lines, "持续 "+p.ResolvedAt.Sub(p.Since).Round(time.Minute).String())
	}
	if link := problemLink(p, base); link != "" {
		lines = append(lines, link)
	}
	return "[Atoll] " + strings.Join(lines, "\n")
}

func problemLink(p api.Problem, base string) string {
	if base == "" || strings.Contains(base, "localhost") {
		return ""
	}
	switch p.Kind {
	case api.ProblemNodeOffline, api.ProblemDiskHigh:
		return base + "/#/servers/" + p.SubjectID
	case api.ProblemDomainError:
		return base + "/#/domains"
	}
	if p.ProjectID != "" {
		return base + "/#/projects/" + p.ProjectID + "/services/" + p.SubjectID
	}
	return ""
}

// inQuietHours reports whether now falls in [QuietStart, QuietEnd) in the settings' timezone;
// the window may wrap midnight.
func inQuietHours(ns api.NotificationSettings, now time.Time) bool {
	start, ok1 := parseHHMM(ns.QuietStart)
	end, ok2 := parseHHMM(ns.QuietEnd)
	if !ok1 || !ok2 || start == end {
		return false
	}
	loc := time.UTC
	if ns.Timezone != "" {
		if l, err := time.LoadLocation(ns.Timezone); err == nil {
			loc = l
		}
	}
	t := now.In(loc)
	m := t.Hour()*60 + t.Minute()
	if start < end {
		return m >= start && m < end
	}
	return m >= start || m < end
}

func parseHHMM(v string) (int, bool) {
	h, m, ok := strings.Cut(strings.TrimSpace(v), ":")
	if !ok {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

func (s *Server) sendNotification(ctx context.Context, ch api.NotificationChannel, msg notifyMessage) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	switch ch.Type {
	case "webhook":
		body, _ := json.Marshal(msg)
		return s.postJSON(ctx, ch.URL, body)
	case "telegram":
		body, _ := json.Marshal(map[string]any{"chat_id": ch.ChatID, "text": msg.Text, "disable_web_page_preview": true})
		return s.postJSON(ctx, strings.TrimRight(s.cfg.TelegramAPI, "/")+"/bot"+ch.BotToken+"/sendMessage", body)
	case "email":
		subject, _, _ := strings.Cut(msg.Text, "\n")
		return sendMail(ctx, ch, subject, msg.Text)
	}
	return fmt.Errorf("unknown channel type %q", ch.Type)
}

func (s *Server) postJSON(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "atoll/"+Version)
	resp, err := s.cfg.NotifyHTTP.Do(req)
	if err != nil {
		return redactURLError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// redactURLError drops the URL from a transport error: webhook and Telegram URLs carry tokens.
func redactURLError(err error) error {
	var ue interface{ Unwrap() error }
	if errors.As(err, &ue) {
		if inner := ue.Unwrap(); inner != nil {
			return inner
		}
	}
	return err
}

func sendMail(ctx context.Context, ch api.NotificationChannel, subject, body string) error {
	port := ch.SMTPPort
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(ch.SMTPHost, strconv.Itoa(port))
	d := net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if port == 465 {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: &tls.Config{ServerName: ch.SMTPHost}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, ch.SMTPHost)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: ch.SMTPHost}); err != nil {
				return err
			}
		}
	}
	if ch.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", ch.Username, ch.Password, ch.SMTPHost)); err != nil {
			return err
		}
	}
	from, err := mail.ParseAddress(ch.From)
	if err != nil {
		return fmt.Errorf("from: %w", err)
	}
	tos, err := mail.ParseAddressList(ch.To)
	if err != nil {
		return fmt.Errorf("to: %w", err)
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	var hdrTo []string
	for _, t := range tos {
		if err := c.Rcpt(t.Address); err != nil {
			return err
		}
		hdrTo = append(hdrTo, t.String())
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := "From: " + from.String() + "\r\nTo: " + strings.Join(hdrTo, ", ") +
		"\r\nSubject: " + mimeHeader(subject) + "\r\nDate: " + time.Now().Format(time.RFC1123Z) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" +
		strings.ReplaceAll(body, "\n", "\r\n") + "\r\n"
	if _, err := io.WriteString(w, msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func mimeHeader(v string) string {
	return mime.BEncoding.Encode("UTF-8", v) // ASCII stays as is
}

// ---------- settings API ----------

// GET /settings/notifications
func (s *Server) handleGetNotifications(w http.ResponseWriter, r *http.Request) error {
	ns, err := s.notificationSettings(r.Context())
	if err != nil {
		return err
	}
	for i := range ns.Channels {
		ns.Channels[i] = maskChannel(ns.Channels[i])
	}
	writeJSON(w, 200, ns)
	return nil
}

func maskChannel(c api.NotificationChannel) api.NotificationChannel {
	c.HasURL, c.HasBotToken, c.HasPassword = c.URL != "", c.BotToken != "", c.Password != ""
	c.URL, c.BotToken, c.Password = "", "", ""
	return c
}

// PUT /settings/notifications
func (s *Server) handlePutNotifications(w http.ResponseWriter, r *http.Request) error {
	var req api.NotificationSettings
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	cur, err := s.notificationSettings(ctx)
	if err != nil {
		return err
	}
	old := map[string]api.NotificationChannel{}
	for _, c := range cur.Channels {
		old[c.ID] = c
	}
	if len(req.Channels) > 20 {
		return badRequest("at most 20 channels")
	}
	for i := range req.Channels {
		c := &req.Channels[i]
		if c.ID == "" || old[c.ID].ID == "" {
			c.ID = newID("nch")
		}
		mergeChannelSecrets(c, old[c.ID])
		if err := validateChannel(*c); err != nil {
			return err
		}
	}
	if (req.QuietStart == "") != (req.QuietEnd == "") {
		return badRequest("quiet_start and quiet_end must be set together")
	}
	if req.QuietStart != "" {
		if _, ok := parseHHMM(req.QuietStart); !ok {
			return badRequest("quiet_start must be HH:MM")
		}
		if _, ok := parseHHMM(req.QuietEnd); !ok {
			return badRequest("quiet_end must be HH:MM")
		}
	}
	if req.Timezone != "" {
		if _, err := time.LoadLocation(req.Timezone); err != nil {
			return badRequest("unknown timezone %q", req.Timezone)
		}
	}
	if req.Channels == nil {
		req.Channels = []api.NotificationChannel{}
	}
	if err := s.st.SetSecretBlob(ctx, notifyKey, req); err != nil {
		return err
	}
	s.st.Audit(ctx, principalFrom(ctx).actor(), "settings.notifications", "", map[string]int{"channels": len(req.Channels)})
	for i := range req.Channels {
		req.Channels[i] = maskChannel(req.Channels[i])
	}
	writeJSON(w, 200, req)
	return nil
}

func mergeChannelSecrets(c *api.NotificationChannel, old api.NotificationChannel) {
	if c.URL == "" {
		c.URL = old.URL
	}
	if c.BotToken == "" {
		c.BotToken = old.BotToken
	}
	if c.Password == "" {
		c.Password = old.Password
	}
	c.HasURL, c.HasBotToken, c.HasPassword = false, false, false
	c.Type = strings.TrimSpace(c.Type)
	c.Name = strings.TrimSpace(c.Name)
	c.ChatID = strings.TrimSpace(c.ChatID)
	c.SMTPHost = strings.TrimSpace(c.SMTPHost)
}

func validateChannel(c api.NotificationChannel) error {
	switch c.Type {
	case "webhook":
		if !strings.HasPrefix(c.URL, "https://") && !strings.HasPrefix(c.URL, "http://") {
			return badRequest("webhook url must start with http:// or https://")
		}
	case "telegram":
		if c.BotToken == "" || c.ChatID == "" {
			return badRequest("telegram needs bot_token and chat_id")
		}
	case "email":
		if c.SMTPHost == "" || c.From == "" || c.To == "" {
			return badRequest("email needs smtp_host, from and to")
		}
		if c.SMTPPort < 0 || c.SMTPPort > 65535 {
			return badRequest("invalid smtp_port")
		}
		if _, err := mail.ParseAddress(c.From); err != nil {
			return badRequest("invalid from address")
		}
		if _, err := mail.ParseAddressList(c.To); err != nil {
			return badRequest("invalid to address")
		}
	default:
		return badRequest("channel type must be webhook, telegram or email")
	}
	return nil
}

// POST /settings/notifications/test
func (s *Server) handleTestNotification(w http.ResponseWriter, r *http.Request) error {
	var req api.NotificationTestRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	cur, err := s.notificationSettings(ctx)
	if err != nil {
		return err
	}
	c := req.Channel
	for _, o := range cur.Channels {
		if o.ID == c.ID && c.ID != "" {
			mergeChannelSecrets(&c, o)
		}
	}
	if err := validateChannel(c); err != nil {
		return err
	}
	now := time.Now()
	p := api.Problem{ID: "prb_test", Kind: "test", SubjectName: "测试通知", Message: "这是一条来自 Atoll 的测试通知。", Since: now, FiringAt: &now}
	msg := notifyMessage{Event: "test", Problem: p, Text: "[Atoll] 🔔 测试通知\n通知渠道配置正确。"}
	if err := s.sendNotification(ctx, c, msg); err != nil {
		return errf(502, "notify_failed", "%v", err)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}
