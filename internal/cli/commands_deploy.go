package cli

// CLI: deploy.

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/client"
)

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
