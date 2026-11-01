package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/client"
)

// templateAliases maps common names to the server's template kinds.
var templateAliases = map[string]string{
	"postgres": "postgres", "postgresql": "postgres", "pg": "postgres",
	"mysql": "mysql",
	"redis": "redis",
	"mongo": "mongo", "mongodb": "mongo",
}

// TemplateKind normalizes a template name (postgresql, mongodb, …) to its kind.
func TemplateKind(name string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if k, ok := templateAliases[n]; ok {
		return k, nil
	}
	return "", fmt.Errorf("unknown template %q (want postgres, mysql, redis or mongodb; see `atoll templates ls`)", name)
}

// TemplateRequest is shared by the CLI and the MCP tool.
type TemplateRequest struct {
	Project  string
	Template string // postgres | mysql | redis | mongo (aliases accepted)
	Name     string // service name (default: the template kind)
	Env      map[string]string
	EnvOrder []string
	Server   string // server name or id (default: the project's)
}

// TemplateSummary is the compact view of a template the CLI and MCP print.
type TemplateSummary struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Image       string   `json:"image"`
	Ports       []string `json:"ports"`
	// ExposedEnv are the variables other services of the project can reference, e.g.
	// ${POSTGRES_CONNECTION_STRING}.
	ExposedEnv []string `json:"exposed_env"`
}

// ListTemplateSummaries fetches the templates in their compact form.
func ListTemplateSummaries(ctx context.Context, c *client.Client) ([]TemplateSummary, error) {
	ts, err := c.ListTemplates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]TemplateSummary, 0, len(ts))
	for _, t := range ts {
		s := TemplateSummary{Kind: t.Kind, Name: t.Name, Description: t.Description, Image: t.Image, Ports: []string{}, ExposedEnv: []string{}}
		for _, p := range t.Ports {
			s.Ports = append(s.Ports, fmt.Sprintf("%s:%d/%s", p.Name, p.ContainerPort, p.Protocol))
		}
		for _, e := range t.Env {
			if e.Exposed {
				s.ExposedEnv = append(s.ExposedEnv, e.Key)
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out, nil
}

// DeployTemplate creates a database service from a template and deploys it. It never
// touches an existing service: a name already in the project is an error.
func DeployTemplate(ctx context.Context, c *client.Client, r TemplateRequest) (*DeployResult, error) {
	kind, err := TemplateKind(r.Template)
	if err != nil {
		return nil, err
	}
	name := strings.ToLower(strings.TrimSpace(r.Name))
	if name == "" {
		name = kind
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
	for _, s := range svcs {
		if s.Name == name {
			return nil, fmt.Errorf("project %s already has a service named %q (%s); pick another name or use `atoll redeploy %s`", p.Name, name, s.ID, name)
		}
	}
	serverID := ""
	if r.Server != "" {
		srv, err := c.ResolveServer(ctx, r.Server)
		if err != nil {
			return nil, err
		}
		if srv.ID != p.ServerID {
			serverID = srv.ID
		}
	}
	order := r.EnvOrder
	if len(order) == 0 {
		order = sortedKeys(r.Env)
	}
	var env []api.EnvVar
	for _, k := range order {
		env = append(env, api.EnvVar{Key: k, Value: r.Env[k], Secret: looksSecret(k)})
	}
	svc, err := c.CreateServiceWithEnv(ctx, p.ID, api.Service{Name: name, Kind: kind, ServerID: serverID}, env)
	if err != nil {
		return nil, err
	}
	return &DeployResult{Action: "created", Service: *svc}, nil
}

func newTemplatesCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "templates", Aliases: []string{"template", "tpl"}, Short: "List and deploy one-click database templates"}
	ls := &cobra.Command{
		Use: "ls", Aliases: []string{"list"}, Short: "List database templates (postgres, mysql, redis, mongodb)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ts, err := ListTemplateSummaries(ctxOf(cmd), c)
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(ts)
			}
			tw := app.table()
			fmt.Fprintln(tw, "KIND\tNAME\tIMAGE\tPORTS\tDESCRIPTION")
			for _, t := range ts {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.Kind, t.Name, t.Image, strings.Join(t.Ports, ","), t.Description)
			}
			return tw.Flush()
		},
	}
	deploy := newTemplateDeployCmd(app, "deploy TEMPLATE [NAME] -p PROJECT [--env K=V] [--server SERVER]", 1)
	cmd.AddCommand(ls, deploy)
	return cmd
}

// newTemplateDeployCmd is `atoll templates deploy`; minArgs is 1 there (TEMPLATE [NAME]).
func newTemplateDeployCmd(app *App, use string, minArgs int) *cobra.Command {
	var project, server string
	var envs []string
	cmd := &cobra.Command{
		Use:   use,
		Short: "Create and deploy a database service from a template (postgres, mysql, redis, mongodb)",
		Long: "Creates a new service from a database template with a random password, a volume and\n" +
			"exposed connection variables (e.g. ${POSTGRES_CONNECTION_STRING}) other services can reference.\n" +
			"NAME defaults to the template kind. --env overrides template variables (e.g. POSTGRES_DATABASE=app).\n" +
			"Needs the deploy permission.",
		Args: cobra.RangeArgs(minArgs, minArgs+1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if project == "" {
				return fmt.Errorf("-p PROJECT is required")
			}
			name := ""
			if len(args) > minArgs {
				name = args[minArgs]
			}
			return runTemplateDeploy(cmd, app, TemplateRequest{Project: project, Template: args[0], Name: name, Server: server}, envs)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&project, "project", "p", "", "project name or id")
	f.StringArrayVarP(&envs, "env", "e", nil, "KEY=VALUE overriding a template variable, repeatable")
	f.StringVar(&server, "server", "", "server name or id (default: the project's server)")
	return cmd
}

func runTemplateDeploy(cmd *cobra.Command, app *App, req TemplateRequest, envs []string) error {
	c, err := app.client()
	if err != nil {
		return err
	}
	m, order, err := parseKV(envs)
	if err != nil {
		return err
	}
	req.Env, req.EnvOrder = m, order
	res, err := DeployTemplate(ctxOf(cmd), c, req)
	if err != nil {
		return err
	}
	if app.JSON {
		return app.printJSON(res)
	}
	fmt.Fprintf(app.Out, "created %s service %s (%s) with %s\n", res.Service.Kind, res.Service.Name, res.Service.ID, res.Service.Image)
	return nil
}
