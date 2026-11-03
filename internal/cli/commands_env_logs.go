package cli

// CLI: env and logs.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/client"
)

// ---------- env ----------

func newEnvCmd(app *App) *cobra.Command {
	var svcRef, project string
	var secret, exposed, noRedeploy, replace bool
	cmd := &cobra.Command{Use: "env", Short: "List, set, unset and import service variables"}
	cmd.PersistentFlags().BoolVar(&noRedeploy, "no-redeploy", false, "only save the variables; deploy later (atoll redeploy)")
	cmd.PersistentFlags().StringVarP(&svcRef, "service", "s", "", "service name or id")
	cmd.PersistentFlags().StringVarP(&project, "project", "p", "", "project name or id (to disambiguate service names)")
	resolve := func(cmd *cobra.Command) (*client.Client, *api.Service, error) {
		c, err := app.client()
		if err != nil {
			return nil, nil, err
		}
		if svcRef == "" {
			return nil, nil, fmt.Errorf("-s SERVICE is required")
		}
		s, err := c.ResolveService(ctxOf(cmd), project, svcRef)
		return c, s, err
	}
	cmd.AddCommand(&cobra.Command{
		Use: "ls -s SERVICE", Aliases: []string{"list"}, Short: "List variables (secrets masked)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, s, err := resolve(cmd)
			if err != nil {
				return err
			}
			vars, err := c.GetEnv(ctxOf(cmd), s.ID)
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(vars)
			}
			tw := app.table()
			fmt.Fprintln(tw, "KEY\tVALUE\tFLAGS")
			for _, v := range vars {
				val := v.Value
				if v.Secret {
					val = "••••"
				}
				var flags []string
				if v.Secret {
					flags = append(flags, "secret")
				}
				if v.Exposed {
					flags = append(flags, "exposed")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", v.Key, val, strings.Join(flags, ","))
			}
			return tw.Flush()
		},
	})
	set := &cobra.Command{
		Use: "set -s SERVICE KEY=VALUE...", Short: "Set variables (redeploys the service)", Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, s, err := resolve(cmd)
			if err != nil {
				return err
			}
			m, order, err := parseKV(args)
			if err != nil {
				return err
			}
			var vars []api.EnvVar
			for _, k := range order {
				vars = append(vars, api.EnvVar{Key: k, Value: m[k], Secret: secret || looksSecret(k), Exposed: exposed})
			}
			cur, err := c.GetEnv(ctxOf(cmd), s.ID)
			if err != nil {
				return err
			}
			rw, err := app.beforeDeploy(ctxOf(cmd), c, s)
			if err != nil {
				return err
			}
			rw.onlyIfDeployed() // the server redeploys only services deployed before
			if noRedeploy {
				rw.svc = nil
			}
			out, err := c.PutEnvOpt(ctxOf(cmd), s.ID, client.MergeEnv(cur, vars, nil), !noRedeploy)
			if err != nil {
				return err
			}
			if app.JSON {
				if err := app.printJSON(out); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(app.Out, "Set %s on %s\n", strings.Join(order, ", "), s.Name)
			}
			_, err = rw.wait(ctxOf(cmd))
			return err
		},
	}
	set.Flags().BoolVar(&secret, "secret", false, "mark as secret")
	set.Flags().BoolVar(&exposed, "exposed", false, "share with other services in the project as ${KEY}")
	cmd.AddCommand(set)
	cmd.AddCommand(&cobra.Command{
		Use: "unset -s SERVICE KEY...", Short: "Remove variables (redeploys the service)", Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, s, err := resolve(cmd)
			if err != nil {
				return err
			}
			cur, err := c.GetEnv(ctxOf(cmd), s.ID)
			if err != nil {
				return err
			}
			rw, err := app.beforeDeploy(ctxOf(cmd), c, s)
			if err != nil {
				return err
			}
			rw.onlyIfDeployed() // the server redeploys only services deployed before
			if noRedeploy {
				rw.svc = nil
			}
			out, err := c.PutEnvOpt(ctxOf(cmd), s.ID, client.MergeEnv(cur, nil, args), !noRedeploy)
			if err != nil {
				return err
			}
			if app.JSON {
				if err := app.printJSON(out); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(app.Out, "Unset %s on %s\n", strings.Join(args, ", "), s.Name)
			}
			_, err = rw.wait(ctxOf(cmd))
			return err
		},
	})
	imp := &cobra.Command{
		Use:   "import -s SERVICE FILE",
		Short: "Set many variables from a .env file (- for stdin) in one change",
		Long: "Reads KEY=VALUE lines (blank lines, # comments and a leading `export` are ignored; values may\n" +
			"be wrapped in single or double quotes) and applies them as one edit, so the service redeploys once.\n" +
			"--replace removes every variable not in the file.",
		Example: "  atoll env import -s api .env\n  cat prod.env | atoll env import -s api --replace -",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var in io.Reader = os.Stdin
			if args[0] != "-" {
				f, err := os.Open(args[0])
				if err != nil {
					return err
				}
				defer f.Close()
				in = f
			}
			m, order, err := parseDotenv(in)
			if err != nil {
				return err
			}
			c, s, err := resolve(cmd)
			if err != nil {
				return err
			}
			cur, err := c.GetEnv(ctxOf(cmd), s.ID)
			if err != nil {
				return err
			}
			var vars []api.EnvVar
			for _, k := range order {
				vars = append(vars, api.EnvVar{Key: k, Value: m[k], Secret: secret || looksSecret(k), Exposed: exposed})
			}
			var unset []string
			if replace {
				for _, e := range cur {
					if _, ok := m[e.Key]; !ok {
						unset = append(unset, e.Key)
					}
				}
			}
			rw, err := app.beforeDeploy(ctxOf(cmd), c, s)
			if err != nil {
				return err
			}
			rw.onlyIfDeployed()
			if noRedeploy {
				rw.svc = nil
			}
			out, err := c.PutEnvOpt(ctxOf(cmd), s.ID, client.MergeEnv(cur, vars, unset), !noRedeploy)
			if err != nil {
				return err
			}
			if app.JSON {
				if err := app.printJSON(out); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(app.Out, "Set %d, removed %d on %s\n", len(order), len(unset), s.Name)
			}
			_, err = rw.wait(ctxOf(cmd))
			return err
		},
	}
	var outFile string
	var includeSecrets bool
	exp := &cobra.Command{
		Use:   "export -s SERVICE [-o FILE] [--include-secrets]",
		Short: "Write the variables as a .env file (secrets left out unless --include-secrets)",
		Long: "Prints KEY=VALUE lines that `atoll env import` reads back unchanged: values with spaces, quotes,\n" +
			"#, backslashes or newlines are double-quoted with \\, \\\" and \\n escapes.\n" +
			"Secret variables are written as comments without their value; --include-secrets writes the real\n" +
			"values (a panel session, or an API token with the exec permission; audited as env.reveal).\n" +
			"-o writes the file with mode 0600 instead of printing to stdout.",
		Example: "  atoll env export -s api > api.env\n  atoll env export -s api --include-secrets -o api.env",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, s, err := resolve(cmd)
			if err != nil {
				return err
			}
			var vars []api.EnvVar
			if includeSecrets {
				vars, err = c.GetEnvRevealed(ctxOf(cmd), s.ID)
			} else {
				vars, err = c.GetEnv(ctxOf(cmd), s.ID)
			}
			if err != nil {
				return err
			}
			text, omitted := formatDotenv(vars, includeSecrets)
			if outFile == "" || outFile == "-" {
				_, err = io.WriteString(app.Out, text)
				return err
			}
			f, err := os.OpenFile(outFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			if _, err := io.WriteString(f, text); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			msg := fmt.Sprintf("Wrote %d variables of %s to %s", len(vars)-omitted, s.Name, outFile)
			if omitted > 0 {
				msg += fmt.Sprintf(" (%d secret left out; --include-secrets to add them)", omitted)
			}
			fmt.Fprintln(app.Err, msg)
			return nil
		},
	}
	exp.Flags().StringVarP(&outFile, "output", "o", "", "write to FILE (mode 0600) instead of stdout")
	exp.Flags().BoolVar(&includeSecrets, "include-secrets", false, "write the real values of secret variables")
	cmd.AddCommand(exp)
	imp.Flags().BoolVar(&secret, "secret", false, "mark every imported variable as secret")
	imp.Flags().BoolVar(&exposed, "exposed", false, "share imported variables with other services as ${KEY}")
	imp.Flags().BoolVar(&replace, "replace", false, "remove variables that are not in the file")
	cmd.AddCommand(imp)
	return cmd
}

// formatDotenv writes vars as .env lines that parseDotenv reads back unchanged. Secrets are
// written as a comment (no value) unless includeSecrets; omitted counts them.
func formatDotenv(vars []api.EnvVar, includeSecrets bool) (text string, omitted int) {
	var b strings.Builder
	for _, v := range vars {
		if v.Secret && !includeSecrets {
			fmt.Fprintf(&b, "# %s: secret, value not exported\n", v.Key)
			omitted++
			continue
		}
		b.WriteString(v.Key + "=" + quoteDotenv(v.Value) + "\n")
	}
	return b.String(), omitted
}

// quoteDotenv leaves plain values bare and double-quotes the rest, escaping exactly what
// parseDotenv unescapes (\\, \" and \n). A bare value has no whitespace (it would be trimmed
// or split), no # (" #" starts a comment) and no quote or backslash. The panel's export
// (web/src/api/dotenv.ts) uses the same rule, so both write the same file.
func quoteDotenv(v string) string {
	if !strings.ContainsFunc(v, needsDotenvQuote) {
		return v
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v) + `"`
}

func needsDotenvQuote(r rune) bool {
	return unicode.IsSpace(r) || r == '\uFEFF' || r == '#' || r == '"' || r == '\'' || r == '\\'
}

// parseDotenv reads KEY=VALUE lines. Later keys win; order is first appearance.
func parseDotenv(r io.Reader) (map[string]string, []string, error) {
	m := map[string]string{}
	var order []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		i := strings.Index(line, "=")
		if i <= 0 {
			return nil, nil, fmt.Errorf("line %d: want KEY=VALUE", n)
		}
		k, v := strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			q := v[0]
			v = v[1 : len(v)-1]
			if q == '"' {
				v = strings.NewReplacer(`\n`, "\n", `\"`, `"`, `\\`, `\`).Replace(v)
			}
		} else if j := strings.Index(v, " #"); j >= 0 {
			v = strings.TrimSpace(v[:j]) // trailing comment on an unquoted value
		}
		if _, seen := m[k]; !seen {
			order = append(order, k)
		}
		m[k] = v
	}
	return m, order, sc.Err()
}

// ---------- logs ----------

func newLogsCmd(app *App) *cobra.Command {
	var follow bool
	var tail int
	var project string
	var build bool
	cmd := &cobra.Command{
		Use:   "logs SERVICE [-f] [--tail N] | logs SERVICE --build [DEPLOYMENT] [-f]",
		Short: "Show service logs, or the build log of a deployment (--build)",
		Long: "Without --build: the container's logs.\n" +
			"With --build: the build log of a service built from git; DEPLOYMENT (dep_...) defaults to the\n" +
			"newest deployment, -f keeps polling until the deployment finishes.",
		Example: "  atoll logs web -f\n  atoll logs web --build -f\n  atoll logs web --build dep_abc123",
		Args: func(cmd *cobra.Command, args []string) error {
			if build {
				return cobra.RangeArgs(1, 2)(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(ctxOf(cmd), os.Interrupt)
			defer stop()
			s, err := c.ResolveService(ctx, project, args[0])
			if err != nil {
				return err
			}
			if build {
				dep := ""
				if len(args) == 2 {
					dep = args[1]
				}
				err = printBuildLogs(ctx, app, c, s, dep, follow)
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			err = c.StreamLogs(ctx, s.ID, client.LogsOptions{Tail: tail, Follow: follow}, func(l api.LogLine) error {
				if app.JSON {
					return app.printJSON(l)
				}
				w := app.Out
				if l.Stream == "stderr" {
					w = app.Err
				}
				ts := ""
				if !l.At.IsZero() {
					ts = l.At.Local().Format("15:04:05") + " "
				}
				_, err := fmt.Fprintln(w, ts+l.Line)
				return err
			})
			if ctx.Err() != nil {
				return nil
			}
			return err
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep streaming (with --build: until the deployment finishes)")
	cmd.Flags().IntVar(&tail, "tail", 200, "lines of history")
	cmd.Flags().BoolVar(&build, "build", false, "show a deployment's build log (services built from git)")
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name or id")
	return cmd
}
