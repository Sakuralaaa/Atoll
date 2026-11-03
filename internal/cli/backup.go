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

func newBackupCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Back up services to your S3 bucket (restic), list backups, restore",
		Long: "Backups run on each server's agent (restic, straight to the bucket configured under\n" +
			"Settings → backup storage), on the service's schedule, even while the panel is offline.\n" +
			"  atoll backup list SERVICE                 backup runs, newest first\n" +
			"  atoll backup run SERVICE                  back up now\n" +
			"  atoll backup restore SNAPSHOT [--to SERVER]   restore (needs the restore permission)\n" +
			"  atoll backup policy SERVICE [--enable|--disable] [--schedule CRON] [--keep-daily N ...]\n" +
			"  atoll backup verify [SERVICE] [--history N]   latest repository check / restore drill\n" +
			"  atoll backup check [--wait]                 check the repository now (restic check)\n" +
			"  atoll backup drill SERVICE [--wait]         rehearse a restore now (temporary resources)\n" +
			"SERVICE is a name, an id (svc_...) or project/name.",
	}
	cmd.AddCommand(newBackupListCmd(app), newBackupRunCmd(app), newBackupRestoreCmd(app), newBackupPolicyCmd(app),
		newBackupVerifyCmd(app), newBackupCheckCmd(app), newBackupDrillCmd(app))
	return cmd
}

// resolveBackupService accepts svc_..., NAME (with optional -p) or PROJECT/NAME.
func resolveBackupService(ctx context.Context, c *client.Client, project, ref string) (*api.Service, error) {
	if project == "" && !strings.HasPrefix(ref, "svc_") {
		if i := strings.Index(ref, "/"); i > 0 && i < len(ref)-1 {
			project, ref = ref[:i], ref[i+1:]
		}
	}
	return c.ResolveService(ctx, project, ref)
}

func newBackupListCmd(app *App) *cobra.Command {
	var project string
	var refresh bool
	cmd := &cobra.Command{
		Use:   "list SERVICE",
		Short: "List a service's backups (newest first)",
		Args:  cobra.ExactArgs(1),
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
			list, err := c.ListBackups(ctx, s.ID, refresh)
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(list)
			}
			if len(list) == 0 {
				fmt.Fprintf(app.Out, "%s: no backups yet. Run one with `atoll backup run %s`.\n", s.Name, s.Name)
				return nil
			}
			return app.printBackups(list)
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name or id")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "reconcile with the repository first (import snapshots the panel does not know)")
	return cmd
}

func (app *App) printBackups(list []api.Backup) error {
	tw := app.table()
	fmt.Fprintln(tw, "ID\tSTARTED\tTRIGGER\tSTATUS\tPARTS\tSIZE\tADDED\tDURATION\tSNAPSHOTS")
	for _, b := range list {
		var parts, snaps []string
		for _, sn := range b.Snapshots {
			parts = append(parts, sn.Part)
			id := sn.ID
			if len(id) > 8 {
				id = id[:8]
			}
			if sn.Pruned {
				id += "(pruned)"
			}
			snaps = append(snaps, id)
		}
		status := b.Status
		if b.Error != "" {
			status += ": " + b.Error
		}
		dur := "-"
		if b.DurationMs > 0 {
			dur = (time.Duration(b.DurationMs) * time.Millisecond).Round(time.Second).String()
		}
		dash := func(v []string) string {
			if len(v) == 0 {
				return "-"
			}
			return strings.Join(v, ",")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", b.ID, ago(b.StartedAt), b.Trigger, status,
			dash(parts), humanSize(b.Bytes), humanSize(b.Added), dur, dash(snaps))
	}
	return tw.Flush()
}

func newBackupRunCmd(app *App) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "run SERVICE",
		Short: "Back up a service now",
		Args:  cobra.ExactArgs(1),
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
			b, err := c.RunBackup(ctx, s.ID)
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(b)
			}
			fmt.Fprintf(app.Out, "%s: backup %s started; follow with `atoll backup list %s`\n", s.Name, b.ID, s.Name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name or id")
	return cmd
}

func newBackupRestoreCmd(app *App) *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "restore SNAPSHOT [--to SERVER]",
		Short: "Restore a backup (replaces the service's current data; needs the restore permission)",
		Long: "SNAPSHOT is a backup id (bak_...) or a restic snapshot id (full or 8-char) from `atoll backup list`.\n" +
			"Without --to the service is restored in place; the agent first keeps a safety copy of the\n" +
			"current data. --to SERVER is disaster recovery: only allowed while the project's server is\n" +
			"offline; the whole project moves to SERVER and this one service's data is restored there\n" +
			"(restore the project's other services from their own backups).\n" +
			"Needs the restore permission and runs immediately; a token created with --require-approval\n" +
			"instead creates a pending confirmation to approve in the panel (or use --wait).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			b, err := c.GetBackup(ctx, args[0])
			if err != nil {
				return err
			}
			target := ""
			if to != "" {
				srv, err := c.ResolveServer(ctx, to)
				if err != nil {
					return err
				}
				target = srv.ID
			}
			job, err := c.RestoreBackup(ctx, b.ID, target)
			_, cf := client.AsConfirmation(err)
			if _, err := app.handle(ctx, c, err); err != nil {
				return err
			}
			if cf {
				// approved after --wait: the job started server side; show the newest one
				if jobs, lerr := c.ListRestores(ctx, b.ServiceID); lerr == nil && len(jobs) > 0 {
					job = &jobs[0]
				} else {
					job = nil
				}
			}
			if app.JSON {
				if job == nil {
					return app.printJSON(map[string]string{"status": "approved", "backup_id": b.ID})
				}
				return app.printJSON(job)
			}
			if job == nil {
				fmt.Fprintf(app.Out, "Restore of %s approved.\n", b.ID)
				return nil
			}
			fmt.Fprintf(app.Out, "Restore %s of backup %s: %s\n", job.ID, b.ID, job.Status)
			for _, st := range job.Steps {
				fmt.Fprintf(app.Out, "  %s: %s\n", st.Name, st.Status)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "restore onto this server (name or id; only while the project's server is offline)")
	return cmd
}

func newBackupPolicyCmd(app *App) *cobra.Command {
	var project, schedule, timezone string
	var enable, disable bool
	var kd, kw, km int
	cmd := &cobra.Command{
		Use:   "policy SERVICE [--enable|--disable] [--schedule CRON] [--keep-daily N --keep-weekly N --keep-monthly N]",
		Short: "Show or change a service's backup schedule and retention",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if enable && disable {
				return fmt.Errorf("use either --enable or --disable")
			}
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			s, err := resolveBackupService(ctx, c, project, args[0])
			if err != nil {
				return err
			}
			p, err := c.GetBackupPolicy(ctx, s.ID)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			changed := false
			set := func(name string, apply func()) {
				if f.Changed(name) {
					apply()
					changed = true
				}
			}
			set("enable", func() { p.Enabled = true })
			set("disable", func() { p.Enabled = false })
			set("schedule", func() { p.Schedule = schedule })
			set("timezone", func() { p.Timezone = timezone })
			set("keep-daily", func() { p.Keep.Daily = kd })
			set("keep-weekly", func() { p.Keep.Weekly = kw })
			set("keep-monthly", func() { p.Keep.Monthly = km })
			if changed {
				if p, err = c.PutBackupPolicy(ctx, s.ID, *p); err != nil {
					return err
				}
			}
			if app.JSON {
				return app.printJSON(p)
			}
			state := "disabled"
			if p.Enabled {
				state = "enabled"
			}
			fmt.Fprintf(app.Out, "%s: backups %s, schedule %q (%s), keep daily %d / weekly %d / monthly %d\n",
				s.Name, state, p.Schedule, p.Timezone, p.Keep.Daily, p.Keep.Weekly, p.Keep.Monthly)
			if len(p.Parts) > 0 {
				fmt.Fprintf(app.Out, "  backs up: %s\n", strings.Join(p.Parts, " + "))
			}
			if p.NextRunAt != nil {
				fmt.Fprintf(app.Out, "  next run: %s\n", p.NextRunAt.UTC().Format("2006-01-02 15:04 UTC"))
			}
			if !p.RepoConfigured {
				fmt.Fprintln(app.Out, "  note: no backup target configured yet (panel: Settings → backup storage)")
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&project, "project", "p", "", "project name or id")
	f.BoolVar(&enable, "enable", false, "turn scheduled backups on")
	f.BoolVar(&disable, "disable", false, "turn scheduled backups off")
	f.StringVar(&schedule, "schedule", "", `5-field cron, e.g. "0 3 * * *"`)
	f.StringVar(&timezone, "timezone", "", "IANA zone the schedule is read in (default: the control server's zone)")
	f.IntVar(&kd, "keep-daily", 0, "daily snapshots to keep")
	f.IntVar(&kw, "keep-weekly", 0, "weekly snapshots to keep")
	f.IntVar(&km, "keep-monthly", 0, "monthly snapshots to keep")
	return cmd
}
