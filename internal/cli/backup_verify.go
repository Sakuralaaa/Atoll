package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/client"
)

// Backup verification: `atoll backup verify|check|drill`.

func newBackupVerifyCmd(app *App) *cobra.Command {
	var project string
	var history int
	cmd := &cobra.Command{
		Use:   "verify [SERVICE] [--history N]",
		Short: "Show the latest repository check and restore drill, and when the next ones run",
		Long: "The repository check (restic check --read-data-subset, weekly, one server at a time) proves the\n" +
			"bucket still holds readable data; the restore drill (monthly, per backup policy) restores the\n" +
			"newest snapshots into temporary resources and checks files come back and dumps load into a\n" +
			"temporary database of the same version. With SERVICE the service's drill is shown too.\n" +
			"Schedules and the check subset are set with PUT /settings (backup_check_schedule,\n" +
			"backup_drill_schedule, backup_check_subset).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			var svc *api.Service
			if len(args) == 1 {
				if svc, err = resolveBackupService(ctx, c, project, args[0]); err != nil {
					return err
				}
			}
			sid := ""
			if svc != nil {
				sid = svc.ID
			}
			st, err := c.BackupVerifyStatus(ctx, sid)
			if err != nil {
				return err
			}
			var list []api.BackupVerification
			if history > 0 {
				if list, err = c.ListBackupVerifications(ctx, "", sid, history); err != nil {
					return err
				}
			}
			if app.JSON {
				if history > 0 {
					return app.printJSON(map[string]any{"status": st, "history": list})
				}
				return app.printJSON(st)
			}
			if !st.RepoConfigured {
				fmt.Fprintln(app.Out, "No backup target configured yet (panel: Settings → backup storage).")
			}
			sched := func(expr string, next *time.Time) string {
				if expr == "" {
					return "off"
				}
				s := fmt.Sprintf("%q (%s)", expr, st.Timezone)
				if next != nil {
					s += ", next " + next.UTC().Format("2006-01-02 15:04 UTC")
				}
				return s
			}
			on := ""
			if st.CheckServerID != "" {
				on = " on " + st.CheckServerID
			}
			fmt.Fprintf(app.Out, "Repository check: %s, reads %s of the data%s\n", sched(st.CheckSchedule, st.NextCheckAt), st.CheckSubset, on)
			fmt.Fprintf(app.Out, "  last: %s\n", verifyLine(st.Check))
			if svc != nil {
				fmt.Fprintf(app.Out, "Restore drill of %s: %s\n", svc.Name, sched(st.DrillSchedule, st.NextDrillAt))
				fmt.Fprintf(app.Out, "  last: %s\n", verifyLine(st.Drill))
				if st.Drill != nil {
					for _, s := range st.Drill.Steps {
						fmt.Fprintf(app.Out, "    %s %s: %s\n", s.Part, s.Status, dashIf(s.Error, s.Detail))
					}
				}
			} else {
				fmt.Fprintf(app.Out, "Restore drills: %s (per backup policy; `atoll backup verify SERVICE` for one)\n", sched(st.DrillSchedule, nil))
			}
			if history > 0 {
				fmt.Fprintln(app.Out)
				return app.printVerifications(list)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name or id")
	cmd.Flags().IntVar(&history, "history", 0, "also list the last N checks and drills")
	return cmd
}

func dashIf(a, b string) string {
	if a != "" {
		return a
	}
	if b != "" {
		return b
	}
	return "-"
}

func verifyLine(v *api.BackupVerification) string {
	if v == nil {
		return "never"
	}
	s := fmt.Sprintf("%s %s (%s, %s", v.Status, ago(v.StartedAt), v.Trigger, v.ID)
	if v.DurationMs > 0 {
		s += ", " + (time.Duration(v.DurationMs) * time.Millisecond).Round(time.Second).String()
	}
	s += ")"
	if v.Error != "" {
		s += ": " + v.Error
	}
	for _, w := range v.Warnings {
		s += "\n    warning: " + w
	}
	return s
}

func (app *App) printVerifications(list []api.BackupVerification) error {
	tw := app.table()
	fmt.Fprintln(tw, "ID\tKIND\tSERVICE\tSTARTED\tTRIGGER\tSTATUS\tDETAIL")
	for _, v := range list {
		var details []string
		for _, s := range v.Steps {
			details = append(details, s.Part+": "+dashIf(s.Error, s.Detail))
		}
		status := v.Status
		if v.Error != "" && len(v.Steps) == 0 {
			details = append(details, v.Error)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", v.ID, v.Kind, dashIf(v.ServiceID, ""), ago(v.StartedAt), v.Trigger, status, dashIf(strings.Join(details, "; "), ""))
	}
	return tw.Flush()
}

// waitVerification polls until the verification finished (or ctx ends).
func waitVerification(ctx context.Context, c *client.Client, v *api.BackupVerification, every time.Duration) (*api.BackupVerification, error) {
	for {
		list, err := c.ListBackupVerifications(ctx, v.Kind, v.ServiceID, 20)
		if err != nil {
			return nil, err
		}
		for i := range list {
			if list[i].ID == v.ID && list[i].Status != "running" {
				return &list[i], nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(every):
		}
	}
}

func (app *App) reportVerification(ctx context.Context, c *client.Client, v *api.BackupVerification, wait bool, what string) error {
	if wait {
		fmt.Fprintf(app.Err, "%s %s started, waiting…\n", what, v.ID)
		var err error
		if v, err = waitVerification(ctx, c, v, 3*time.Second); err != nil {
			return err
		}
	}
	if app.JSON {
		return app.printJSON(v)
	}
	if !wait {
		fmt.Fprintf(app.Out, "%s %s started on %s; follow with `atoll backup verify --history 5`\n", what, v.ID, v.ServerID)
		return nil
	}
	fmt.Fprintf(app.Out, "%s %s: %s\n", what, v.ID, verifyLine(v))
	for _, s := range v.Steps {
		fmt.Fprintf(app.Out, "  %s %s: %s\n", s.Part, s.Status, dashIf(s.Error, s.Detail))
	}
	if v.Status == "failed" {
		return fmt.Errorf("%s failed", strings.ToLower(what))
	}
	return nil
}

func newBackupCheckCmd(app *App) *cobra.Command {
	var server, subset string
	var wait bool
	cmd := &cobra.Command{
		Use:   "check [--server SERVER] [--subset 5%] [--wait]",
		Short: "Check the backup repository now (restic check --read-data-subset)",
		Long: "Reads a share of the repository's data and verifies it. The panel picks the server that runs the\n" +
			"scheduled check unless --server is given. --subset: a percentage (5%), a fraction (1/20) or a\n" +
			"size (500M); default the configured one. --wait waits for the result (exit 1 when it failed).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			sid := ""
			if server != "" {
				srv, err := c.ResolveServer(ctx, server)
				if err != nil {
					return err
				}
				sid = srv.ID
			}
			v, err := c.RunBackupCheck(ctx, sid, subset)
			if err != nil {
				return err
			}
			return app.reportVerification(ctx, c, v, wait, "Repository check")
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "run on this server (name or id)")
	cmd.Flags().StringVar(&subset, "subset", "", "share of the data to read (5%, 1/20, 500M)")
	cmd.Flags().BoolVar(&wait, "wait", false, "wait for the result")
	return cmd
}

func newBackupDrillCmd(app *App) *cobra.Command {
	var project string
	var wait bool
	cmd := &cobra.Command{
		Use:   "drill SERVICE [--wait]",
		Short: "Rehearse a restore of a service now (temporary resources only)",
		Long: "Restores the service's newest snapshots on its server into a temporary directory (volumes: files\n" +
			"must come back, non-empty) and a temporary container of the same image (dumps: the database must\n" +
			"load the dump and answer a query), then removes everything. The running service and its data\n" +
			"are not touched. --wait waits for the result (exit 1 when it failed).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			s, err := resolveBackupService(ctx, c, project, args[0])
			if err != nil {
				return err
			}
			v, err := c.RunBackupDrill(ctx, s.ID)
			if err != nil {
				return err
			}
			return app.reportVerification(ctx, c, v, wait, "Restore drill")
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name or id")
	cmd.Flags().BoolVar(&wait, "wait", false, "wait for the result")
	return cmd
}
