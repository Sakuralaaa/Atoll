package cli

// CLI: actions, rollback, exec, domains and migrate.

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/client"
)

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
			rw, err := app.beforeDeploy(ctx, c, s)
			if err != nil {
				return err
			}
			if _, err := app.handle(ctx, c, c.ServiceAction(ctx, s.ID, action)); err != nil {
				return err
			}
			if app.WaitReady {
				switch action {
				case "redeploy":
					if _, err := rw.wait(ctx); err != nil {
						return err
					}
				case "start", "restart":
					if err := app.waitRunning(ctx, c, s); err != nil {
						return err
					}
				}
			}
			if app.JSON {
				return app.printJSON(map[string]string{"service": s.ID, "action": action, "status": "ok"})
			}
			if app.WaitReady && action != "stop" {
				fmt.Fprintf(app.Out, "%s: %s done, running\n", s.Name, action)
				return nil
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
			rw, err := app.beforeDeploy(ctx, c, s)
			if err != nil {
				return err
			}
			if _, err := app.handle(ctx, c, c.Rollback(ctx, s.ID, depID)); err != nil {
				return err
			}
			if _, err := rw.wait(ctx); err != nil {
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
