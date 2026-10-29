package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/atoll-sh/atoll/internal/api"
	"github.com/atoll-sh/atoll/internal/client"
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
				return fmt.Errorf("--url and --token are required (create a token in the panel under Settings → API tokens)")
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
				return app.printJSON(map[string]any{"url": url, "user": me.Name(), "config": p})
			}
			fmt.Fprintf(app.Out, "Logged in to %s as %s (saved to %s)\n", url, me.Name(), p)
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "panel URL, e.g. https://atoll.example.com")
	cmd.Flags().StringVar(&token, "token", "", "API token (atl_pat_...)")
	return cmd
}

// ---------- servers ----------

func newServersCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "servers", Aliases: []string{"server"}, Short: "List servers"}
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
		Use: "rm PROJECT", Aliases: []string{"delete"}, Short: "Delete a project (needs approval in the panel)", Args: cobra.ExactArgs(1),
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
	cmd := &cobra.Command{Use: "services", Aliases: []string{"service", "svc"}, Short: "List services"}
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
	cmd.AddCommand(ls)
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
	var project, name string
	var ports, envs, domains []string
	cmd := &cobra.Command{
		Use:   "deploy IMAGE -p PROJECT [--name NAME] [--port [name:]PORT[/proto]] [--env K=V] [--domain HOST]",
		Short: "Deploy an image: create the service, or update its image/ports/env and redeploy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			if project == "" {
				return fmt.Errorf("-p PROJECT is required")
			}
			image := args[0]
			if name == "" {
				name = imageName(image)
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
			res, err := Deploy(ctxOf(cmd), c, DeployRequest{Project: project, Image: image, Name: name, Ports: parsed, Env: envMap, EnvOrder: envOrder, Domains: domains})
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
			fmt.Fprintf(app.Out, "%s service %s (%s) with %s\n", res.Action, res.Service.Name, res.Service.ID, image)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&project, "project", "p", "", "project name or id")
	f.StringVar(&name, "name", "", "service name (default: image name)")
	f.StringArrayVar(&ports, "port", nil, "port as [name:]PORT[/http|tcp|udp], repeatable")
	f.StringArrayVarP(&envs, "env", "e", nil, "KEY=VALUE, repeatable")
	f.StringArrayVar(&domains, "domain", nil, "hostname to route to the first http port, repeatable")
	return cmd
}

// DeployRequest is shared by the CLI and the MCP tool.
type DeployRequest struct {
	Project  string
	Image    string
	Name     string
	Ports    []api.Port
	Env      map[string]string
	EnvOrder []string
	Domains  []string
}

// DeployResult says what happened.
type DeployResult struct {
	Action  string      `json:"action"` // "created" | "updated" | "redeployed"
	Service api.Service `json:"service"`
	Pending []string    `json:"pending_confirmations,omitempty"`
}

// Deploy creates or updates a service from an image.
func Deploy(ctx context.Context, c *client.Client, r DeployRequest) (*DeployResult, error) {
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
		svc, err = c.CreateService(ctx, p.ID, api.Service{Name: r.Name, Kind: "image", Image: r.Image, Ports: ports, Volumes: []api.Volume{}})
		if err != nil {
			return nil, err
		}
		res.Action = "created"
		if len(set) > 0 {
			if _, err := c.PutEnv(ctx, svc.ID, set); err != nil {
				return nil, err
			}
		}
	} else {
		patch := map[string]any{}
		if existing.Image != r.Image {
			patch["image"] = r.Image
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
			if !envChanged {
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
	cmd := &cobra.Command{
		Use: "logs SERVICE [-f] [--tail N]", Short: "Show service logs", Args: cobra.ExactArgs(1),
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
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep streaming")
	cmd.Flags().IntVar(&tail, "tail", 200, "lines of history")
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
		Use: "add HOSTNAME -s SERVICE", Short: "Route a hostname to a service (needs approval: changes DNS)", Args: cobra.ExactArgs(1),
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
		Use: "rm HOSTNAME|DOMAIN_ID", Aliases: []string{"delete"}, Short: "Remove a domain (needs approval)", Args: cobra.ExactArgs(1),
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
		Use: "migrate PROJECT --to SERVER", Short: "Move a project to another server (stop → copy volumes → start → DNS); needs approval", Args: cobra.ExactArgs(1),
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

var _ = time.Second
