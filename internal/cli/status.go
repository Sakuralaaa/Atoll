package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// problemsFoundError: `atoll status --check` found firing problems (exit 5).
type problemsFoundError struct{ n int }

func (e *problemsFoundError) Error() string { return fmt.Sprintf("%d problem(s)", e.n) }

var problemKindLabel = map[string]string{
	api.ProblemNodeOffline:        "server offline",
	api.ProblemDiskHigh:           "disk almost full",
	api.ProblemServiceDown:        "service down",
	api.ProblemDeployFailed:       "deploy failed",
	api.ProblemBackupFailed:       "backup failed",
	api.ProblemBackupVerifyFailed: "backup verification failed",
	api.ProblemDomainError:        "domain error",
}

func newStatusCmd(app *App) *cobra.Command {
	var resolved, check bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show servers and current problems (offline servers, full disks, failing services, failed deploys/backups)",
		Long: "Prints how many servers are online and every problem the control server is watching.\n" +
			"A problem shows up only after it held for a while (e.g. a server offline for 2 minutes).\n" +
			"  atoll status              servers + problems\n" +
			"  atoll status --resolved   also problems resolved in the last 24 hours\n" +
			"  atoll status --check      exit 5 when there is any problem (for cron / CI)",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			servers, err := c.ListServers(ctx)
			if err != nil {
				return err
			}
			probs, err := c.Problems(ctx, resolved)
			if err != nil {
				return err
			}
			firing := 0
			for _, p := range probs {
				if p.ResolvedAt == nil {
					firing++
				}
			}
			if app.JSON {
				if err := app.printJSON(map[string]any{"servers": servers, "problems": probs}); err != nil {
					return err
				}
			} else {
				online, workers := 0, 0
				for _, s := range servers {
					if s.Role == "control" {
						continue
					}
					workers++
					if s.Status == "online" {
						online++
					}
				}
				fmt.Fprintf(app.Out, "servers: %d/%d online\n", online, workers)
				if len(probs) == 0 {
					fmt.Fprintln(app.Out, "no problems")
				} else {
					fmt.Fprintf(app.Out, "problems: %d\n\n", firing)
					w := app.table()
					fmt.Fprintln(w, "KIND\tSUBJECT\tSINCE\tDETAIL")
					for _, p := range probs {
						kind := problemKindLabel[p.Kind]
						if kind == "" {
							kind = p.Kind
						}
						since := ago(p.Since)
						if p.ResolvedAt != nil {
							since = "resolved " + ago(*p.ResolvedAt)
						}
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", kind, p.SubjectName, since, p.Message)
					}
					w.Flush()
				}
			}
			if check && firing > 0 {
				return &problemsFoundError{n: firing}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&resolved, "resolved", false, "include problems resolved in the last 24 hours")
	cmd.Flags().BoolVar(&check, "check", false, "exit with code 5 when any problem is firing")
	return cmd
}
