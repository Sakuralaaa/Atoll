package client

import (
	"context"
	"net/http"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ListImageUpdates returns the image update status of every service
// (onlyAvailable: just those with an update).
func (c *Client) ListImageUpdates(ctx context.Context, onlyAvailable bool) ([]api.ImageUpdate, error) {
	p := "/image-updates"
	if onlyAvailable {
		p += "?available=1"
	}
	var v []api.ImageUpdate
	return v, c.do(ctx, http.MethodGet, p, nil, &v)
}

// GetImageUpdate returns one service's image update status.
func (c *Client) GetImageUpdate(ctx context.Context, serviceID string) (*api.ImageUpdate, error) {
	var v api.ImageUpdate
	return &v, c.do(ctx, http.MethodGet, "/services/"+esc(serviceID)+"/image-update", nil, &v)
}

// CheckImageUpdate asks the registry now.
func (c *Client) CheckImageUpdate(ctx context.Context, serviceID string) (*api.ImageUpdate, error) {
	var v api.ImageUpdate
	return &v, c.do(ctx, http.MethodPost, "/services/"+esc(serviceID)+"/image-update/check", nil, &v)
}

// SetImageUpdatePolicy sets notify|auto|off and the optional UTC window "HH:MM-HH:MM".
func (c *Client) SetImageUpdatePolicy(ctx context.Context, serviceID string, p api.ImageUpdatePolicy) (*api.ImageUpdate, error) {
	var v api.ImageUpdate
	return &v, c.do(ctx, http.MethodPut, "/services/"+esc(serviceID)+"/image-update/policy", p, &v)
}

// ApplyImageUpdate redeploys to the latest digest of the current tag, or to tag when set.
func (c *Client) ApplyImageUpdate(ctx context.Context, serviceID, tag string) (*api.Deployment, error) {
	var v api.Deployment
	return &v, c.do(ctx, http.MethodPost, "/services/"+esc(serviceID)+"/image-update/apply", api.ImageUpdateApplyRequest{Tag: tag}, &v)
}
