package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newServiceMigrateCmd(app *App) *cobra.Command {
	var to, project string
	cmd := &cobra.Command{
		Use:   "migrate SERVICE --to SERVER [-p PROJECT]",
		Short: "Move one service (container + volumes) to another server; needs the migrate permission",
		Long: "Moves a single service to SERVER. The service stops during the data copy; the rest of the\n" +
			"project stays where it is and reaches the moved service over the tailnet mesh under the same\n" +
			"name, so both servers must be on the tailnet (`atoll servers network join SERVER`).\n" +
			"Runs immediately when the token holds the migrate permission (a token with require_approval\n" +
			"waits for approval in the panel). To move a whole project use `atoll migrate`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				return fmt.Errorf("--to SERVER is required")
			}
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			s, err := c.ResolveService(ctx, project, args[0])
			if err != nil {
				return err
			}
			srv, err := c.ResolveServer(ctx, to)
			if err != nil {
				return err
			}
			if s.ServerID == srv.ID {
				return fmt.Errorf("service %s already runs on %s", s.Name, srv.Name)
			}
			job, err := c.MigrateService(ctx, s.ID, srv.ID)
			ok, err := app.handle(ctx, c, err)
			if err != nil {
				return err
			}
			if app.JSON {
				if ok && job != nil && job.ID != "" {
					return app.printJSON(job)
				}
				return app.printJSON(map[string]string{"service": s.ID, "to_server_id": srv.ID, "status": "started"})
			}
			if job != nil && job.ID != "" {
				fmt.Fprintf(app.Out, "Moving %s to %s (migration %s, %s)\n", s.Name, srv.Name, job.ID, job.Status)
			} else {
				fmt.Fprintf(app.Out, "Moving %s to %s started\n", s.Name, srv.Name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "target server name or id")
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name or id (to disambiguate service names)")
	return cmd
}

func newServerNetworkCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Private network (Tailscale) actions for a server; needs the servers permission",
		Long: "  atoll servers network join SERVER     install Tailscale if needed, join the tailnet with the stored\n" +
			"                                        auth key, then connect the agent over the tailnet\n" +
			"  atoll servers network switch SERVER   already on the tailnet: connect the agent over it\n" +
			"  atoll servers network public SERVER   connect the agent over the public panel URL again\n" +
			"The new control URL is verified by the agent before it switches; on failure the old one stays.\n" +
			"Enable the Tailscale network provider in the panel settings first.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown network action %q (want join, switch or public)", args[0])
			}
			return cmd.Help()
		},
	}
	for _, a := range []struct{ action, short string }{
		{"join", "Join the tailnet (installs Tailscale if needed) and switch the agent to it"},
		{"switch", "Switch the agent's control URL to the tailnet (node already on the tailnet)"},
		{"public", "Switch the agent back to the public panel URL"},
	} {
		action := a.action
		cmd.AddCommand(&cobra.Command{
			Use: action + " SERVER", Short: a.short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := app.client()
				if err != nil {
					return err
				}
				ctx := ctxOf(cmd)
				srv, err := c.ResolveServer(ctx, args[0])
				if err != nil {
					return err
				}
				if action == "join" && !app.JSON {
					fmt.Fprintf(app.Err, "Joining %s to the tailnet (installing Tailscale can take a few minutes)…\n", srv.Name)
				}
				res, err := c.ServerNetwork(ctx, srv.ID, action)
				if _, err := app.handle(ctx, c, err); err != nil {
					return err
				}
				if app.JSON {
					return app.printJSON(res)
				}
				switched := "not switched"
				if res.Switched {
					switched = "switched"
				}
				fmt.Fprintf(app.Out, "%s: %s, control URL %s (%s)", srv.Name, res.State, res.ControlURL, switched)
				if res.TailnetIP != "" {
					fmt.Fprintf(app.Out, ", tailnet IP %s", res.TailnetIP)
				}
				fmt.Fprintln(app.Out)
				if res.Message != "" {
					fmt.Fprintln(app.Out, res.Message)
				}
				return nil
			},
		})
	}
	return cmd
}
