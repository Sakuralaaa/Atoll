package cli

// CLI: env and logs.

import (
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/client"
)

// ---------- env ----------

func newEnvCmd(app *App) *cobra.Command {
	var svcRef, project string
	var secret, exposed bool
	cmd := &cobra.Command{Use: "env", Short: "List, set and unset service variables"}
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
			out, err := c.PutEnv(ctxOf(cmd), s.ID, client.MergeEnv(cur, vars, nil))
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(out)
			}
			fmt.Fprintf(app.Out, "Set %s on %s\n", strings.Join(order, ", "), s.Name)
			return nil
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
			out, err := c.PutEnv(ctxOf(cmd), s.ID, client.MergeEnv(cur, nil, args))
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(out)
			}
			fmt.Fprintf(app.Out, "Unset %s on %s\n", strings.Join(args, ", "), s.Name)
			return nil
		},
	})
	return cmd
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
