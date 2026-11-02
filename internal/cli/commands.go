package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/client"
)

func ctxOf(cmd *cobra.Command) context.Context {
	if c := cmd.Context(); c != nil {
		return c
	}
	return context.Background()
}

// ---------- login ----------

func newLoginCmd(app *App) *cobra.Command {
	var url, token string
	cmd := &cobra.Command{
		Use:   "login --url URL --token TOKEN",
		Short: "Store the panel URL and an API token (verified with /auth/me)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if url == "" {
				url = app.URL
			}
			if token == "" {
				token = app.Token
			}
			if token == "" {
				token = os.Getenv("ATOLL_TOKEN")
			}
			if url == "" || token == "" {
				return fmt.Errorf("--url and --token are required (create a token in the panel under CLI & API → tokens)")
			}
			c := app.NewClient(url, token)
			me, err := c.Me(ctxOf(cmd))
			if err != nil {
				return fmt.Errorf("could not verify the token: %w", err)
			}
			p, err := SaveConfig(app.ConfigPath, Config{URL: strings.TrimRight(url, "/"), Token: token, User: me.Name()})
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(map[string]any{"url": url, "user": me.Name(), "config": p, "permissions": me.Permissions(), "projects": me.Projects(), "require_approval": me.RequireApproval()})
			}
			fmt.Fprintf(app.Out, "Logged in to %s as %s (saved to %s)\n", url, me.Name(), p)
			fmt.Fprintf(app.Out, "Token permissions: %s\n", strings.Join(me.Permissions(), ", "))
			if pr := me.Projects(); len(pr) > 0 {
				fmt.Fprintf(app.Out, "Limited to projects: %s\n", strings.Join(pr, ", "))
			}
			if me.RequireApproval() {
				fmt.Fprintln(app.Out, "Destructive operations wait for approval in the panel (require_approval).")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "panel URL, e.g. https://atoll.example.com")
	cmd.Flags().StringVar(&token, "token", "", "API token (atl_pat_...)")
	return cmd
}

// ---------- servers ----------

func newServersCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "servers", Aliases: []string{"server"}, Short: "List servers and switch their private network"}
	cmd.AddCommand(&cobra.Command{
		Use: "ls", Aliases: []string{"list"}, Short: "List servers", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			list, err := c.ListServers(ctxOf(cmd))
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(list)
			}
			tw := app.table()
			fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tIP\tCPU\tMEM\tDISK\tLAST SEEN")
			for _, s := range list {
				cpu, mem := "-", "-"
				if s.Usage != nil {
					cpu = fmt.Sprintf("%.0f%%", s.Usage.CPUPct)
					mem = fmt.Sprintf("%d/%dMB", s.Usage.MemUsedMB, s.Usage.MemTotalMB)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%dGB\t%s\n", s.ID, s.Name, s.Status, s.PublicIPv4, cpu, mem, s.DiskGB, ago(s.LastSeenAt))
			}
			return tw.Flush()
		},
	})
	cmd.AddCommand(newServerNetworkCmd(app))
	return cmd
}

// ---------- projects ----------

func newProjectsCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "projects", Aliases: []string{"project"}, Short: "List, create and remove projects"}
	cmd.AddCommand(&cobra.Command{
		Use: "ls", Aliases: []string{"list"}, Short: "List projects", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			list, err := c.ListProjects(ctxOf(cmd))
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(list)
			}
			tw := app.table()
			fmt.Fprintln(tw, "ID\tNAME\tSERVER\tSERVICES\tCREATED")
			for _, p := range list {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", p.ID, p.Name, p.ServerID, len(p.Services), ago(p.CreatedAt))
			}
			return tw.Flush()
		},
	})
	var server, desc string
	create := &cobra.Command{
		Use: "create NAME --server SERVER", Short: "Create a project on a server", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			if server == "" {
				return fmt.Errorf("--server is required (all services of a project run on one server; see `atoll servers ls`)")
			}
			ctx := ctxOf(cmd)
			s, err := c.ResolveServer(ctx, server)
			if err != nil {
				return err
			}
			p, err := c.CreateProject(ctx, client.CreateProjectRequest{Name: args[0], ServerID: s.ID, Description: desc})
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(p)
			}
			fmt.Fprintf(app.Out, "Created project %s (%s) on %s\n", p.Name, p.ID, s.Name)
			return nil
		},
	}
	create.Flags().StringVarP(&server, "server", "s", "", "server name or id")
	create.Flags().StringVar(&desc, "description", "", "description")
	cmd.AddCommand(create)
	cmd.AddCommand(&cobra.Command{
		Use: "rm PROJECT", Aliases: []string{"delete"}, Short: "Delete a project with all its services and volumes (needs the delete permission; runs immediately)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			p, err := c.ResolveProject(ctx, args[0])
			if err != nil {
				return err
			}
			if _, err := app.handle(ctx, c, c.DeleteProject(ctx, p.ID)); err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "Deleted project %s\n", p.Name)
			return nil
		},
	})
	return cmd
}

// ---------- services ----------

func newServicesCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "services", Aliases: []string{"service", "svc"}, Short: "List, delete and move services; show a git service's webhook"}
	var project string
	ls := &cobra.Command{
		Use: "ls -p PROJECT", Aliases: []string{"list"}, Short: "List services of a project", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			if project == "" {
				return fmt.Errorf("-p PROJECT is required")
			}
			p, err := c.ResolveProject(ctxOf(cmd), project)
			if err != nil {
				return err
			}
			svcs := p.Services
			if svcs == nil {
				if svcs, err = c.ListServices(ctxOf(cmd), p.ID); err != nil {
					return err
				}
			}
			if app.JSON {
				return app.printJSON(svcs)
			}
			tw := app.table()
			fmt.Fprintln(tw, "ID\tNAME\tKIND\tSTATUS\tREPLICAS\tIMAGE\tDOMAINS")
			for _, s := range svcs {
				var ds []string
				for _, d := range s.Domains {
					ds = append(ds, d.Hostname)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Name, s.Kind, s.Status, s.Replicas, s.Image, strings.Join(ds, ","))
			}
			return tw.Flush()
		},
	}
	ls.Flags().StringVarP(&project, "project", "p", "", "project name or id")
	var rmProject string
	rm := &cobra.Command{
		Use: "rm SERVICE [-p PROJECT]", Aliases: []string{"delete"}, Short: "Delete a service with its container, volumes and domains (needs the delete permission; runs immediately)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			s, err := c.ResolveService(ctx, rmProject, args[0])
			if err != nil {
				return err
			}
			if _, err := app.handle(ctx, c, c.DeleteService(ctx, s.ID)); err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "Deleted service %s\n", s.Name)
			return nil
		},
	}
	rm.Flags().StringVarP(&rmProject, "project", "p", "", "project name or id (to disambiguate service names)")
	cmd.AddCommand(ls, rm, newServiceMigrateCmd(app), newServiceWebhookCmd(app))
	return cmd
}

// ---------- deploy ----------

// ParsePort parses "[name:]port[/proto]", e.g. "8080", "web:8080/http", "db:5432/tcp".
func ParsePort(s string, i int) (api.Port, error) {
	p := api.Port{Protocol: "http"}
	if j := strings.LastIndex(s, "/"); j >= 0 {
		p.Protocol = strings.ToLower(s[j+1:])
		s = s[:j]
	}
	if j := strings.Index(s, ":"); j >= 0 {
		p.Name, s = s[:j], s[j+1:]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 || n > 65535 {
		return p, fmt.Errorf("bad port %q (want [name:]port[/http|tcp|udp])", s)
	}
	p.ContainerPort = n
	switch p.Protocol {
	case "http", "tcp", "udp":
	default:
		return p, fmt.Errorf("bad protocol %q", p.Protocol)
	}
	if p.Name == "" {
		if i == 0 && p.Protocol == "http" {
			p.Name = "web"
		} else {
			p.Name = fmt.Sprintf("port%d", n)
		}
	}
	return p, nil
}

// imageName derives a service name from an image ref.
func imageName(image string) string {
	ref := image
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	if i := strings.Index(ref, ":"); i >= 0 {
		ref = ref[:i]
	}
	return ref
}

func newDeployCmd(app *App) *cobra.Command {
	var project, name, server, template string
	var ports, envs, domains []string
	var gitFlags GitFlags
	cmd := &cobra.Command{
		Use: "deploy [IMAGE] -p PROJECT [--name NAME] [--port [name:]PORT[/proto]] [--env K=V] [--domain HOST] [--server SERVER] [--git URL [--branch B] [--context DIR] [--dockerfile F] [--build-arg K=V] [--git-token-env VAR]]",
		Example: "  atoll deploy ghcr.io/me/app:v2 -p shop --port web:3000/http --domain app.example.com\n" +
			"  atoll deploy --git https://github.com/me/app --branch main -p shop --port 3000\n" +
			"  GH_TOKEN=... atoll deploy --git https://github.com/me/private -p shop --git-token-env GH_TOKEN\n" +
			"  atoll deploy --template postgres db -p shop\n" +
			"  atoll deploy --template mongodb -p shop -e MONGO_USERNAME=app",
		Long: "Creates the service when the project has none by that name, otherwise updates it and redeploys.\n" +
			"Needs the deploy permission; --domain also needs the domains permission.\n" +
			"--server places a NEW service on a server other than the project's (both on the tailnet);\n" +
			"to move an existing service use `atoll services migrate`.\n" +
			"--template creates a database service from a template instead (same as `atoll templates deploy`);\n" +
			"the optional argument is then the service name (default: the template kind).\n" +
			"--git builds the service from a GitHub repository on the service's own server instead of\n" +
			"pulling IMAGE (give one or the other). A new git service prints its GitHub push webhook URL\n" +
			"and secret once. For an existing service a changed source is updated, an unchanged one is\n" +
			"redeployed (which builds the branch head). A private repo's token is read from the\n" +
			"environment variable named by --git-token-env, never from a flag value.",
		Short: "Deploy an image, a GitHub repo or a database template: create the service, or update it and redeploy",
		Args: func(cmd *cobra.Command, args []string) error {
			if template != "" || gitFlags.Repo != "" {
				return cobra.MaximumNArgs(1)(cmd, args)
			}
			if len(args) == 0 {
				return fmt.Errorf("give an IMAGE, --git URL or --template KIND")
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if template != "" {
				if gitFlags.Repo != "" || gitFlags.set(cmd) {
					return fmt.Errorf("--git and the build flags do not apply to --template")
				}
				if project == "" {
					return fmt.Errorf("-p PROJECT is required")
				}
				if len(ports) > 0 || len(domains) > 0 {
					return fmt.Errorf("--port and --domain do not apply to --template (templates bring their own ports; add a domain with `atoll domain add`)")
				}
				n := name
				if len(args) == 1 {
					if n != "" && n != args[0] {
						return fmt.Errorf("give the service name once (argument or --name)")
					}
					n = args[0]
				}
				return runTemplateDeploy(cmd, app, TemplateRequest{Project: project, Template: template, Name: n, Server: server}, envs)
			}
			c, err := app.client()
			if err != nil {
				return err
			}
			if project == "" {
				return fmt.Errorf("-p PROJECT is required")
			}
			var image string
			var git *api.GitSource
			if gitFlags.Repo != "" {
				if len(args) == 1 {
					return fmt.Errorf("give either IMAGE or --git URL, not both")
				}
				if git, err = gitFlags.Source(os.Getenv); err != nil {
					return err
				}
				if name == "" {
					name = repoName(git.Repo)
				}
			} else {
				if gitFlags.set(cmd) {
					return fmt.Errorf("--branch, --context, --dockerfile, --build-arg and --git-token-env need --git URL")
				}
				image = args[0]
				if name == "" {
					name = imageName(image)
				}
			}
			envMap, envOrder, err := parseKV(envs)
			if err != nil {
				return err
			}
			var parsed []api.Port
			for i, p := range ports {
				pp, err := ParsePort(p, i)
				if err != nil {
					return err
				}
				parsed = append(parsed, pp)
			}
			res, err := Deploy(ctxOf(cmd), c, DeployRequest{Project: project, Image: image, Git: git, Name: name, Ports: parsed, Env: envMap, EnvOrder: envOrder, Domains: domains, Server: server})
			if res != nil && len(res.Pending) > 0 {
				for _, p := range res.Pending {
					fmt.Fprintln(app.Err, "pending approval:", p)
				}
			}
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(res)
			}
			if git != nil {
				printGitDeploy(app, res, git)
				return nil
			}
			fmt.Fprintf(app.Out, "%s service %s (%s) with %s\n", res.Action, res.Service.Name, res.Service.ID, image)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&project, "project", "p", "", "project name or id")
	f.StringVar(&name, "name", "", "service name (default: image name)")
	f.StringArrayVar(&ports, "port", nil, "port as [name:]PORT[/http|tcp|udp], repeatable")
	f.StringArrayVarP(&envs, "env", "e", nil, "KEY=VALUE, repeatable")
	f.StringArrayVar(&domains, "domain", nil, "hostname to route to the first http port, repeatable (needs the domains permission)")
	f.StringVar(&server, "server", "", "server name or id for a new service (default: the project's server)")
	f.StringVar(&template, "template", "", "deploy a database template (postgres, mysql, redis, mongodb) instead of an image")
	gitFlags.register(f)
	return cmd
}

// DeployRequest is shared by the CLI and the MCP tool.
type DeployRequest struct {
	Project string
	Image   string
	// Git builds the service from a GitHub repository instead of pulling Image (exactly one
	// of Image and Git is set). For an existing service only the non-empty fields are compared
	// and sent (the server keeps omitted ones).
	Git      *api.GitSource
	Name     string
	Ports    []api.Port
	Env      map[string]string
	EnvOrder []string
	Domains  []string
	// Server (name or id) places a newly created service on that server instead of the
	// project's. For an existing service it must match where it runs (move it with migrate).
	Server string
}

// DeployResult says what happened.
type DeployResult struct {
	Action  string      `json:"action"` // "created" | "updated" | "redeployed"
	Service api.Service `json:"service"`
	Pending []string    `json:"pending_confirmations,omitempty"`
}

// Deploy creates or updates a service from an image or a git source.
func Deploy(ctx context.Context, c *client.Client, r DeployRequest) (*DeployResult, error) {
	if (r.Image == "") == (r.Git == nil) {
		return nil, fmt.Errorf("give either an image or a git source")
	}
	p, err := c.ResolveProject(ctx, r.Project)
	if err != nil {
		return nil, err
	}
	svcs := p.Services
	if svcs == nil {
		if svcs, err = c.ListServices(ctx, p.ID); err != nil {
			return nil, err
		}
	}
	var existing *api.Service
	for i := range svcs {
		if svcs[i].Name == r.Name || svcs[i].ID == r.Name {
			existing = &svcs[i]
		}
	}
	serverID := ""
	if r.Server != "" {
		srv, err := c.ResolveServer(ctx, r.Server)
		if err != nil {
			return nil, err
		}
		serverID = srv.ID
		if existing != nil {
			cur := existing.ServerID
			if cur == "" {
				cur = p.ServerID
			}
			if cur != srv.ID {
				return nil, fmt.Errorf("service %s already exists on another server; move it with `atoll services migrate %s --to %s`", existing.Name, existing.Name, srv.Name)
			}
		}
		if serverID == p.ServerID {
			serverID = ""
		}
	}
	res := &DeployResult{}
	var set []api.EnvVar
	order := r.EnvOrder
	if len(order) == 0 {
		for k := range r.Env {
			order = append(order, k)
		}
		sort.Strings(order)
	}
	for _, k := range order {
		set = append(set, api.EnvVar{Key: k, Value: r.Env[k], Secret: looksSecret(k)})
	}
	var svc *api.Service
	if existing == nil {
		ports := r.Ports
		if ports == nil {
			ports = []api.Port{}
		}
		if r.Git != nil {
			// env goes into the create request so it does not trigger a second build
			svc, err = c.CreateServiceWithEnv(ctx, p.ID, api.Service{Name: r.Name, Kind: "image", Git: r.Git, Ports: ports, Volumes: []api.Volume{}, ServerID: serverID}, set)
			if err != nil {
				return nil, err
			}
			res.Action = "created"
			set = nil
		} else {
			svc, err = c.CreateService(ctx, p.ID, api.Service{Name: r.Name, Kind: "image", Image: r.Image, Ports: ports, Volumes: []api.Volume{}, ServerID: serverID})
			if err != nil {
				return nil, err
			}
			res.Action = "created"
		}
		if len(set) > 0 {
			if _, err := c.PutEnv(ctx, svc.ID, set); err != nil {
				return nil, err
			}
		}
	} else {
		patch := map[string]any{}
		if r.Git != nil {
			if gitChanged(existing.Git, r.Git) {
				patch["git"] = gitPatch(r.Git)
			}
		} else {
			if existing.Git != nil {
				patch["git"] = nil // back to a registry image
				patch["image"] = r.Image
			} else if existing.Image != r.Image {
				patch["image"] = r.Image
			}
		}
		if len(r.Ports) > 0 {
			patch["ports"] = r.Ports
		}
		envChanged := false
		if len(set) > 0 {
			cur, err := c.GetEnv(ctx, existing.ID)
			if err != nil {
				return nil, err
			}
			if _, err := c.PutEnv(ctx, existing.ID, client.MergeEnv(cur, set, nil)); err != nil {
				return nil, err
			}
			envChanged = true
		}
		if len(patch) > 0 {
			if svc, err = c.UpdateService(ctx, existing.ID, patch); err != nil {
				return nil, err
			}
			res.Action = "updated"
		} else {
			svc = existing
			// an env change deploys the last built image; a git deploy should build the
			// branch head, so redeploy in that case too
			if !envChanged || r.Git != nil {
				if err := c.ServiceAction(ctx, existing.ID, client.ActionRedeploy); err != nil {
					return nil, err
				}
			}
			res.Action = "redeployed"
		}
	}
	res.Service = *svc
	have := map[string]bool{}
	for _, d := range svc.Domains {
		have[d.Hostname] = true
	}
	for _, d := range r.Domains {
		if have[d] {
			continue
		}
		if _, err := c.AddDomain(ctx, svc.ID, client.AddDomainRequest{Hostname: d, Proxied: true}); err != nil {
			if cf, ok := client.AsConfirmation(err); ok {
				res.Pending = append(res.Pending, fmt.Sprintf("domain %s: approve confirmation %s in the panel", d, cf.ID))
				continue
			}
			return res, err
		}
	}
	return res, nil
}

func looksSecret(k string) bool {
	u := strings.ToUpper(k)
	for _, s := range []string{"PASS", "SECRET", "TOKEN", "KEY", "PRIVATE", "CREDENTIAL", "DSN"} {
		if strings.Contains(u, s) {
			return true
		}
	}
	return false
}

// ---------- env ----------

func newEnvCmd(app *App) *cobra.Command {
	var svcRef, project string
	var secret, exposed bool
	cmd := &cobra.Command{Use: "env", Short: "List, set and unset service variables"}
	cmd.PersistentFlags().StringVarP(&svcRef, "service", "s", "", "service name or id")
	cmd.PersistentFlags().StringVarP(&project, "project", "p", "", "project name or id (to disambiguate service names)")
	resolve := func(cmd *cobra.Command) (*client.Client, *api.Service, error) {
		c, err := app.client()
		if err != nil {
			return nil, nil, err
		}
		if svcRef == "" {
			return nil, nil, fmt.Errorf("-s SERVICE is required")
		}
		s, err := c.ResolveService(ctxOf(cmd), project, svcRef)
		return c, s, err
	}
	cmd.AddCommand(&cobra.Command{
		Use: "ls -s SERVICE", Aliases: []string{"list"}, Short: "List variables (secrets masked)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, s, err := resolve(cmd)
			if err != nil {
				return err
			}
			vars, err := c.GetEnv(ctxOf(cmd), s.ID)
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(vars)
			}
			tw := app.table()
			fmt.Fprintln(tw, "KEY\tVALUE\tFLAGS")
			for _, v := range vars {
				val := v.Value
				if v.Secret {
					val = "••••"
				}
				var flags []string
				if v.Secret {
					flags = append(flags, "secret")
				}
				if v.Exposed {
					flags = append(flags, "exposed")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", v.Key, val, strings.Join(flags, ","))
			}
			return tw.Flush()
		},
	})
	set := &cobra.Command{
		Use: "set -s SERVICE KEY=VALUE...", Short: "Set variables (redeploys the service)", Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, s, err := resolve(cmd)
			if err != nil {
				return err
			}
			m, order, err := parseKV(args)
			if err != nil {
				return err
			}
			var vars []api.EnvVar
			for _, k := range order {
				vars = append(vars, api.EnvVar{Key: k, Value: m[k], Secret: secret || looksSecret(k), Exposed: exposed})
			}
			cur, err := c.GetEnv(ctxOf(cmd), s.ID)
			if err != nil {
				return err
			}
			out, err := c.PutEnv(ctxOf(cmd), s.ID, client.MergeEnv(cur, vars, nil))
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(out)
			}
			fmt.Fprintf(app.Out, "Set %s on %s\n", strings.Join(order, ", "), s.Name)
			return nil
		},
	}
	set.Flags().BoolVar(&secret, "secret", false, "mark as secret")
	set.Flags().BoolVar(&exposed, "exposed", false, "share with other services in the project as ${KEY}")
	cmd.AddCommand(set)
	cmd.AddCommand(&cobra.Command{
		Use: "unset -s SERVICE KEY...", Short: "Remove variables (redeploys the service)", Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, s, err := resolve(cmd)
			if err != nil {
				return err
			}
			cur, err := c.GetEnv(ctxOf(cmd), s.ID)
			if err != nil {
				return err
			}
			out, err := c.PutEnv(ctxOf(cmd), s.ID, client.MergeEnv(cur, nil, args))
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(out)
			}
			fmt.Fprintf(app.Out, "Unset %s on %s\n", strings.Join(args, ", "), s.Name)
			return nil
		},
	})
	return cmd
}

// ---------- logs ----------

func newLogsCmd(app *App) *cobra.Command {
	var follow bool
	var tail int
	var project string
	var build bool
	cmd := &cobra.Command{
		Use:   "logs SERVICE [-f] [--tail N] | logs SERVICE --build [DEPLOYMENT] [-f]",
		Short: "Show service logs, or the build log of a deployment (--build)",
		Long: "Without --build: the container's logs.\n" +
			"With --build: the build log of a service built from git; DEPLOYMENT (dep_...) defaults to the\n" +
			"newest deployment, -f keeps polling until the deployment finishes.",
		Example: "  atoll logs web -f\n  atoll logs web --build -f\n  atoll logs web --build dep_abc123",
		Args: func(cmd *cobra.Command, args []string) error {
			if build {
				return cobra.RangeArgs(1, 2)(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(ctxOf(cmd), os.Interrupt)
			defer stop()
			s, err := c.ResolveService(ctx, project, args[0])
			if err != nil {
				return err
			}
			if build {
				dep := ""
				if len(args) == 2 {
					dep = args[1]
				}
				err = printBuildLogs(ctx, app, c, s, dep, follow)
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			err = c.StreamLogs(ctx, s.ID, client.LogsOptions{Tail: tail, Follow: follow}, func(l api.LogLine) error {
				if app.JSON {
					return app.printJSON(l)
				}
				w := app.Out
				if l.Stream == "stderr" {
					w = app.Err
				}
				ts := ""
				if !l.At.IsZero() {
					ts = l.At.Local().Format("15:04:05") + " "
				}
				_, err := fmt.Fprintln(w, ts+l.Line)
				return err
			})
			if ctx.Err() != nil {
				return nil
			}
			return err
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep streaming (with --build: until the deployment finishes)")
	cmd.Flags().IntVar(&tail, "tail", 200, "lines of history")
	cmd.Flags().BoolVar(&build, "build", false, "show a deployment's build log (services built from git)")
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name or id")
	return cmd
}

// ---------- actions, rollback, exec ----------

func newActionCmd(app *App, action, short string) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use: action + " SERVICE", Short: short, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			s, err := c.ResolveService(ctx, project, args[0])
			if err != nil {
				return err
			}
			if _, err := app.handle(ctx, c, c.ServiceAction(ctx, s.ID, action)); err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(map[string]string{"service": s.ID, "action": action, "status": "ok"})
			}
			fmt.Fprintf(app.Out, "%s: %s requested\n", s.Name, action)
			return nil
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name or id")
	return cmd
}

func newRollbackCmd(app *App) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use: "rollback SERVICE [DEPLOYMENT]", Short: "Roll back to a previous deployment (default: the last good one)", Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			s, err := c.ResolveService(ctx, project, args[0])
			if err != nil {
				return err
			}
			depID := ""
			if len(args) == 2 {
				depID = args[1]
			} else {
				deps, err := c.ListDeployments(ctx, s.ID)
				if err != nil {
					return err
				}
				d, err := client.PreviousDeployment(deps)
				if err != nil {
					return err
				}
				depID = d.ID
			}
			if _, err := app.handle(ctx, c, c.Rollback(ctx, s.ID, depID)); err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(map[string]string{"service": s.ID, "deployment_id": depID})
			}
			fmt.Fprintf(app.Out, "%s: rolling back to %s\n", s.Name, depID)
			return nil
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name or id")
	return cmd
}

func newExecCmd(app *App) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use: "exec SERVICE -- CMD [ARGS...]", Short: "Run a one-shot command in a service's container", Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			s, err := c.ResolveService(ctx, project, args[0])
			if err != nil {
				return err
			}
			r, err := c.Exec(ctx, s.ID, args[1:])
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(r)
			}
			fmt.Fprint(app.Out, r.Output)
			if r.Output != "" && !strings.HasSuffix(r.Output, "\n") {
				fmt.Fprintln(app.Out)
			}
			if !r.OK {
				return fmt.Errorf("command failed: %s", r.Error)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name or id")
	return cmd
}

// ---------- domains ----------

func newDomainCmd(app *App) *cobra.Command {
	var svcRef, project, portName string
	var proxied bool
	cmd := &cobra.Command{Use: "domain", Aliases: []string{"domains"}, Short: "Add, remove and list domains"}
	cmd.PersistentFlags().StringVarP(&svcRef, "service", "s", "", "service name or id")
	cmd.PersistentFlags().StringVarP(&project, "project", "p", "", "project name or id")
	add := &cobra.Command{
		Use: "add HOSTNAME -s SERVICE", Short: "Route a hostname to a service (needs the domains permission: changes DNS)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			if svcRef == "" {
				return fmt.Errorf("-s SERVICE is required")
			}
			ctx := ctxOf(cmd)
			s, err := c.ResolveService(ctx, project, svcRef)
			if err != nil {
				return err
			}
			d, err := c.AddDomain(ctx, s.ID, client.AddDomainRequest{Hostname: args[0], PortName: portName, Proxied: proxied})
			if ok, err := app.handle(ctx, c, err); err != nil || !ok {
				return err
			}
			if app.JSON {
				return app.printJSON(d)
			}
			fmt.Fprintf(app.Out, "Domain %s → %s\n", args[0], s.Name)
			return nil
		},
	}
	add.Flags().StringVar(&portName, "port-name", "", "port name (default: first http port)")
	add.Flags().BoolVar(&proxied, "proxied", true, "Cloudflare proxy (orange cloud)")
	cmd.AddCommand(add)
	cmd.AddCommand(&cobra.Command{
		Use: "rm HOSTNAME|DOMAIN_ID", Aliases: []string{"delete"}, Short: "Remove a domain (needs the domains permission: changes DNS)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			id := args[0]
			if !strings.HasPrefix(id, "dom_") {
				all, err := c.ListDomains(ctx)
				if err != nil {
					return err
				}
				id = ""
				for _, d := range all {
					if strings.EqualFold(d.Hostname, args[0]) {
						id = d.ID
					}
				}
				if id == "" {
					return fmt.Errorf("domain %q not found", args[0])
				}
			}
			if _, err := app.handle(ctx, c, c.DeleteDomain(ctx, id)); err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "Removed %s\n", args[0])
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "ls [-s SERVICE]", Aliases: []string{"list"}, Short: "List domains", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			var list []api.Domain
			if svcRef != "" {
				s, err := c.ResolveService(ctx, project, svcRef)
				if err != nil {
					return err
				}
				list, err = c.ListServiceDomains(ctx, s.ID)
				if err != nil {
					return err
				}
			} else if list, err = c.ListDomains(ctx); err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(list)
			}
			tw := app.table()
			fmt.Fprintln(tw, "ID\tHOSTNAME\tSERVICE\tPORT\tPROXIED\tSTATUS")
			for _, d := range list {
				st := d.Status
				if d.Error != "" {
					st += ": " + d.Error
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%v\t%s\n", d.ID, d.Hostname, d.ServiceID, d.PortName, d.Proxied, st)
			}
			return tw.Flush()
		},
	})
	return cmd
}

// ---------- migrate ----------

func newMigrateCmd(app *App) *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use: "migrate PROJECT --to SERVER", Short: "Move a project to another server (stop → copy volumes → start → DNS); needs the migrate permission", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			if to == "" {
				return fmt.Errorf("--to SERVER is required")
			}
			ctx := ctxOf(cmd)
			p, err := c.ResolveProject(ctx, args[0])
			if err != nil {
				return err
			}
			s, err := c.ResolveServer(ctx, to)
			if err != nil {
				return err
			}
			if p.ServerID == s.ID {
				return fmt.Errorf("project %s already runs on %s", p.Name, s.Name)
			}
			if _, err := app.handle(ctx, c, c.MigrateProject(ctx, p.ID, s.ID)); err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "Migration of %s to %s started; follow it in the panel or with `atoll services ls -p %s`\n", p.Name, s.Name, p.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "target server name or id")
	return cmd
}
