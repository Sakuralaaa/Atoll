package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/atoll-sh/atoll/internal/api"
)

// LogsOptions controls a log stream.
type LogsOptions struct {
	Tail   int  // lines of history, 0 = server default
	Follow bool // keep streaming new lines
	// Idle ends a non-follow stream when no line arrives for this long (default 5s);
	// it guards against servers that do not close the socket after the history.
	Idle time.Duration
}

func (c *Client) wsURL(path string) string {
	u := c.apiURL(path)
	switch {
	case strings.HasPrefix(u, "https://"):
		return "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		return "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u
}

// StreamLogs opens GET /services/{id}/logs (WebSocket) and calls fn for every LogLine frame
// until the server closes the stream, ctx ends, or fn returns an error.
func (c *Client) StreamLogs(ctx context.Context, serviceID string, opt LogsOptions, fn func(api.LogLine) error) error {
	q := "?follow=0"
	if opt.Follow {
		q = "?follow=1"
	}
	if opt.Tail > 0 {
		q += "&tail=" + strconv.Itoa(opt.Tail)
	}
	h := http.Header{}
	if c.Token != "" {
		h.Set("Authorization", "Bearer "+c.Token)
	}
	conn, resp, err := websocket.Dial(ctx, c.wsURL("/services/"+esc(serviceID)+"/logs"+q), &websocket.DialOptions{
		HTTPHeader: h,
		HTTPClient: c.HTTP,
	})
	if err != nil {
		if resp != nil && resp.StatusCode >= 300 {
			return &APIError{StatusCode: resp.StatusCode, Message: "log stream refused"}
		}
		return fmt.Errorf("open log stream: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)
	idle := opt.Idle
	if idle <= 0 {
		idle = 5 * time.Second
	}
	for {
		rctx, cancel := ctx, context.CancelFunc(func() {})
		if !opt.Follow {
			rctx, cancel = context.WithTimeout(ctx, idle)
		}
		_, data, err := conn.Read(rctx)
		timedOut := rctx.Err() != nil && ctx.Err() == nil
		cancel()
		if err != nil {
			if timedOut {
				return nil
			}
			var ce websocket.CloseError
			if errors.As(err, &ce) && (ce.Code == websocket.StatusNormalClosure || ce.Code == websocket.StatusGoingAway) {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		var line api.LogLine
		if err := json.Unmarshal(data, &line); err != nil {
			line = api.LogLine{ServiceID: serviceID, Line: string(data), Stream: "stdout"}
		}
		if err := fn(line); err != nil {
			conn.Close(websocket.StatusNormalClosure, "")
			return err
		}
	}
}

// TailLogs collects up to n history lines without following.
func (c *Client) TailLogs(ctx context.Context, serviceID string, n int) ([]api.LogLine, error) {
	var out []api.LogLine
	err := c.StreamLogs(ctx, serviceID, LogsOptions{Tail: n}, func(l api.LogLine) error {
		out = append(out, l)
		return nil
	})
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out, err
}
