package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func newControlCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "control",
		Short: "Show the control server's own status (中控)",
		Long: "Version, uptime, host, resources, agents, last control backup and the latest release of\n" +
			"atoll-server itself (needs the read permission). Logs, audit, backups, export, update and\n" +
			"rollback are panel-only (a browser session with re-authentication), not API tokens.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			st, err := c.ControlStatus(ctxOf(cmd))
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(st)
			}
			w := app.Out
			ver := st.Version
			if st.Commit != "" {
				ver += " (" + st.Commit + ")"
			}
			fmt.Fprintf(w, "atoll-server  %s, up %s\n", ver, (time.Duration(st.UptimeSeconds) * time.Second).String())
			fmt.Fprintf(w, "host          %s · %s · kernel %s · %s · %d cores\n", st.Host.Hostname, st.Host.OS, st.Host.Kernel, st.Host.Arch, st.Host.CPUCores)
			fmt.Fprintf(w, "cpu / load    %.1f%% · %.2f %.2f %.2f\n", st.CPUPct, st.Load1, st.Load5, st.Load15)
			fmt.Fprintf(w, "memory        %d / %d MB\n", st.MemUsedMB, st.MemTotalMB)
			fmt.Fprintf(w, "disk (data)   %.1f / %.1f GB · data dir %s · database %s\n", st.DiskUsedGB, st.DiskTotalGB, humanBytes(st.DataDirBytes), humanBytes(st.DatabaseBytes))
			fmt.Fprintf(w, "public URL    %s\n", st.PublicURL)
			if st.Tailnet != nil {
				fmt.Fprintf(w, "tailnet       %s %s\n", st.Tailnet.IP, st.Tailnet.ControlURL)
			}
			fmt.Fprintf(w, "agents        %d of %d connected\n", st.AgentsConnected, st.AgentsTotal)
			if b := st.LastBackup; b != nil {
				where := "local"
				if b.Remote {
					where = "local + bucket"
				}
				fmt.Fprintf(w, "last backup   %s (%s, %s, %s)\n", b.CreatedAt.Local().Format(time.DateTime), b.Trigger, humanBytes(b.SizeBytes), where)
			} else {
				fmt.Fprintln(w, "last backup   none yet")
			}
			switch {
			case st.UpdateInProgress:
				fmt.Fprintf(w, "update        in progress (%s)\n", st.UpdatePhase)
			case st.LatestRelease != nil && st.UpdateAvailable:
				fmt.Fprintf(w, "update        %s available (update from the panel: 中控)\n", st.LatestRelease.Tag)
			case st.LatestRelease != nil:
				fmt.Fprintf(w, "update        up to date (latest %s)\n", st.LatestRelease.Tag)
			case st.ReleaseError != "":
				fmt.Fprintf(w, "update        release check failed: %s\n", st.ReleaseError)
			}
			if u := st.LastUpdate; u != nil && !st.UpdateInProgress {
				res := "ok"
				if !u.OK {
					res = "failed: " + u.Error
				}
				fmt.Fprintf(w, "last %-8s %s -> %s %s\n", u.Mode, u.FromVersion, u.ToVersion, res)
			}
			return nil
		},
	}
	return cmd
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
