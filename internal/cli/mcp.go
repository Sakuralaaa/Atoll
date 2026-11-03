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
	return s + "Destructive tools (service_delete, project_delete, project_migrate, service_migrate, domain_add, domain_remove, backup_restore, service_rollback, service_stop) and service_exec run IMMEDIATELY when called: " +
		"there is no confirmation step, so confirm with the user before calling them. " +
		`If a tool returns "PENDING CONFIRMATION ..." the user must approve it in the panel; never retry to bypass it.`
}

func newMCPCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
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
	legacy := cmd.Flags().Bool("legacy-names", true, "also serve the pre-v0.1.8 tool names (list_servers, deploy, set_env, ...) as deprecated aliases")
	cmd.PreRun = func(*cobra.Command, []string) { app.MCPNoLegacy = !*legacy }
	return cmd
}

// mcpToolPerm names the token permission each MCP tool needs. Every tool must be listed.
var mcpToolPerm = map[string]string{
	"server_list":           api.PermRead,
	"problem_list":          api.PermRead,
	"project_list":          api.PermRead,
	"service_list":          api.PermRead,
	"service_logs":          api.PermRead,
	"deployment_get":        api.PermRead,
	"deployment_wait":       api.PermRead,
	"deployment_build_logs": api.PermRead,
	"zeabur_scan":           api.PermRead,
	"zeabur_import_status":  api.PermRead,
	"update_list":           api.PermRead,
	"backup_list":           api.PermRead,
	"backup_verify":         api.PermRead,
	"template_list":         api.PermRead,
	"service_deploy":        api.PermDeploy,
	"template_deploy":       api.PermDeploy,
	"env_set":               api.PermDeploy,
	"service_restart":       api.PermDeploy,
	"service_redeploy":      api.PermDeploy,
	"service_rollback":      api.PermDeploy,
	"backup_run":            api.PermDeploy,
	"backup_check":          api.PermDeploy,
	"backup_drill":          api.PermDeploy,
	"domain_add":            api.PermDomains,
	"project_migrate":       api.PermMigrate,
	"service_migrate":       api.PermMigrate,
	"zeabur_node_cutover":   api.PermMigrate,
	"backup_restore":        api.PermRestore,
	"service_delete":        api.PermDelete,
	"project_delete":        api.PermDelete,
	"service_stop":          api.PermDeploy,
	"service_start":         api.PermDeploy,
	"service_exec":          api.PermExec,
	"env_get":               api.PermRead,
	"domain_list":           api.PermRead,
	"domain_remove":         api.PermDomains,
}

// mcpLegacyNames maps the tool names used before v0.1.8 to the current <resource>_<action>
// names. `atoll mcp` still serves the old names as deprecated aliases unless --legacy-names=false.
var mcpLegacyNames = map[string]string{
	"list_servers": "server_list", "list_problems": "problem_list", "list_projects": "project_list",
	"list_services": "service_list", "get_logs": "service_logs", "get_deployment": "deployment_get",
	"wait_deployment": "deployment_wait", "get_build_logs": "deployment_build_logs", "list_updates": "update_list",
	"list_templates": "template_list", "deploy": "service_deploy", "deploy_template": "template_deploy",
	"set_env": "env_set", "restart": "service_restart", "redeploy": "service_redeploy", "rollback": "service_rollback",
	"add_domain": "domain_add", "migrate": "project_migrate", "delete_service": "service_delete", "delete_project": "project_delete",
}

func init() {
	for old, cur := range mcpLegacyNames {
		mcpToolPerm[old] = mcpToolPerm[cur]
	}
}

// withLegacyAliases appends a deprecated copy of every renamed tool under its old name.
func withLegacyAliases(tools []server.ServerTool) []server.ServerTool {
	byName := map[string]server.ServerTool{}
	for _, t := range tools {
		byName[t.Tool.Name] = t
	}
	olds := make([]string, 0, len(mcpLegacyNames))
	for old := range mcpLegacyNames {
		olds = append(olds, old)
	}
	sort.Strings(olds)
	out := tools
	for _, old := range olds {
		t, ok := byName[mcpLegacyNames[old]]
		if !ok {
			continue
		}
		t.Tool.Name = old
		t.Tool.Description = "Deprecated alias of " + mcpLegacyNames[old] + "; use that name. " + t.Tool.Description
		out = append(out, t)
	}
	return out
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

// MCPTools returns every tool served by `atoll mcp`, with the deprecated aliases unless
// app.MCPNoLegacy.
func MCPTools(app *App) []server.ServerTool {
	tools := mcpTools(app)
	if !app.MCPNoLegacy {
		tools = withLegacyAliases(tools)
	}
	return tools
}

func mcpTools(app *App) []server.ServerTool {
	t := func(tl mcp.Tool, body mcpBody) server.ServerTool {
		return server.ServerTool{Tool: tl, Handler: mcpHandle(app, body)}
	}
	const imm = " Runs immediately when the token has the %s permission (no confirmation step unless the token requires approval), so confirm with the user first."
	return []server.ServerTool{
		t(tool("server_list", "List the user's servers with status and resource usage.", true, false),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				return c.ListServers(ctx)
			}),
		t(tool("problem_list", "List current problems the control server watches: offline servers, disks >= 90% full, services down or crash-looping, failed deploys and backups, domain errors. Each has kind, subject, since and a message. Empty means all is well.", true, false,
			mcp.WithBoolean("resolved", mcp.Description("also problems resolved in the last 24 hours"))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				return c.Problems(ctx, r.GetBool("resolved", false))
			}),
		t(tool("project_list", "List projects (each project runs on one server).", true, false),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				return c.ListProjects(ctx)
			}),
		t(tool("service_list", "List services of a project.", true, false,
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
		t(tool("service_deploy", "Deploy an image, or build from a GitHub repository, to a project: creates the service or updates its image/source/ports/env and redeploys. Give exactly one of image and repo. "+
			"A git service is built on its own server (follow it with deployment_build_logs); an unchanged repo is redeployed, which builds the branch head. "+
			"Private repositories need a token that can only be set in the panel or with `atoll deploy --git-token-env`, never here. "+
			"A NEW git service's response contains its GitHub webhook URL and secret once: pass them to the user. Same as `atoll deploy`.", false, false,
			mcp.WithString("project", mcp.Required(), mcp.Description("project name or id")),
			mcp.WithString("image", mcp.Description("image ref, e.g. ghcr.io/me/app:sha-abc (or give repo)")),
			mcp.WithString("repo", mcp.Description("build from this GitHub repository instead: https://github.com/OWNER/REPO")),
			mcp.WithString("branch", mcp.Description("git branch (default main)")),
			mcp.WithString("context", mcp.Description("build context: subdirectory of the repository (default root)")),
			mcp.WithString("dockerfile", mcp.Description("Dockerfile path inside the context (default Dockerfile)")),
			mcp.WithObject("build_args", mcp.Description("docker build args {KEY: VALUE} (not secret: visible in the image history)")),
			mcp.WithString("name", mcp.Description("service name (default: image or repository name)")),
			mcp.WithArray("ports", mcp.WithStringItems(), mcp.Description("ports as [name:]PORT[/http|tcp|udp]")),
			mcp.WithObject("env", mcp.Description("environment variables to set, {KEY: VALUE}")),
			mcp.WithArray("domains", mcp.WithStringItems(), mcp.Description("hostnames to route to the first http port (needs the domains permission; runs immediately)")),
			mcp.WithString("server", mcp.Description("server name or id for a NEW service (default: the project's server; both must be on the tailnet)"))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				project, err := reqStr(r, "project")
				if err != nil {
					return nil, err
				}
				image := strings.TrimSpace(r.GetString("image", ""))
				git, err := mcpGitSource(r)
				if err != nil {
					return nil, err
				}
				if (image == "") == (git == nil) {
					return nil, fmt.Errorf("give exactly one of image and repo")
				}
				name := r.GetString("name", "")
				if name == "" {
					if git != nil {
						name = repoName(git.Repo)
					} else {
						name = imageName(image)
					}
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
				return Deploy(ctx, c, DeployRequest{Project: project, Image: image, Git: git, Name: name, Ports: ports, Env: env, EnvOrder: sortedKeys(env), Domains: r.GetStringSlice("domains", nil), Server: r.GetString("server", "")})
			}),
		t(tool("template_list", "List the one-click database templates (postgres, mysql, redis, mongo) with their image, ports and the exposed variables other services can reference (e.g. ${POSTGRES_CONNECTION_STRING}).", true, false),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				return ListTemplateSummaries(ctx, c)
			}),
		t(tool("template_deploy", "Create and deploy a NEW database service from a template (random password, data volume, exposed connection variables). Same as `atoll deploy --template`. Fails if the project already has a service with that name.", false, false,
			mcp.WithString("project", mcp.Required(), mcp.Description("project name or id")),
			mcp.WithString("template", mcp.Required(), mcp.Description("postgres | mysql | redis | mongodb")),
			mcp.WithString("name", mcp.Description("service name (default: the template kind)")),
			mcp.WithObject("env", mcp.Description("template variables to override, {KEY: VALUE}, e.g. {POSTGRES_DATABASE: app}")),
			mcp.WithString("server", mcp.Description("server name or id (default: the project's server; both must be on the tailnet)"))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				project, err := reqStr(r, "project")
				if err != nil {
					return nil, err
				}
				tpl, err := reqStr(r, "template")
				if err != nil {
					return nil, err
				}
				env := argMap(r, "env")
				return DeployTemplate(ctx, c, TemplateRequest{Project: project, Template: tpl, Name: r.GetString("name", ""), Env: env, EnvOrder: sortedKeys(env), Server: r.GetString("server", "")})
			}),
		t(tool("env_set", "Set (or unset) environment variables of a service in one change; the service redeploys once (redeploy=false only saves). Secret-looking keys are stored as secrets.", false, false,
			append(svcArgs(),
				mcp.WithObject("vars", mcp.Description("variables to set, {KEY: VALUE}")),
				mcp.WithArray("unset", mcp.WithStringItems(), mcp.Description("keys to remove")),
				mcp.WithBoolean("secret", mcp.Description("mark every set variable as secret")),
				mcp.WithBoolean("exposed", mcp.Description("share set variables with other services in the project")),
				mcp.WithBoolean("redeploy", mcp.Description("redeploy after saving (default true); false stages the change for a later redeploy")))...),
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
				out, err := c.PutEnvOpt(ctx, s.ID, client.MergeEnv(cur, set, unset), r.GetBool("redeploy", true))
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
		t(tool("service_logs", "Get the last lines of a service's logs.", true, false,
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
		t(tool("service_restart", "Restart a service's container.", false, false, svcArgs()...),
			actionBody(client.ActionRestart)),
		t(tool("service_stop", "Stop a service's container; it stays down until service_start. Volumes are kept. Runs immediately; confirm with the user first.", false, true, svcArgs()...),
			actionBody(client.ActionStop)),
		t(tool("service_start", "Start a stopped service.", false, false, svcArgs()...),
			actionBody(client.ActionStart)),
		t(tool("service_exec", "Run a one-shot, non-interactive command in a service's container and return its output and exit_code (0 = success; absent when it could not run). "+
			"A shell in the container can read its secrets and change its data: tell the user what you will run first.", false, true,
			append(svcArgs(),
				mcp.WithArray("cmd", mcp.Required(), mcp.WithStringItems(), mcp.Description(`argv, e.g. ["sh","-c","ls -la /app"]`)),
				mcp.WithNumber("timeout_sec", mcp.Description("default 60, max 600")))...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				argv := r.GetStringSlice("cmd", nil)
				if len(argv) == 0 {
					return nil, fmt.Errorf(`cmd is required, e.g. ["sh","-c","ls /"]`)
				}
				res, err := c.ExecTimeout(ctx, s.ID, argv, r.GetInt("timeout_sec", 0))
				if err != nil {
					return nil, err
				}
				return res, nil
			}),
		t(tool("env_get", "List a service's environment variables (secret values masked).", true, false, svcArgs()...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				vars, err := c.GetEnv(ctx, s.ID)
				if err != nil {
					return nil, err
				}
				for i := range vars {
					if vars[i].Secret {
						vars[i].Value = "••••"
					}
				}
				return vars, nil
			}),
		t(tool("domain_list", "List the domains routed to a service, with their status.", true, false, svcArgs()...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				return c.ListServiceDomains(ctx, s.ID)
			}),
		t(tool("domain_remove", "Remove a domain from a service (deletes its DNS record)."+fmt.Sprintf(imm, api.PermDomains), false, true,
			append(svcArgs(), mcp.WithString("hostname", mcp.Required(), mcp.Description("e.g. app.example.com")))...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				host, err := reqStr(r, "hostname")
				if err != nil {
					return nil, err
				}
				ds, err := c.ListServiceDomains(ctx, s.ID)
				if err != nil {
					return nil, err
				}
				for _, d := range ds {
					if strings.EqualFold(d.Hostname, host) {
						if err := c.DeleteDomain(ctx, d.ID); err != nil {
							return nil, err
						}
						return fmt.Sprintf("%s: removed %s", s.Name, d.Hostname), nil
					}
				}
				return nil, fmt.Errorf("%s has no domain %s", s.Name, host)
			}),
		t(tool("service_redeploy", "Redeploy a service (pull the image again and recreate the container; a service built from git builds its branch head).", false, false, svcArgs()...),
			actionBody(client.ActionRedeploy)),
		t(tool("service_rollback", "Roll a service back to a previous deployment (default: the last good one before the current). Runs immediately; confirm with the user first.", false, true,
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
		t(tool("deployment_wait", "Wait until a service's deployment finishes (default: its newest) and return it. status running means the service is up; failed carries the reason and the last build log lines. Call it after service_deploy, service_redeploy, service_rollback, service_start or env_set instead of polling.", true, false,
			append(svcArgs(),
				mcp.WithString("deployment_id", mcp.Description("dep_... to wait for (default: the newest)")),
				mcp.WithNumber("timeout_sec", mcp.Description("how long to wait (default 300, max 900)")))...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				return waitDeploymentTool(ctx, c, s, r.GetString("deployment_id", ""), r.GetInt("timeout_sec", 300))
			}),
		t(tool("domain_add", "Route a hostname to a service (changes DNS)."+fmt.Sprintf(imm, api.PermDomains), false, true,
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
		t(tool("project_migrate", "Move a whole project to another server (stop, copy volumes, start, switch DNS; downtime)."+fmt.Sprintf(imm, api.PermMigrate), false, true,
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
		t(tool("service_delete", "Delete ONE service with its container, volumes (data is lost) and domains."+fmt.Sprintf(imm, api.PermDelete), false, true, svcArgs()...),
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
		t(tool("project_delete", "Delete a whole project with ALL its services, volumes (data is lost) and domains."+fmt.Sprintf(imm, api.PermDelete), false, true,
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
		t(tool("deployment_get", "Show a service's deployments (newest first), or one deployment by id.", true, false,
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
		t(tool("deployment_build_logs", "Read the build log of a service built from git: the newest deployment unless deployment_id is given. Poll with after = last_seq until status is running or failed.", true, false,
			append(svcArgs(),
				mcp.WithString("deployment_id", mcp.Description("dep_... (default: the newest deployment)")),
				mcp.WithNumber("after", mcp.Description("only lines after this sequence number (last_seq of a previous call)")),
				mcp.WithNumber("tail", mcp.Description("keep at most this many of the newest lines (default 300, max 5000)")))...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				after := int64(r.GetInt("after", 0))
				if after < 0 {
					after = 0
				}
				n := r.GetInt("tail", 300)
				if n <= 0 || n > 5000 {
					n = 300
				}
				return GetBuildLogs(ctx, c, s, r.GetString("deployment_id", ""), after, n)
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
		t(tool("zeabur_import_status", "Show an import job (imp_...) and its steps. Rehearse/rollback/cleanup are run by the user with `atoll zeabur ...` or in the panel; several imports of one server are cut over together with zeabur_node_cutover.", true, false,
			mcp.WithString("import_id", mcp.Required(), mcp.Description("imp_... id"))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				id, err := reqStr(r, "import_id")
				if err != nil {
					return nil, err
				}
				return c.GetImport(ctx, id)
			}),
		t(tool("zeabur_node_cutover", "Cut over several rehearsed Zeabur imports (imp_...) of ONE server together: each project's Zeabur workloads scale to 0 and get a final data sync, then Zeabur's ingress (shared by every Zeabur project on that server) is parked once and Atoll serves all their hostnames on 80/443. Refused with other_zeabur_projects while a project outside the list still serves through the ingress. Follow each job with zeabur_import_status. Roll back with `atoll zeabur rollback --server SERVER` or the panel."+fmt.Sprintf(imm, api.PermMigrate)+" If it returns PENDING CONFIRMATION, never retry to bypass it.", false, true,
			mcp.WithArray("import_ids", mcp.Required(), mcp.Description("imp_... ids, all on the same server"), mcp.WithStringItems()),
			mcp.WithBoolean("force", mcp.Description("park the ingress even though other Zeabur projects on the server still serve (they go offline); default false"))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				ids := r.GetStringSlice("import_ids", nil)
				if len(ids) == 0 {
					return nil, fmt.Errorf("import_ids is required")
				}
				j, err := c.GetImport(ctx, ids[0])
				if err != nil {
					return nil, err
				}
				if j.ServerID == "" {
					return nil, fmt.Errorf("import %s is not an in-place Zeabur takeover", ids[0])
				}
				return c.ZeaburNodeCutover(ctx, j.ServerID, api.ZeaburNodeCutoverRequest{ImportIDs: ids, Force: r.GetBool("force", false)})
			}),
		t(tool("update_list", "List image updates: services whose tag now points to a newer digest than what runs, or that have a newer semver tag (same major; major bumps in major_tag). Apply one with `atoll updates --apply SERVICE` or the panel.", true, false,
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
		t(tool("backup_verify", "Show the newest backup repository check (restic check --read-data-subset, weekly) and, with service, that service's newest restore drill (monthly: newest snapshots restored into temporary resources), with their schedules. status failed = backups may not be restorable.", true, false,
			mcp.WithString("service", mcp.Description("service name or id (adds its restore drill)")),
			mcp.WithString("project", mcp.Description("project name or id (to disambiguate service names)"))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				sid := ""
				if ref := r.GetString("service", ""); ref != "" {
					s, err := c.ResolveService(ctx, r.GetString("project", ""), ref)
					if err != nil {
						return nil, err
					}
					sid = s.ID
				}
				return c.BackupVerifyStatus(ctx, sid)
			}),
		t(tool("backup_check", "Check the backup repository now (restic check, reading a share of the data). Returns the running record; read the result later with backup_verify.", false, false,
			mcp.WithString("subset", mcp.Description(`share of the data to read: "5%", "1/20" or "500M" (default: the configured one)`))),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				return c.RunBackupCheck(ctx, "", r.GetString("subset", ""))
			}),
		t(tool("backup_drill", "Rehearse a restore of a service now: its newest snapshots are restored into a temporary directory and a temporary database container of the same image, checked, and removed. The running service is not touched. Read the result later with backup_verify.", false, false, svcArgs()...),
			func(ctx context.Context, c *client.Client, r mcp.CallToolRequest) (any, error) {
				s, err := resolveSvc(ctx, c, r)
				if err != nil {
					return nil, err
				}
				return c.RunBackupDrill(ctx, s.ID)
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

// mcpGitSource reads the deploy tool's git arguments (nil when repo is not given). There is
// deliberately no token argument: a private repo's token is set in the panel or the CLI.
func mcpGitSource(r mcp.CallToolRequest) (*api.GitSource, error) {
	repo := strings.TrimSpace(r.GetString("repo", ""))
	args := r.GetArguments()
	if repo == "" {
		for _, k := range []string{"branch", "context", "dockerfile", "build_args"} {
			if v, ok := args[k]; ok && v != nil && v != "" {
				return nil, fmt.Errorf("%s needs repo", k)
			}
		}
		return nil, nil
	}
	if _, ok := args["token"]; ok {
		return nil, fmt.Errorf("a git token cannot be passed through MCP; ask the user to set it in the panel or with `atoll deploy --git-token-env`")
	}
	g := GitFlags{Repo: repo, Branch: r.GetString("branch", ""), Context: r.GetString("context", ""), Dockerfile: r.GetString("dockerfile", "")}
	src, err := g.Source(func(string) string { return "" })
	if err != nil {
		return nil, fmt.Errorf("%s", strings.ReplaceAll(err.Error(), "--git", "repo"))
	}
	if _, ok := args["build_args"]; ok {
		src.BuildArgs = argMap(r, "build_args")
	}
	return src, nil
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
