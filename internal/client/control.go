package client

import (
	"context"
	"net/http"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ControlStatus returns the control server's own status (GET /control, read permission).
func (c *Client) ControlStatus(ctx context.Context) (*api.ControlStatus, error) {
	var v api.ControlStatus
	return &v, c.do(ctx, http.MethodGet, "/control", nil, &v)
}
