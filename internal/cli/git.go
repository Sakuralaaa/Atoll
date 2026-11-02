package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/client"
)

// GitFlags are the `atoll deploy --git` flags (builds from source).
type GitFlags struct {
	Repo       string
	Branch     string
	Context    string
	Dockerfile string
	BuildArgs  []string
	// TokenEnv names the environment variable holding a GitHub token for a private repo.
	// The token itself is never accepted as a flag value (it would end up in shell history
	// and process listings).
	TokenEnv string
}

var gitBuildFlagNames = []string{"branch", "context", "dockerfile", "build-arg", "git-token-env"}

func (g *GitFlags) register(f *pflag.FlagSet) {
	f.StringVar(&g.Repo, "git", "", "build from this GitHub repository (https://github.com/OWNER/REPO) instead of pulling an image")
	f.StringVar(&g.Branch, "branch", "", "git branch to build (default main)")
	f.StringVar(&g.Context, "context", "", "build context: subdirectory of the repository (default: root)")
	f.StringVar(&g.Dockerfile, "dockerfile", "", "Dockerfile path inside the context (default Dockerfile)")
	f.StringArrayVar(&g.BuildArgs, "build-arg", nil, "docker build-arg KEY=VALUE, repeatable (not secret: visible in the image history)")
	f.StringVar(&g.TokenEnv, "git-token-env", "", "name of an environment variable holding a GitHub token for a private repository")
}

// set reports whether any build flag other than --git was given.
func (g *GitFlags) set(cmd *cobra.Command) bool {
	for _, n := range gitBuildFlagNames {
		if f := cmd.Flags().Lookup(n); f != nil && f.Changed {
			return true
		}
	}
	return false
}

// Source builds the requested git source; getenv reads --git-token-env.
func (g *GitFlags) Source(getenv func(string) string) (*api.GitSource, error) {
	repo := strings.TrimSpace(g.Repo)
	if repo == "" {
		return nil, fmt.Errorf("--git URL is required")
	}
	if !strings.HasPrefix(repo, "https://github.com/") {
		return nil, fmt.Errorf("--git must be https://github.com/OWNER/REPO")
	}
	if strings.Contains(strings.TrimPrefix(repo, "https://"), "@") {
		return nil, fmt.Errorf("--git must not contain credentials; use --git-token-env VAR for a private repository")
	}
	src := &api.GitSource{Repo: repo, Branch: strings.TrimSpace(g.Branch), Context: strings.TrimSpace(g.Context), Dockerfile: strings.TrimSpace(g.Dockerfile)}
	if len(g.BuildArgs) > 0 {
		m, _, err := parseKV(g.BuildArgs)
		if err != nil {
			return nil, fmt.Errorf("--build-arg: %w", err)
		}
		src.BuildArgs = m
	}
	if g.TokenEnv != "" {
		if strings.HasPrefix(g.TokenEnv, "ghp_") || strings.HasPrefix(g.TokenEnv, "github_pat_") || strings.HasPrefix(g.TokenEnv, "gho_") {
			return nil, fmt.Errorf("--git-token-env takes the NAME of an environment variable, not the token itself")
		}
		tok := strings.TrimSpace(getenv(g.TokenEnv))
		if tok == "" {
			return nil, fmt.Errorf("environment variable %s (from --git-token-env) is empty or not set", g.TokenEnv)
		}
		src.Token = tok
	}
	return src, nil
}

// repoName derives a service name from a repository URL.
func repoName(repo string) string {
	r := strings.TrimRight(strings.TrimSpace(repo), "/")
	r = strings.TrimSuffix(r, ".git")
	if i := strings.LastIndex(r, "/"); i >= 0 {
		r = r[i+1:]
	}
	r = strings.ToLower(r)
	r = strings.NewReplacer("_", "-", ".", "-").Replace(r)
	return strings.Trim(r, "-")
}

func normRepo(r string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(r), "/"), ".git"))
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// gitChanged reports whether want differs from the service's current source. Only the
// fields set in want are compared (the server keeps omitted ones); a token always counts
// as a change since the stored one cannot be read back.
func gitChanged(cur, want *api.GitSource) bool {
	if cur == nil {
		return true
	}
	if normRepo(cur.Repo) != normRepo(want.Repo) || want.Token != "" {
		return true
	}
	if want.Branch != "" && want.Branch != orDefault(cur.Branch, "main") {
		return true
	}
	if want.Context != "" && strings.Trim(want.Context, "/") != strings.Trim(cur.Context, "/") {
		return true
	}
	if want.Dockerfile != "" && want.Dockerfile != orDefault(cur.Dockerfile, "Dockerfile") {
		return true
	}
	if want.BuildArgs != nil {
		if len(want.BuildArgs) != len(cur.BuildArgs) {
			return true
		}
		for k, v := range want.BuildArgs {
			if cv, ok := cur.BuildArgs[k]; !ok || cv != v {
				return true
			}
		}
	}
	return false
}

// gitPatch is the partial `git` object for PATCH /services/{id}: only the fields given.
func gitPatch(g *api.GitSource) map[string]any {
	m := map[string]any{"repo": g.Repo}
	if g.Branch != "" {
		m["branch"] = g.Branch
	}
	if g.Context != "" {
		m["context"] = g.Context
	}
	if g.Dockerfile != "" {
		m["dockerfile"] = g.Dockerfile
	}
	if g.BuildArgs != nil {
		m["build_args"] = g.BuildArgs
	}
	if g.Token != "" {
		m["token"] = g.Token
	}
	return m
}

func printGitDeploy(app *App, res *DeployResult, git *api.GitSource) {
	branch := orDefault(git.Branch, "main")
	fmt.Fprintf(app.Out, "%s service %s (%s) from %s@%s; it builds on its server\n", res.Action, res.Service.Name, res.Service.ID, git.Repo, branch)
	if wh := res.Service.Webhook; wh != nil {
		printWebhook(app, wh)
		if wh.Secret != "" {
			fmt.Fprintln(app.Out, "The secret is shown only this once; store it in the GitHub webhook now.")
		}
	}
	fmt.Fprintf(app.Out, "Follow the build: atoll logs %s --build -f\n", res.Service.Name)
}

func printWebhook(app *App, wh *api.GitWebhook) {
	fmt.Fprintln(app.Out, "GitHub push webhook (repo → Settings → Webhooks → Add webhook):")
	tw := app.table()
	fmt.Fprintf(tw, "  Payload URL\t%s\n", wh.URL)
	fmt.Fprintf(tw, "  Content type\t%s\n", orDefault(wh.ContentType, "application/json"))
	if wh.Secret != "" {
		fmt.Fprintf(tw, "  Secret\t%s\n", wh.Secret)
	}
	fmt.Fprintf(tw, "  Events\t%s\n", orDefault(wh.Events, "push"))
	if wh.Branch != "" {
		fmt.Fprintf(tw, "  Branch\t%s (pushes to other branches are ignored)\n", wh.Branch)
	}
	tw.Flush()
}

// ---------- services webhook ----------

func newServiceWebhookCmd(app *App) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "webhook SERVICE [-p PROJECT]",
		Short: "Show the GitHub push webhook of a service built from git",
		Long: "Shows the webhook URL, branch and events to configure on GitHub. The secret is printed once\n" +
			"when the git service is created; afterwards only a panel session can see or rotate it.",
		Args: cobra.ExactArgs(1),
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
			wh, err := c.ServiceWebhook(ctx, s.ID)
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(wh)
			}
			printWebhook(app, wh)
			if wh.Secret == "" {
				state := "a secret is set"
				if !wh.HasSecret {
					state = "no secret is set"
				}
				fmt.Fprintf(app.Out, "Secret: not shown (%s). It is shown once at creation and otherwise only in a panel session; rotate it in the panel if it was lost.\n", state)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name or id (to disambiguate service names)")
	return cmd
}

// ---------- build logs ----------

// buildPoll is how often `atoll logs --build -f` polls for new lines.
var buildPoll = 2 * time.Second

// deploymentDone reports whether a deployment status is final (no more build lines).
func deploymentDone(status string) bool {
	switch status {
	case "running", "failed", "superseded", "stopped":
		return true
	}
	return false
}

// newestDeployment picks the deployment to show when none is named.
func newestDeployment(ctx context.Context, c *client.Client, s *api.Service) (string, error) {
	deps, err := c.ListDeployments(ctx, s.ID)
	if err != nil {
		return "", err
	}
	if len(deps) == 0 {
		return "", fmt.Errorf("service %s has no deployments", s.Name)
	}
	best := deps[0]
	for _, d := range deps[1:] {
		if d.StartedAt.After(best.StartedAt) {
			best = d
		}
	}
	return best.ID, nil
}

func printBuildLogs(ctx context.Context, app *App, c *client.Client, s *api.Service, dep string, follow bool) error {
	if dep == "" {
		var err error
		if dep, err = newestDeployment(ctx, c, s); err != nil {
			return err
		}
	}
	var after int64
	first := true
	for {
		l, err := c.DeploymentLogs(ctx, s.ID, dep, after)
		if err != nil {
			return err
		}
		if first && !app.JSON {
			fmt.Fprintf(app.Err, "build log of %s (%s), status %s\n", dep, s.Name, l.Status)
			if l.Truncated {
				fmt.Fprintln(app.Err, "(older lines were dropped; only the newest are kept)")
			}
		}
		first = false
		for _, ln := range l.Lines {
			if ln.Seq > after {
				after = ln.Seq
			}
			if app.JSON {
				if err := app.printJSON(ln); err != nil {
					return err
				}
				continue
			}
			ts := ""
			if !ln.At.IsZero() {
				ts = ln.At.Local().Format("15:04:05") + " "
			}
			fmt.Fprintln(app.Out, ts+ln.Line)
		}
		if !follow || deploymentDone(l.Status) {
			if follow && !app.JSON {
				fmt.Fprintf(app.Err, "deployment %s: %s\n", dep, l.Status)
			}
			if follow && l.Status == "failed" {
				return fmt.Errorf("deployment %s failed", dep)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(buildPoll):
		}
	}
}

// BuildLogs is the MCP view of a deployment's build log.
type BuildLogs struct {
	Service      string `json:"service"`
	DeploymentID string `json:"deployment_id"`
	Status       string `json:"status"`
	Truncated    bool   `json:"truncated,omitempty"`
	LastSeq      int64  `json:"last_seq"`
	Log          string `json:"log"`
}

// GetBuildLogs reads a deployment's build log (newest deployment when dep is empty),
// keeping at most tail lines.
func GetBuildLogs(ctx context.Context, c *client.Client, s *api.Service, dep string, after int64, tail int) (*BuildLogs, error) {
	if dep == "" {
		var err error
		if dep, err = newestDeployment(ctx, c, s); err != nil {
			return nil, err
		}
	}
	l, err := c.DeploymentLogs(ctx, s.ID, dep, after)
	if err != nil {
		return nil, err
	}
	out := &BuildLogs{Service: s.Name, DeploymentID: dep, Status: l.Status, Truncated: l.Truncated, LastSeq: after}
	lines := l.Lines
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
		out.Truncated = true
	}
	var b strings.Builder
	for _, ln := range l.Lines {
		if ln.Seq > out.LastSeq {
			out.LastSeq = ln.Seq
		}
	}
	for _, ln := range lines {
		b.WriteString(ln.Line)
		b.WriteByte('\n')
	}
	out.Log = b.String()
	if out.Log == "" {
		out.Log = "(no build log lines)"
	}
	return out, nil
}
