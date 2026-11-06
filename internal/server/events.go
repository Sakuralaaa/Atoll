package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Event is one frame on /api/v1/events.
type Event struct {
	Type    string `json:"type"` // service|server|deployment|confirmation|domain|project|import|migration
	Payload any    `json:"payload"`
}

type eventBus struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newEventBus() *eventBus { return &eventBus{subs: map[chan []byte]struct{}{}} }

func (b *eventBus) subscribe() chan []byte {
	ch := make(chan []byte, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *eventBus) unsubscribe(ch chan []byte) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

func (s *Server) publish(typ string, payload any) {
	raw, err := json.Marshal(Event{Type: typ, Payload: payload})
	if err != nil {
		return
	}
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	for ch := range s.events.subs {
		select {
		case ch <- raw:
		default: // slow consumer: drop; the client refetches on reconnect
		}
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) error {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: originPatterns(r)})
	if err != nil {
		return nil
	}
	defer c.CloseNow()
	ch := s.events.subscribe()
	defer s.events.unsubscribe(ch)
	p := principalFrom(r.Context())
	ctx := c.CloseRead(r.Context())
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.bg.Done():
			c.Close(websocket.StatusGoingAway, "server shutting down")
			return nil
		case <-ping.C:
			if p != nil && p.Member { // re-read the scope: an admin's change or a disable applies here too
				np := s.authenticate(r)
				if np == nil || !np.Member {
					c.Close(websocket.StatusPolicyViolation, "signed out")
					return nil
				}
				p = np
			}
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return nil
			}
		case msg := <-ch:
			if p != nil && p.Member && !s.memberSeesEvent(ctx, p, msg) {
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return nil
			}
		}
	}
}

// originPatterns allows same-host browser WebSockets (Vite dev proxy forwards Host).
func originPatterns(r *http.Request) []string {
	return []string{r.Host}
}

// memberSeesEvent: a member only gets frames about its servers and the projects on them.
// A frame it cannot place is dropped (fail closed).
func (s *Server) memberSeesEvent(ctx context.Context, p *principal, msg []byte) bool {
	var ev struct {
		Type    string         `json:"type"`
		Payload map[string]any `json:"payload"`
	}
	if json.Unmarshal(msg, &ev) != nil || ev.Payload == nil {
		return false
	}
	str := func(k string) string { v, _ := ev.Payload[k].(string); return v }
	switch ev.Type {
	case "server":
		return p.Servers[str("id")]
	case "project":
		return p.Projects[str("id")]
	case "confirmation":
		return false // approvals are the admin's
	}
	if sid := str("server_id"); sid != "" && !p.Servers[sid] {
		return false
	}
	if pid := str("project_id"); pid != "" {
		return p.Projects[pid]
	}
	svcID := str("service_id")
	if ev.Type == "service" {
		svcID = str("id")
	}
	if svcID != "" {
		svc, _, err := s.st.GetService(ctx, svcID)
		return err == nil && p.Projects[svc.ProjectID] && (svc.ServerID == "" || p.Servers[svc.ServerID])
	}
	return false
}
