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

const mcpInstructions = `Atoll manages services on the user's own servers. Read tools are safe. ` +
	`Destructive actions (migrate, add_domain, zeabur steps) need the user's approval in the Atoll panel: ` +
	`such a tool returns "PENDING CONFIRMATION ..." and nothing happens until the user approves it there. ` +
	`Never retry to get around a pending confirmation; tell the user what to approve.`

func newMCPCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the Atoll tools over MCP (stdio) for AI agents",
		Long: "Runs an MCP server on stdin/stdout. Configure your agent with the command `atoll mcp`;\n" +
			"it uses the same login as the CLI (atoll login, or $ATOLL_URL/$ATOLL_TOKEN).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s := NewMCPServer(app)
			return server.ServeStdio(s)
		},
	}
}

// NewMCPServer builds the MCP server with every Atoll tool registered.
func NewMCPServer(app *App) *server.MCPServer {
	if app.NewClient == nil {
		app.NewClient = client.New
	}
	s := server.NewMCPServer("atoll", app.Version, server.WithToolCapabilities(false), server.WithInstructions(mcpInstructions))
	s.AddTools(MCPTools(app)...)
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
			return mcp.NewToolResultError(err.Error()), nil
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
			mcp.WithArray("domains", mcp.WithStringItems(), mcp.Description("hostnames to route to the first http port (need approval)"))),
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
				return Deploy(ctx, c, DeployRequest{Project: project, Image: image, Name: name, Ports: ports, Env: env, EnvOrder: sortedKeys(env), Domains: r.GetStringSlice("domains", nil)})
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
		t(tool("rollback", "Roll a service back to a previous deployment (default: the last good one before the current).", false, true,
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
		t(tool("add_domain", "Route a hostname to a service (changes DNS: needs approval in the panel).", false, true,
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
		t(tool("migrate", "Move a project to another server (stop, copy volumes, start, switch DNS). Needs approval in the panel.", false, true,
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
