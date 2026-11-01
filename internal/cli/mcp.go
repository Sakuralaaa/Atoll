package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/client"
)

// mcpInstructions describes how permissions work for this token.
func mcpInstructions(me client.Me) string {
	s := "Atoll manages services on the user's own servers. Read tools are safe. " +
		"Only the tools this API token is permitted to use are listed; an operation outside its permissions fails with PERMISSION DENIED " +
		"(tell the user which permission to grant; do not try to work around it). "
	if me.RequireApproval() {
		return s + "This token requires approval: destructive tools (delete, migrate, domains, restore, zeabur steps) return " +
			`"PENDING CONFIRMATION ..." and nothing happens until the user approves it in the Atoll panel. ` +
			"Never retry to get around a pending confirmation; tell the user what to approve."
	}
	return s + "Destructive tools (delete_service, delete_project, migrate, service_migrate, add_domain, backup_restore, rollback) run IMMEDIATELY when called: " +
		"there is no confirmation step, so confirm with the user before calling them. " +
		`If a tool returns "PENDING CONFIRMATION ..." the user must approve it in the panel; never retry to bypass it.`
}

func newMCPCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the Atoll tools over MCP (stdio) for AI agents",
		Long: "Runs an MCP server on stdin/stdout. Configure your agent with the command `atoll mcp`;\n" +
			"it uses the same login as the CLI (atoll login, or $ATOLL_URL/$ATOLL_TOKEN).\n" +
			"At startup it reads the token's permissions (/auth/me) and only offers the tools the token\n" +
			"may use; give the agent a token with just the permissions it needs (`atoll token create`).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s := NewMCPServer(app)
			return server.ServeStdio(s)
		},
	}
}

// mcpToolPerm names the token permission each MCP tool needs. Every tool must be listed.
var mcpToolPerm = map[string]string{
	"list_servers":         api.PermRead,
	"list_projects":        api.PermRead,
	"list_services":        api.PermRead,
	"get_logs":             api.PermRead,
	"get_deployment":       api.PermRead,
	"zeabur_scan":          api.PermRead,
	"zeabur_import_status": api.PermRead,
	"list_updates":         api.PermRead,
	"backup_list":          api.PermRead,
	"deploy":               api.PermDeploy,
	"set_env":              api.PermDeploy,
	"restart":              api.PermDeploy,
	"redeploy":             api.PermDeploy,
	"rollback":             api.PermDeploy,
	"backup_run":           api.PermDeploy,
	"add_domain":           api.PermDomains,
	"migrate":              api.PermMigrate,
	"service_migrate":      api.PermMigrate,
	"backup_restore":       api.PermRestore,
	"delete_service":       api.PermDelete,
	"delete_project":       api.PermDelete,
}

// MCPToolPermission returns the permission tool needs ("" for an unknown tool).
func MCPToolPermission(tool string) string { return mcpToolPerm[tool] }

// FilterMCPTools keeps the tools whose permission is in perms.
func FilterMCPTools(tools []server.ServerTool, perms []string) []server.ServerTool {
	have := map[string]bool{}
	for _, p := range perms {
		have[p] = true
	}
	var out []server.ServerTool
	for _, t := range tools {
		if p := mcpToolPerm[t.Tool.Name]; p != "" && have[p] {
			out = append(out, t)
		}
	}
	return out
}

// NewMCPServer builds the MCP server. It asks /auth/me for the token's permissions and
// registers only the tools the token may use; when /auth/me cannot be reached every tool is
// registered (calls then report the real error).
func NewMCPServer(app *App) *server.MCPServer {
	if app.NewClient == nil {
		app.NewClient = client.New
	}
	var me client.Me
	tools := MCPTools(app)
	if c, err := app.client(); err == nil {
		if m, err := c.Me(context.Background()); err == nil {
			me = m
			tools = FilterMCPTools(tools, m.Permissions())
		} else if app.Err != nil {
			fmt.Fprintln(app.Err, "atoll mcp: could not read token permissions, offering every tool:", err)
		}
	} else if app.Err != nil {
		fmt.Fprintln(app.Err, "atoll mcp:", err)
	}
	s := server.NewMCPServer("atoll", app.Version, server.WithToolCapabilities(false), server.WithInstructions(mcpInstructions(me)))
	s.AddTools(tools...)
	return s
}

// mcpHandler wraps a tool body: it gets an authenticated client and turns errors and
// 202 confirmations into tool results.
type mcpBody func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error)

func mcpHandle(app *App, body mcpBody) server.ToolHandlerFunc {
	return func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		c, err := app.client()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		v, err := body(ctx, c, r)
		if err != nil {
			if cf, ok := client.AsConfirmation(err); ok {
				return mcp.NewToolResultText(pendingText(cf)), nil
			}
			return mcp.NewToolResultError(mcpErrorText(err)), nil
		}
		if s, ok := v.(string); ok {
			return mcp.NewToolResultText(s), nil
		}
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(b)), nil
	}
}

// mcpErrorText makes permission errors explicit for the agent.
func mcpErrorText(err error) string {
	if perm, ok := client.MissingPermission(err); ok {
		if perm == "" {
			return "PERMISSION DENIED: " + err.Error() + "\nNothing was done. Do not retry; ask the user to grant the permission to this API token in the Atoll panel."
		}
		return fmt.Sprintf("PERMISSION DENIED: this Atoll API token lacks the %q permission needed for this action. Nothing was done. "+
			"Do not retry or work around it; ask the user to grant %q to the token in the Atoll panel (CLI & API → tokens) if they want you to do this.", perm, perm)
	}
	switch client.ErrorCode(err) {
	case client.CodeProjectScope, client.CodeEscalation, client.CodeSessionRequired:
		return "PERMISSION DENIED: " + ExplainError(err).Error() + "\nNothing was done. Do not retry."
	}
	return err.Error()
}

func pendingText(cf *api.Confirmation) string {
	return fmt.Sprintf("PENDING CONFIRMATION %s (%s): %s\nNothing has been done yet. Ask the user to approve it in the Atoll panel (Confirmations). It was not forced.", cf.ID, cf.Action, cf.Summary)
}

func reqStr(r mcp.CallToolRequest, k string) (string, error) {
	v := strings.TrimSpace(r.GetString(k, ""))
	if v == "" {
		return "", fmt.Errorf("%s is required", k)
	}
	return v, nil
}

// argMap reads an object argument (string values) such as env.
func argMap(r mcp.CallToolRequest, k string) map[string]string {
	out := map[string]string{}
	raw, ok := r.GetArguments()[k]
	if !ok || raw == nil {
		return out
	}
	switch m := raw.(type) {
	case map[string]any:
		for key, v := range m {
			switch t := v.(type) {
			case string:
				out[key] = t
			case nil:
				out[key] = ""
			default:
				out[key] = fmt.Sprint(t)
			}
		}
	case string: // tolerate "K=V,K2=V2"
		for _, p := range strings.Split(m, ",") {
			if i := strings.Index(p, "="); i > 0 {
				out[strings.TrimSpace(p[:i])] = p[i+1:]
			}
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func svcArgs() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithString("service", mcp.Required(), mcp.Description("service name or id")),
		mcp.WithString("project", mcp.Description("project name or id (to disambiguate service names)")),
	}
}

func tool(name, desc string, readOnly, destructive bool, opts ...mcp.ToolOption) mcp.Tool {
	all := append([]mcp.ToolOption{mcp.WithDescription(desc), mcp.WithReadOnlyHintAnnotation(readOnly), mcp.WithDestructiveHintAnnotation(destructive)}, opts...)
	return mcp.NewTool(name, all...)
}

func resolveSvc(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (*api.Service, error) {
	ref, err := reqStr(r, "service")
	if err != nil {
		return nil, err
	}
	return c.ResolveService(ctx, r.GetString("project", ""), ref)
}

// MCPTools returns every tool served by `atoll mcp`.
func MCPTools(app *App) []server.ServerTool {
	t := func(tl mcp.Tool, body mcpBody) server.ServerTool {
		return server.ServerTool{Tool: tl, Handler: mcpHandle(app, body)}
	}
	const imm = " Runs immediately when the token has the %s permission (no confirmation step unless the token requires approval), so confirm with the user first."
	return []server.ServerTool{
		t(tool("list_servers", "List the user's servers with status and resource usage.", true, false),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				return c.ListServers(ctx)
			}),
		t(tool("list_projects", "List projects (each project runs on one server).", true, false),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				return c.ListProjects(ctx)
			}),
		t(tool("list_services", "List services of a project.", true, false,
			mcp.WithString("project", mcp.Required(), mcp.Description("project name or id"))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				ref, err := reqStr(r, "project")
				if err != nil {
					return nil, err
				}
				p, err := c.ResolveProject(ctx, ref)
				if err != nil {
					return nil, err
				}
				if p.Services != nil {
					return p.Services, nil
				}
				return c.ListServices(ctx, p.ID)
			}),
		t(tool("deploy", "Deploy an image to a project: creates the service or updates its image/ports/env and redeploys. Same as `atoll deploy`.", false, false,
			mcp.WithString("project", mcp.Required(), mcp.Description("project name or id")),
			mcp.WithString("image", mcp.Required(), mcp.Description("image ref, e.g. ghcr.io/me/app:sha-abc")),
			mcp.WithString("name", mcp.Description("service name (default: image name)")),
			mcp.WithArray("ports", mcp.WithStringItems(), mcp.Description("ports as [name:]PORT[/http|tcp|udp]")),
			mcp.WithObject("env", mcp.Description("environment variables to set, {KEY: VALUE}")),
			mcp.WithArray("domains", mcp.WithStringItems(), mcp.Description("hostnames to route to the first http port (needs the domains permission; runs immediately)")),
			mcp.WithString("server", mcp.Description("server name or id for a NEW service (default: the project's server; both must be on the tailnet)"))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				project, err := reqStr(r, "project")
				if err != nil {
					return nil, err
				}
				image, err := reqStr(r, "image")
				if err != nil {
					return nil, err
				}
				name := r.GetString("name", "")
				if name == "" {
					name = imageName(image)
				}
				var ports []api.Port
				for i, p := range r.GetStringSlice("ports", nil) {
					pp, err := ParsePort(p, i)
					if err != nil {
						return nil, err
					}
					ports = append(ports, pp)
				}
				env := argMap(r, "env")
				return Deploy(ctx, c, DeployRequest{Project: project, Image: image, Name: name, Ports: ports, Env: env, EnvOrder: sortedKeys(env), Domains: r.GetStringSlice("domains", nil), Server: r.GetString("server", "")})
			}),
		t(tool("set_env", "Set (or unset) environment variables of a service; the service redeploys. Secret-looking keys are stored as secrets.", false, false,
			append(svcArgs(),
				mcp.WithObject("vars", mcp.Description("variables to set, {KEY: VALUE}")),
				mcp.WithArray("unset", mcp.WithStringItems(), mcp.Description("keys to remove")),
				mcp.WithBoolean("secret", mcp.Description("mark every set variable as secret")),
				mcp.WithBoolean("exposed", mcp.Description("share set variables with other services in the project")))...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				vars := argMap(r, "vars")
				unset := r.GetStringSlice("unset", nil)
				if len(vars) == 0 && len(unset) == 0 {
					return nil, fmt.Errorf("give vars and/or unset")
				}
				var set []api.EnvVar
				for _, k := range sortedKeys(vars) {
					set = append(set, api.EnvVar{Key: k, Value: vars[k], Secret: r.GetBool("secret", false) || looksSecret(k), Exposed: r.GetBool("exposed", false)})
				}
				cur, err := c.GetEnv(ctx, s.ID)
				if err != nil {
					return nil, err
				}
				out, err := c.PutEnv(ctx, s.ID, client.MergeEnv(cur, set, unset))
				if err != nil {
					return nil, err
				}
				for i := range out {
					if out[i].Secret {
						out[i].Value = "••••"
					}
				}
				return out, nil
			}),
		t(tool("get_logs", "Get the last lines of a service's logs.", true, false,
			append(svcArgs(), mcp.WithNumber("tail", mcp.Description("number of lines (default 200, max 2000)")))...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				n := r.GetInt("tail", 200)
				if n <= 0 || n > 2000 {
					n = 200
				}
				lines, err := c.TailLogs(ctx, s.ID, n)
				if err != nil {
					return nil, err
				}
				var b strings.Builder
				for _, l := range lines {
					if !l.At.IsZero() {
						b.WriteString(l.At.UTC().Format("2006-01-02T15:04:05Z "))
					}
					if l.Stream == "stderr" {
						b.WriteString("[stderr] ")
					}
					b.WriteString(l.Line)
					b.WriteByte('\n')
				}
				if b.Len() == 0 {
					return "(no log lines)", nil
				}
				return b.String(), nil
			}),
		t(tool("restart", "Restart a service's container.", false, false, svcArgs()...),
			actionBody(client.ActionRestart)),
		t(tool("redeploy", "Redeploy a service (pull the image again and recreate the container).", false, false, svcArgs()...),
			actionBody(client.ActionRedeploy)),
		t(tool("rollback", "Roll a service back to a previous deployment (default: the last good one before the current). Runs immediately; confirm with the user first.", false, true,
			append(svcArgs(), mcp.WithString("deployment_id", mcp.Description("dep_... to roll back to")))...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				dep := r.GetString("deployment_id", "")
				if dep == "" {
					deps, err := c.ListDeployments(ctx, s.ID)
					if err != nil {
						return nil, err
					}
					d, err := client.PreviousDeployment(deps)
					if err != nil {
						return nil, err
					}
					dep = d.ID
				}
				if err := c.Rollback(ctx, s.ID, dep); err != nil {
					return nil, err
				}
				return fmt.Sprintf("%s: rolling back to %s", s.Name, dep), nil
			}),
		t(tool("add_domain", "Route a hostname to a service (changes DNS)."+fmt.Sprintf(imm, api.PermDomains), false, true,
			append(svcArgs(),
				mcp.WithString("hostname", mcp.Required(), mcp.Description("e.g. app.example.com")),
				mcp.WithString("port_name", mcp.Description("port name (default: first http port)")),
				mcp.WithBoolean("proxied", mcp.Description("Cloudflare proxy (default true)")))...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				host, err := reqStr(r, "hostname")
				if err != nil {
					return nil, err
				}
				return c.AddDomain(ctx, s.ID, client.AddDomainRequest{Hostname: host, PortName: r.GetString("port_name", ""), Proxied: r.GetBool("proxied", true)})
			}),
		t(tool("migrate", "Move a whole project to another server (stop, copy volumes, start, switch DNS; downtime)."+fmt.Sprintf(imm, api.PermMigrate), false, true,
			mcp.WithString("project", mcp.Required(), mcp.Description("project name or id")),
			mcp.WithString("server", mcp.Required(), mcp.Description("target server name or id"))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				pref, err := reqStr(r, "project")
				if err != nil {
					return nil, err
				}
				sref, err := reqStr(r, "server")
				if err != nil {
					return nil, err
				}
				p, err := c.ResolveProject(ctx, pref)
				if err != nil {
					return nil, err
				}
				s, err := c.ResolveServer(ctx, sref)
				if err != nil {
					return nil, err
				}
				if p.ServerID == s.ID {
					return nil, fmt.Errorf("project %s already runs on %s", p.Name, s.Name)
				}
				if err := c.MigrateProject(ctx, p.ID, s.ID); err != nil {
					return nil, err
				}
				return fmt.Sprintf("Migration of %s to %s started.", p.Name, s.Name), nil
			}),
		t(tool("service_migrate", "Move ONE service (container + volumes) to another server; it stops during the data copy and the rest of the project reaches it over the tailnet mesh under the same name (both servers must be on the tailnet)."+fmt.Sprintf(imm, api.PermMigrate), false, true,
			append(svcArgs(), mcp.WithString("server", mcp.Required(), mcp.Description("target server name or id")))...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				sref, err := reqStr(r, "server")
				if err != nil {
					return nil, err
				}
				srv, err := c.ResolveServer(ctx, sref)
				if err != nil {
					return nil, err
				}
				if s.ServerID == srv.ID {
					return nil, fmt.Errorf("service %s already runs on %s", s.Name, srv.Name)
				}
				return c.MigrateService(ctx, s.ID, srv.ID)
			}),
		t(tool("delete_service", "Delete ONE service with its container, volumes (data is lost) and domains."+fmt.Sprintf(imm, api.PermDelete), false, true, svcArgs()...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				if err := c.DeleteService(ctx, s.ID); err != nil {
					return nil, err
				}
				return fmt.Sprintf("Deleted service %s (%s).", s.Name, s.ID), nil
			}),
		t(tool("delete_project", "Delete a whole project with ALL its services, volumes (data is lost) and domains."+fmt.Sprintf(imm, api.PermDelete), false, true,
			mcp.WithString("project", mcp.Required(), mcp.Description("project name or id"))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				ref, err := reqStr(r, "project")
				if err != nil {
					return nil, err
				}
				p, err := c.ResolveProject(ctx, ref)
				if err != nil {
					return nil, err
				}
				if err := c.DeleteProject(ctx, p.ID); err != nil {
					return nil, err
				}
				return fmt.Sprintf("Deleted project %s (%s).", p.Name, p.ID), nil
			}),
		t(tool("get_deployment", "Show a service's deployments (newest first), or one deployment by id.", true, false,
			append(svcArgs(), mcp.WithString("deployment_id", mcp.Description("dep_... (default: all, newest first)")))...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				deps, err := c.ListDeployments(ctx, s.ID)
				if err != nil {
					return nil, err
				}
				if id := r.GetString("deployment_id", ""); id != "" {
					for _, d := range deps {
						if d.ID == id {
							return d, nil
						}
					}
					return nil, fmt.Errorf("deployment %s not found on %s", id, s.Name)
				}
				return deps, nil
			}),
		t(tool("zeabur_scan", "Scan what Zeabur runs on one of the user's servers (agent installed on the same VPS). Env values are masked.", true, false,
			mcp.WithString("server", mcp.Required(), mcp.Description("server name or id"))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				ref, err := reqStr(r, "server")
				if err != nil {
					return nil, err
				}
				s, err := c.ResolveServer(ctx, ref)
				if err != nil {
					return nil, err
				}
				return c.ZeaburScan(ctx, s.ID)
			}),
		t(tool("zeabur_import_status", "Show an import job (imp_...) and its steps. Rehearse/cutover/rollback/cleanup are run by the user with `atoll zeabur ...` or in the panel.", true, false,
			mcp.WithString("import_id", mcp.Required(), mcp.Description("imp_... id"))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				id, err := reqStr(r, "import_id")
				if err != nil {
					return nil, err
				}
				return c.GetImport(ctx, id)
			}),
		t(tool("list_updates", "List image updates: services whose tag now points to a newer digest than what runs, or that have a newer semver tag (same major; major bumps in major_tag). Apply one with `atoll updates --apply SERVICE` or the panel.", true, false,
			mcp.WithBoolean("available_only", mcp.Description("only services with an update (default true)")),
			mcp.WithString("project", mcp.Description("project name or id to filter"))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				list, err := c.ListImageUpdates(ctx, r.GetBool("available_only", true))
				if err != nil {
					return nil, err
				}
				if ref := r.GetString("project", ""); ref != "" {
					p, err := c.ResolveProject(ctx, ref)
					if err != nil {
						return nil, err
					}
					kept := list[:0]
					for _, u := range list {
						if u.ProjectID == p.ID {
							kept = append(kept, u)
						}
					}
					list = kept
				}
				return list, nil
			}),
		t(tool("backup_list", "List a service's backups (newest first): id, status, snapshots, size. Restore one with backup_restore.", true, false, svcArgs()...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				return c.ListBackups(ctx, s.ID, false)
			}),
		t(tool("backup_run", "Back up a service now (restic on the service's server, straight to the configured bucket).", false, false, svcArgs()...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				return c.RunBackup(ctx, s.ID)
			}),
		t(tool("backup_restore", "Restore a backup, replacing the service's current data (a safety copy is kept). target_server is disaster recovery and only works while the project's server is offline."+fmt.Sprintf(imm, api.PermRestore)+" If it returns PENDING CONFIRMATION, never retry to bypass it.", false, true,
			mcp.WithString("backup_id", mcp.Required(), mcp.Description("bak_... id or restic snapshot id from backup_list")),
			mcp.WithString("target_server", mcp.Description("restore onto this server (name or id); default in place"))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				id, err := reqStr(r, "backup_id")
				if err != nil {
					return nil, err
				}
				b, err := c.GetBackup(ctx, id)
				if err != nil {
					return nil, err
				}
				target := ""
				if ref := r.GetString("target_server", ""); ref != "" {
					srv, err := c.ResolveServer(ctx, ref)
					if err != nil {
						return nil, err
					}
					target = srv.ID
				}
				// a token with require_approval gets 202 + Confirmation (surfaced by mcpHandle as
				// PENDING CONFIRMATION); otherwise the restore starts now
				return c.RestoreBackup(ctx, b.ID, target)
			}),
	}
}

func actionBody(action string) mcpBody {
	return func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
		s, err := resolveSvc(ctx, c, r)
		if err != nil {
			return nil, err
		}
		if err := c.ServiceAction(ctx, s.ID, action); err != nil {
			return nil, err
		}
		return fmt.Sprintf("%s: %s requested", s.Name, action), nil
	}
}
