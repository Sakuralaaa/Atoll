package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// `atoll apps`: one-click apps (a blog with its database, tools…), each created as a new project.
func newAppsCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "apps", Aliases: []string{"app"}, Short: "List one-click apps and create a project from one"}
	ls := &cobra.Command{
		Use: "ls", Aliases: []string{"list"}, Short: "List one-click apps", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			as, err := c.ListAppTemplates(ctxOf(cmd))
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(as)
			}
			tw := app.table()
			fmt.Fprintln(tw, "ID\tNAME\tCATEGORY\tSERVICES\tDOMAIN\tDESCRIPTION")
			for _, a := range as {
				names := make([]string, 0, len(a.Services))
				for _, s := range a.Services {
					names = append(names, s.Name)
				}
				dom := "optional"
				if a.NeedsDomain {
					dom = "required"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", a.ID, a.Name, a.Category, strings.Join(names, ","), dom, a.DescriptionEn)
			}
			return tw.Flush()
		},
	}
	var name, server, domain string
	create := &cobra.Command{
		Use:   "create APP [-n PROJECT] [--server SERVER] [--domain HOST]",
		Short: "Create a project from a one-click app and deploy it",
		Long: "Creates a new project with the app's services (and its database), binds --domain to the\n" +
			"app when given, and deploys everything. Some apps need a domain (see `atoll apps ls`).\n" +
			"Needs the deploy permission, plus domains when --domain is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			req := api.AppTemplateRequest{Name: name, Hostname: domain}
			if server != "" {
				srv, err := c.ResolveServer(ctx, server)
				if err != nil {
					return err
				}
				req.ServerID = srv.ID
			}
			res, err := c.CreateFromAppTemplate(ctx, args[0], req)
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(res)
			}
			fmt.Fprintf(app.Out, "created project %s (%s) with %d services, deploying\n", res.Project.Name, res.Project.ID, len(res.Services))
			if res.Domain != nil {
				fmt.Fprintf(app.Out, "domain: https://%s\n", res.Domain.Hostname)
			}
			if res.DomainError != "" {
				fmt.Fprintf(app.Out, "domain not bound: %s\n", res.DomainError)
			}
			return nil
		},
	}
	f := create.Flags()
	f.StringVarP(&name, "name", "n", "", "project name (default: the app id)")
	f.StringVar(&server, "server", "", "server name or id (default: the only server)")
	f.StringVar(&domain, "domain", "", "hostname to bind to the app, e.g. blog.example.com")
	cmd.AddCommand(ls, create)
	return cmd
}
