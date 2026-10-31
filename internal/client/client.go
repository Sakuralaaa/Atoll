// Package client is a typed Go client for the Atoll REST API (/api/v1, see docs/api.md).
// It authenticates with a personal access token (Bearer atl_pat_...). Destructive actions
// requested with a PAT are answered with 202 + Confirmation; those surface as
// *ConfirmationRequiredError so callers can tell the user to approve in the panel, or wait.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Client talks to one Atoll control node.
type Client struct {
	BaseURL   string // panel URL, e.g. https://atoll.example.com (with or without /api/v1)
	Token     string // atl_pat_...
	HTTP      *http.Client
	UserAgent string
}

// New returns a client for baseURL using token.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Token:     token,
		HTTP:      &http.Client{Timeout: 0},
		UserAgent: "atoll-cli",
	}
}

// APIError is a non-2xx answer carrying the server's ErrorBody.
type APIError struct {
	StatusCode int
	Message    string
	Code       string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if e.Code != "" {
		return fmt.Sprintf("atoll api: %d %s (%s)", e.StatusCode, msg, e.Code)
	}
	return fmt.Sprintf("atoll api: %d %s", e.StatusCode, msg)
}

// IsNotFound reports whether err is a 404 from the API.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound
}

// ConfirmationRequiredError is returned when the server answered 202 with a Confirmation:
// the action is queued until a human approves it in the panel.
type ConfirmationRequiredError struct {
	Confirmation api.Confirmation
}

func (e *ConfirmationRequiredError) Error() string {
	c := e.Confirmation
	s := fmt.Sprintf("confirmation required: %q needs approval in the Atoll panel (confirmation id %s)", c.Action, c.ID)
	if c.Summary != "" {
		s += ": " + c.Summary
	}
	return s + ". Approve it under Confirmations, or re-run with --wait to wait for approval."
}

// AsConfirmation extracts a pending confirmation from err.
func AsConfirmation(err error) (*api.Confirmation, bool) {
	var ce *ConfirmationRequiredError
	if errors.As(err, &ce) {
		return &ce.Confirmation, true
	}
	return nil, false
}

func (c *Client) apiURL(path string) string {
	base := strings.TrimRight(c.BaseURL, "/")
	base = strings.TrimSuffix(base, "/api/v1")
	return base + "/api/v1" + path
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.apiURL(path), body)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	return req, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// do sends a JSON request and decodes the JSON answer into out (may be nil).
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(req, out)
}

func (c *Client) send(req *http.Request, out any) error {
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeResponse(resp, out)
}

func decodeResponse(resp *http.Response, out any) error {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusAccepted {
		var cf api.Confirmation
		// other 202 bodies (e.g. a queued Backup) also carry an id: a confirmation has an action
		if len(data) > 0 && json.Unmarshal(data, &cf) == nil && cf.ID != "" && cf.Action != "" {
			return &ConfirmationRequiredError{Confirmation: cf}
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ae := &APIError{StatusCode: resp.StatusCode}
		var eb api.ErrorBody
		if json.Unmarshal(data, &eb) == nil && eb.Error != "" {
			ae.Message, ae.Code = eb.Error, eb.Code
		} else {
			ae.Message = strings.TrimSpace(string(data))
			if len(ae.Message) > 300 {
				ae.Message = ae.Message[:300]
			}
		}
		return ae
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s response: %w", resp.Request.URL.Path, err)
	}
	return nil
}

func esc(s string) string { return url.PathEscape(s) }

// ---------- auth ----------

// Me is whatever /auth/me returns; the shape is not part of types.go, so it is kept loose.
type Me map[string]any

// Name returns a display name from the /auth/me answer.
func (m Me) Name() string {
	for _, k := range []string{"username", "name", "user", "id"} {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func (c *Client) Me(ctx context.Context) (Me, error) {
	var m Me
	err := c.do(ctx, http.MethodGet, "/auth/me", nil, &m)
	return m, err
}

// ---------- servers ----------

func (c *Client) ListServers(ctx context.Context) ([]api.Server, error) {
	var v []api.Server
	return v, c.do(ctx, http.MethodGet, "/servers", nil, &v)
}

func (c *Client) GetServer(ctx context.Context, id string) (*api.Server, error) {
	var v api.Server
	return &v, c.do(ctx, http.MethodGet, "/servers/"+esc(id), nil, &v)
}

// UpdateServer PATCHes {name, provider, region}; empty fields are omitted.
func (c *Client) UpdateServer(ctx context.Context, id string, patch map[string]any) (*api.Server, error) {
	var v api.Server
	return &v, c.do(ctx, http.MethodPatch, "/servers/"+esc(id), patch, &v)
}

func (c *Client) DeleteServer(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/servers/"+esc(id), nil, nil)
}

func (c *Client) CreateJoinToken(ctx context.Context) (*api.JoinToken, error) {
	var v api.JoinToken
	return &v, c.do(ctx, http.MethodPost, "/servers/join-token", nil, &v)
}

func (c *Client) ServerMetrics(ctx context.Context, id, rng string) ([]api.NodeMetrics, error) {
	var v []api.NodeMetrics
	return v, c.do(ctx, http.MethodGet, "/servers/"+esc(id)+"/metrics?range="+url.QueryEscape(rng), nil, &v)
}

func (c *Client) ServerContainers(ctx context.Context, id string) ([]api.ContainerState, error) {
	var v []api.ContainerState
	return v, c.do(ctx, http.MethodGet, "/servers/"+esc(id)+"/containers", nil, &v)
}

// ---------- projects ----------

type CreateProjectRequest struct {
	Name        string `json:"name"`
	ServerID    string `json:"server_id"`
	Description string `json:"description,omitempty"`
}

func (c *Client) ListProjects(ctx context.Context) ([]api.Project, error) {
	var v []api.Project
	return v, c.do(ctx, http.MethodGet, "/projects", nil, &v)
}

func (c *Client) CreateProject(ctx context.Context, req CreateProjectRequest) (*api.Project, error) {
	var v api.Project
	return &v, c.do(ctx, http.MethodPost, "/projects", req, &v)
}

// GetProject returns the project including its services.
func (c *Client) GetProject(ctx context.Context, id string) (*api.Project, error) {
	var v api.Project
	return &v, c.do(ctx, http.MethodGet, "/projects/"+esc(id), nil, &v)
}

func (c *Client) UpdateProject(ctx context.Context, id string, patch map[string]any) (*api.Project, error) {
	var v api.Project
	return &v, c.do(ctx, http.MethodPatch, "/projects/"+esc(id), patch, &v)
}

func (c *Client) DeleteProject(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/projects/"+esc(id), nil, nil)
}

// MigrateProject moves all services of a project to another server (usually 202).
func (c *Client) MigrateProject(ctx context.Context, id, serverID string) error {
	return c.do(ctx, http.MethodPost, "/projects/"+esc(id)+"/migrate", map[string]string{"server_id": serverID}, nil)
}

// ---------- services ----------

func (c *Client) ListServices(ctx context.Context, projectID string) ([]api.Service, error) {
	var v []api.Service
	return v, c.do(ctx, http.MethodGet, "/projects/"+esc(projectID)+"/services", nil, &v)
}

func (c *Client) CreateService(ctx context.Context, projectID string, svc api.Service) (*api.Service, error) {
	var v api.Service
	return &v, c.do(ctx, http.MethodPost, "/projects/"+esc(projectID)+"/services", svc, &v)
}

func (c *Client) GetService(ctx context.Context, id string) (*api.Service, error) {
	var v api.Service
	return &v, c.do(ctx, http.MethodGet, "/services/"+esc(id), nil, &v)
}

// UpdateService PATCHes a partial Service (only the keys present in patch); triggers a redeploy.
func (c *Client) UpdateService(ctx context.Context, id string, patch map[string]any) (*api.Service, error) {
	var v api.Service
	return &v, c.do(ctx, http.MethodPatch, "/services/"+esc(id), patch, &v)
}

func (c *Client) DeleteService(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/services/"+esc(id), nil, nil)
}

// Service actions.
const (
	ActionStart    = "start"
	ActionStop     = "stop"
	ActionRestart  = "restart"
	ActionRedeploy = "redeploy"
)

func (c *Client) ServiceAction(ctx context.Context, id, action string) error {
	switch action {
	case ActionStart, ActionStop, ActionRestart, ActionRedeploy:
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	return c.do(ctx, http.MethodPost, "/services/"+esc(id)+"/actions", map[string]string{"action": action}, nil)
}

func (c *Client) Rollback(ctx context.Context, serviceID, deploymentID string) error {
	return c.do(ctx, http.MethodPost, "/services/"+esc(serviceID)+"/rollback", map[string]string{"deployment_id": deploymentID}, nil)
}

func (c *Client) ListDeployments(ctx context.Context, serviceID string) ([]api.Deployment, error) {
	var v []api.Deployment
	return v, c.do(ctx, http.MethodGet, "/services/"+esc(serviceID)+"/deployments", nil, &v)
}

func (c *Client) ServiceMetrics(ctx context.Context, serviceID, rng string) ([]api.ContainerMetrics, error) {
	var v []api.ContainerMetrics
	return v, c.do(ctx, http.MethodGet, "/services/"+esc(serviceID)+"/metrics?range="+url.QueryEscape(rng), nil, &v)
}

// Exec runs a one-shot command in the service's container.
func (c *Client) Exec(ctx context.Context, serviceID string, cmd []string) (*api.ResultPayload, error) {
	var v api.ResultPayload
	return &v, c.do(ctx, http.MethodPost, "/services/"+esc(serviceID)+"/exec", map[string]any{"cmd": cmd}, &v)
}

// ---------- env ----------

func (c *Client) GetEnv(ctx context.Context, serviceID string) ([]api.EnvVar, error) {
	var v []api.EnvVar
	return v, c.do(ctx, http.MethodGet, "/services/"+esc(serviceID)+"/env", nil, &v)
}

// PutEnv replaces all variables. A secret with Value "" keeps its stored value.
func (c *Client) PutEnv(ctx context.Context, serviceID string, vars []api.EnvVar) ([]api.EnvVar, error) {
	if vars == nil {
		vars = []api.EnvVar{}
	}
	var v []api.EnvVar
	return v, c.do(ctx, http.MethodPut, "/services/"+esc(serviceID)+"/env", vars, &v)
}

func (c *Client) EnvReferences(ctx context.Context, serviceID string) ([]api.EnvVar, error) {
	var v []api.EnvVar
	return v, c.do(ctx, http.MethodGet, "/services/"+esc(serviceID)+"/env/references", nil, &v)
}

// MergeEnv returns current with set applied (upsert by key) and unset keys removed.
// Secret values of untouched keys stay "" so the server keeps them.
func MergeEnv(current []api.EnvVar, set []api.EnvVar, unset []string) []api.EnvVar {
	drop := map[string]bool{}
	for _, k := range unset {
		drop[k] = true
	}
	idx := map[string]int{}
	out := make([]api.EnvVar, 0, len(current)+len(set))
	for _, e := range current {
		if drop[e.Key] {
			continue
		}
		idx[e.Key] = len(out)
		out = append(out, e)
	}
	for _, e := range set {
		if i, ok := idx[e.Key]; ok {
			old := out[i]
			e.Secret = e.Secret || old.Secret
			e.Exposed = e.Exposed || old.Exposed
			out[i] = e
			continue
		}
		idx[e.Key] = len(out)
		out = append(out, e)
	}
	return out
}

// ---------- domains ----------

type AddDomainRequest struct {
	Hostname string `json:"hostname"`
	PortName string `json:"port_name,omitempty"`
	Proxied  bool   `json:"proxied"`
}

func (c *Client) ListServiceDomains(ctx context.Context, serviceID string) ([]api.Domain, error) {
	var v []api.Domain
	return v, c.do(ctx, http.MethodGet, "/services/"+esc(serviceID)+"/domains", nil, &v)
}

func (c *Client) AddDomain(ctx context.Context, serviceID string, req AddDomainRequest) (*api.Domain, error) {
	var v api.Domain
	return &v, c.do(ctx, http.MethodPost, "/services/"+esc(serviceID)+"/domains", req, &v)
}

func (c *Client) DeleteDomain(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/domains/"+esc(id), nil, nil)
}

func (c *Client) ListDomains(ctx context.Context) ([]api.Domain, error) {
	var v []api.Domain
	return v, c.do(ctx, http.MethodGet, "/domains", nil, &v)
}

// ---------- tokens, confirmations, settings, templates ----------

func (c *Client) ListTokens(ctx context.Context) ([]api.APIToken, error) {
	var v []api.APIToken
	return v, c.do(ctx, http.MethodGet, "/tokens", nil, &v)
}

func (c *Client) CreateToken(ctx context.Context, name, scope string) (*api.APIToken, error) {
	var v api.APIToken
	return &v, c.do(ctx, http.MethodPost, "/tokens", map[string]string{"name": name, "scope": scope}, &v)
}

func (c *Client) DeleteToken(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/tokens/"+esc(id), nil, nil)
}

// ListConfirmations lists confirmations; status "" lists all.
func (c *Client) ListConfirmations(ctx context.Context, status string) ([]api.Confirmation, error) {
	p := "/confirmations"
	if status != "" {
		p += "?status=" + url.QueryEscape(status)
	}
	var v []api.Confirmation
	return v, c.do(ctx, http.MethodGet, p, nil, &v)
}

// ApproveConfirmation and RejectConfirmation only work with a panel session, not a PAT.
func (c *Client) ApproveConfirmation(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/confirmations/"+esc(id)+"/approve", nil, nil)
}

func (c *Client) RejectConfirmation(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/confirmations/"+esc(id)+"/reject", nil, nil)
}

// WaitConfirmation polls until the confirmation leaves "pending". It returns the final
// Confirmation; an error if it was rejected or expired, or ctx ended.
func (c *Client) WaitConfirmation(ctx context.Context, id string, every time.Duration) (*api.Confirmation, error) {
	if every <= 0 {
		every = 3 * time.Second
	}
	for {
		all, err := c.ListConfirmations(ctx, "")
		if err != nil {
			return nil, err
		}
		var found *api.Confirmation
		for i := range all {
			if all[i].ID == id {
				found = &all[i]
				break
			}
		}
		if found != nil {
			switch found.Status {
			case "approved":
				return found, nil
			case "rejected", "expired":
				return found, fmt.Errorf("confirmation %s was %s", id, found.Status)
			}
		} else {
			// Not listed at all any more: treat it as resolved and let the caller re-check state.
			return &api.Confirmation{ID: id, Status: "unknown"}, nil
		}
		select {
		case <-ctx.Done():
			return found, fmt.Errorf("still waiting for approval of %s: %w", id, ctx.Err())
		case <-time.After(every):
		}
	}
}

func (c *Client) GetSettings(ctx context.Context) (*api.Settings, error) {
	var v api.Settings
	return &v, c.do(ctx, http.MethodGet, "/settings", nil, &v)
}

func (c *Client) PutSettings(ctx context.Context, s api.Settings) (*api.Settings, error) {
	var v api.Settings
	return &v, c.do(ctx, http.MethodPut, "/settings", s, &v)
}

// Template is a database template from GET /templates. Its shape is not in types.go;
// the server returns Service-like objects, so they decode into api.Service.
func (c *Client) ListTemplates(ctx context.Context) ([]api.Service, error) {
	var v []api.Service
	return v, c.do(ctx, http.MethodGet, "/templates", nil, &v)
}

// ---------- imports ----------

func (c *Client) ListImports(ctx context.Context) ([]api.ImportJob, error) {
	var v []api.ImportJob
	return v, c.do(ctx, http.MethodGet, "/imports", nil, &v)
}

func (c *Client) CreateImport(ctx context.Context, plan api.ImportPlan) (*api.ImportJob, error) {
	var v api.ImportJob
	return &v, c.do(ctx, http.MethodPost, "/imports", plan, &v)
}

func (c *Client) GetImport(ctx context.Context, id string) (*api.ImportJob, error) {
	var v api.ImportJob
	return &v, c.do(ctx, http.MethodGet, "/imports/"+esc(id), nil, &v)
}

// UploadImportVolume streams a tar.gz of a volume's contents (relative to the volume root).
func (c *Client) UploadImportVolume(ctx context.Context, importID, serviceName, volume string, body io.Reader, size int64) error {
	req, err := c.newRequest(ctx, http.MethodPut, "/imports/"+esc(importID)+"/volumes/"+esc(serviceName)+"/"+esc(volume), body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/gzip")
	if size > 0 {
		req.ContentLength = size
	}
	return c.send(req, nil)
}

func (c *Client) StartImport(ctx context.Context, id string) (*api.ImportJob, error) {
	var v api.ImportJob
	return &v, c.do(ctx, http.MethodPost, "/imports/"+esc(id)+"/start", nil, &v)
}

// WaitImport polls the job until it is done or failed.
func (c *Client) WaitImport(ctx context.Context, id string, every time.Duration, onUpdate func(*api.ImportJob)) (*api.ImportJob, error) {
	if every <= 0 {
		every = 3 * time.Second
	}
	for {
		job, err := c.GetImport(ctx, id)
		if err != nil {
			return nil, err
		}
		if onUpdate != nil {
			onUpdate(job)
		}
		switch job.Status {
		case "done":
			return job, nil
		case "failed":
			return job, fmt.Errorf("import %s failed", id)
		}
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-time.After(every):
		}
	}
}

// ---------- in-place Zeabur takeover (agent on the same host) ----------

// ZeaburScan returns what the agent on serverID found in Zeabur's local k3s (env masked).
func (c *Client) ZeaburScan(ctx context.Context, serverID string) (*api.ZeaburInventory, error) {
	var v api.ZeaburInventory
	return &v, c.do(ctx, http.MethodGet, "/servers/"+esc(serverID)+"/zeabur", nil, &v)
}

// ZeaburLocalImport creates the Atoll project + services for an in-place takeover.
func (c *Client) ZeaburLocalImport(ctx context.Context, req api.ZeaburLocalImportRequest) (*api.ImportJob, error) {
	var v api.ImportJob
	return &v, c.do(ctx, http.MethodPost, "/imports/zeabur-local", req, &v)
}

// importStep POSTs /imports/{id}/{step}; the answer is the updated ImportJob (or a 202 Confirmation).
func (c *Client) importStep(ctx context.Context, id, step string, body any) (*api.ImportJob, error) {
	var v api.ImportJob
	return &v, c.do(ctx, http.MethodPost, "/imports/"+esc(id)+"/"+step, body, &v)
}

// RehearseImport starts Atoll copies without public ingress, copies data and verifies. Zeabur keeps serving.
func (c *Client) RehearseImport(ctx context.Context, id string) (*api.ImportJob, error) {
	return c.importStep(ctx, id, "rehearse", nil)
}

// CutoverImport scales Zeabur apps to 0, final data sync, parks Zeabur's ingress, enables Atoll's.
func (c *Client) CutoverImport(ctx context.Context, id string) (*api.ImportJob, error) {
	return c.importStep(ctx, id, "cutover", nil)
}

// RollbackImport reverses a cutover.
func (c *Client) RollbackImport(ctx context.Context, id string) (*api.ImportJob, error) {
	return c.importStep(ctx, id, "rollback", nil)
}

// CleanupImport uninstalls Zeabur's k3s from the host (irreversible). req.Confirm must equal
// the server's name. The server treats it as session-only; with a PAT expect a Confirmation.
func (c *Client) CleanupImport(ctx context.Context, id string, req api.ZeaburCleanupRequest) (*api.ImportJob, error) {
	return c.importStep(ctx, id, "cleanup", req)
}
