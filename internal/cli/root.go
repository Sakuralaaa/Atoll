// Package cli implements the `atoll` command: REST client commands, `atoll mcp`
// (MCP over stdio), the in-place Zeabur takeover (`atoll zeabur ...`) and the optional
// Zeabur-CLI based helpers (`atoll import zeabur ...`).
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/client"
)

// App carries global state for every command.
type App struct {
	Version string
	In      io.Reader
	Out     io.Writer
	Err     io.Writer

	JSON        bool
	URL         string
	Token       string
	ConfigPath  string
	Wait        bool
	WaitTimeout time.Duration

	// NewClient may be replaced in tests.
	NewClient func(url, token string) *client.Client
}

// Execute runs the CLI and returns the process exit code.
func Execute(version string) int {
	app := &App{Version: version, In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
	root := NewRoot(app)
	if err := root.Execute(); err != nil {
		var pe *pendingError
		if errors.As(err, &pe) {
			fmt.Fprintln(app.Err, pe.Error())
			return 3
		}
		fmt.Fprintln(app.Err, "Error:", err)
		return 1
	}
	return 0
}

// NewRoot builds the command tree.
func NewRoot(app *App) *cobra.Command {
	if app.NewClient == nil {
		app.NewClient = client.New
	}
	root := &cobra.Command{
		Use:           "atoll",
		Short:         "Atoll CLI: deploy and manage services on your Atoll servers",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(app.In)
	root.SetOut(app.Out)
	root.SetErr(app.Err)
	pf := root.PersistentFlags()
	pf.BoolVar(&app.JSON, "json", false, "print machine-readable JSON")
	pf.StringVar(&app.URL, "url", "", "Atoll panel URL (default from config / $ATOLL_URL)")
	pf.StringVar(&app.Token, "token", "", "API token (default from config / $ATOLL_TOKEN)")
	pf.StringVar(&app.ConfigPath, "config", "", "config file (default ~/.config/atoll/config.json)")
	pf.BoolVar(&app.Wait, "wait", false, "when an action needs approval in the panel, wait until it is approved")
	pf.DurationVar(&app.WaitTimeout, "wait-timeout", 15*time.Minute, "how long --wait waits")

	root.AddCommand(
		newLoginCmd(app), newVersionCmd(app), newServersCmd(app), newProjectsCmd(app),
		newServicesCmd(app), newDeployCmd(app), newEnvCmd(app), newLogsCmd(app),
		newActionCmd(app, "restart", "Restart a service's container"),
		newActionCmd(app, "stop", "Stop a service"),
		newActionCmd(app, "start", "Start a stopped service"),
		newActionCmd(app, "redeploy", "Redeploy a service (pull image again, recreate container)"),
		newRollbackCmd(app), newDomainCmd(app), newMigrateCmd(app), newExecCmd(app),
		newMCPCmd(app), newZeaburCmd(app), newImportCmd(app),
	)
	return root
}

func newVersionCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if app.JSON {
				return app.printJSON(map[string]string{"version": app.Version})
			}
			fmt.Fprintln(app.Out, "atoll", app.Version)
			return nil
		},
	}
}

// client returns an authenticated client from flags, env and config.
func (app *App) client() (*client.Client, error) {
	cfg, _ := LoadConfig(app.ConfigPath)
	url, token := cfg.URL, cfg.Token
	if v := os.Getenv("ATOLL_URL"); v != "" {
		url = v
	}
	if v := os.Getenv("ATOLL_TOKEN"); v != "" {
		token = v
	}
	if app.URL != "" {
		url = app.URL
	}
	if app.Token != "" {
		token = app.Token
	}
	if url == "" || token == "" {
		return nil, errors.New("not logged in: run `atoll login --url https://your-panel --token atl_pat_...` (create a token in the panel under Settings → API tokens)")
	}
	c := app.NewClient(url, token)
	c.UserAgent = "atoll-cli/" + app.Version
	return c, nil
}

func (app *App) printJSON(v any) error {
	enc := json.NewEncoder(app.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (app *App) table() *tabwriter.Writer {
	return tabwriter.NewWriter(app.Out, 2, 4, 2, ' ', 0)
}

// pendingError is returned when an action waits for approval in the panel (exit code 3).
type pendingError struct{ msg string }

func (e *pendingError) Error() string { return e.msg }

// handle turns a confirmation-required error into a friendly message, optionally waiting.
// It returns (approved, err): approved=true means the action went through (now or after wait).
func (app *App) handle(ctx context.Context, c *client.Client, err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	cf, ok := client.AsConfirmation(err)
	if !ok {
		return false, err
	}
	if app.JSON && !app.Wait {
		app.printJSON(map[string]any{"pending_confirmation": cf})
		return false, &pendingError{msg: err.Error()}
	}
	if !app.Wait {
		return false, &pendingError{msg: err.Error()}
	}
	fmt.Fprintf(app.Err, "Waiting for approval of %s (%s) in the Atoll panel…\n", cf.ID, cf.Action)
	wctx, cancel := context.WithTimeout(ctx, app.WaitTimeout)
	defer cancel()
	got, werr := c.WaitConfirmation(wctx, cf.ID, 3*time.Second)
	if werr != nil {
		return false, werr
	}
	fmt.Fprintf(app.Err, "Approved (%s).\n", got.Status)
	return true, nil
}

// parseKV parses K=V pairs.
func parseKV(pairs []string) (map[string]string, []string, error) {
	m := map[string]string{}
	var order []string
	for _, p := range pairs {
		i := strings.Index(p, "=")
		if i <= 0 {
			return nil, nil, fmt.Errorf("%q: want KEY=VALUE", p)
		}
		k := p[:i]
		if _, seen := m[k]; !seen {
			order = append(order, k)
		}
		m[k] = p[i+1:]
	}
	return m, order, nil
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
