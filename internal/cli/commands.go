package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

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
			fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tIP\tCPU\tMEM\tDISK\tAGENT\tLAST SEEN")
			for _, s := range list {
				cpu, mem := "-", "-"
				if s.Usage != nil {
					cpu = fmt.Sprintf("%.0f%%", s.Usage.CPUPct)
					mem = fmt.Sprintf("%d/%dMB", s.Usage.MemUsedMB, s.Usage.MemTotalMB)
				}
				agentV := s.AgentVersion
				if s.AgentLatest != "" && s.AgentVersion != s.AgentLatest {
					agentV += " → " + s.AgentLatest
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%dGB\t%s\t%s\n", s.ID, s.Name, s.Status, s.PublicIPv4, cpu, mem, s.DiskGB, agentV, ago(s.LastSeenAt))
			}
			return tw.Flush()
		},
	})
	cmd.AddCommand(newServerNetworkCmd(app))
	cmd.AddCommand(newUpgradeAgentCmd(app))
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

func newUpgradeAgentCmd(app *App) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "upgrade-agent [SERVER...] | --all",
		Short: "Upgrade the agent on servers to the control server's version (verified, rolls back if it does not reconnect)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) > 0) {
				return fmt.Errorf("name servers or pass --all")
			}
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			var ids []string
			for _, a := range args {
				s, err := c.ResolveServer(ctx, a)
				if err != nil {
					return err
				}
				ids = append(ids, s.ID)
			}
			rows, err := c.UpgradeAgents(ctx, ids)
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(rows)
			}
			if len(rows) == 0 {
				fmt.Fprintln(app.Out, "Every online agent is up to date.")
				return nil
			}
			tw := app.table()
			fmt.Fprintln(tw, "SERVER\tRESULT")
			failed := 0
			for _, r := range rows {
				if r.Upgrade != nil {
					fmt.Fprintf(tw, "%s\tupgrading %s → %s\n", r.ID, r.Upgrade.From, r.Upgrade.To)
				} else {
					failed++
					fmt.Fprintf(tw, "%s\t%s\n", r.ID, r.Error)
				}
			}
			tw.Flush()
			fmt.Fprintln(app.Out, "Agents restart in a few seconds; follow with `atoll servers ls`.")
			if failed > 0 {
				return fmt.Errorf("%d of %d servers were not upgraded", failed, len(rows))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "every online server whose agent is on another version")
	return cmd
}
