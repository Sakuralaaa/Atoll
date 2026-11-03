package cli

// CLI: `atoll context` (several saved panels) and shell completion of names.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// The global flag is --panel, not --context: `atoll deploy --context` is the git build context.

func newContextCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "context",
		Aliases: []string{"contexts", "ctx"},
		Short:   "Switch between saved panels (atoll login --name NAME adds one)",
		Long: "Each `atoll login --name NAME` saves a panel. `atoll context use NAME` makes it current;\n" +
			"--panel NAME (or $ATOLL_PANEL) picks one for a single command.",
		Example: "  atoll login --name prod --url https://atoll.example.com --token atl_pat_...\n" +
			"  atoll context ls\n  atoll context use prod\n  atoll --panel staging status",
	}
	load := func() (Config, error) {
		cfg, err := LoadConfig(app.ConfigPath)
		if err != nil {
			return cfg, err
		}
		cfg.normalize()
		return cfg, nil
	}
	cmd.AddCommand(&cobra.Command{
		Use: "ls", Aliases: []string{"list"}, Short: "List saved panels (* = current)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := load()
			if err != nil {
				return err
			}
			names := panelNames(cfg)
			if app.JSON {
				type row struct {
					Name    string `json:"name"`
					URL     string `json:"url"`
					User    string `json:"user,omitempty"`
					Current bool   `json:"current"`
				}
				out := []row{}
				for _, n := range names {
					p := cfg.Panels[n]
					out = append(out, row{n, p.URL, p.User, n == cfg.Current})
				}
				return app.printJSON(out)
			}
			if len(names) == 0 {
				fmt.Fprintln(app.Out, "No saved panels: run `atoll login --name NAME --url URL --token TOKEN`.")
				return nil
			}
			tw := app.table()
			fmt.Fprintln(tw, "CURRENT\tNAME\tURL\tUSER")
			for _, n := range names {
				p := cfg.Panels[n]
				mark := ""
				if n == cfg.Current {
					mark = "*"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", mark, n, p.URL, p.User)
			}
			return tw.Flush()
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "current", Short: "Print the current panel name", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := load()
			if err != nil {
				return err
			}
			if cfg.Current == "" {
				return fmt.Errorf("no current panel: run `atoll context use NAME` or `atoll login`")
			}
			fmt.Fprintln(app.Out, cfg.Current)
			return nil
		},
	})
	use := &cobra.Command{
		Use: "use NAME", Short: "Make a saved panel current", Args: cobra.ExactArgs(1),
		ValidArgsFunction: completePanels(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := load()
			if err != nil {
				return err
			}
			if !cfg.Use(args[0]) {
				return fmt.Errorf("no saved panel %q (have: %s)", args[0], strings.Join(panelNames(cfg), ", "))
			}
			if _, err := SaveConfig(app.ConfigPath, cfg); err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "Now using %s (%s)\n", args[0], cfg.URL)
			return nil
		},
	}
	cmd.AddCommand(use)
	cmd.AddCommand(&cobra.Command{
		Use: "rm NAME", Aliases: []string{"remove", "delete"}, Short: "Forget a saved panel (the token stays valid; revoke it in the panel)", Args: cobra.ExactArgs(1),
		ValidArgsFunction: completePanels(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := load()
			if err != nil {
				return err
			}
			if !cfg.Remove(args[0]) {
				return fmt.Errorf("no saved panel %q", args[0])
			}
			if _, err := SaveConfig(app.ConfigPath, cfg); err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "Removed %s\n", args[0])
			if cfg.Current == "" && len(cfg.Panels) > 0 {
				fmt.Fprintln(app.Out, "No current panel now: pick one with `atoll context use NAME`.")
			}
			return nil
		},
	})
	return cmd
}

func panelNames(cfg Config) []string {
	names := make([]string, 0, len(cfg.Panels))
	for n := range cfg.Panels {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

type completeFunc = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)

func completePanels(app *App) completeFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 && cmd.Name() != "atoll" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, _ := LoadConfig(app.ConfigPath)
		cfg.normalize()
		return panelNames(cfg), cobra.ShellCompDirectiveNoFileComp
	}
}

// completeServices offers service names (from every project, or the one given with -p).
// It asks the panel, so it is bounded by a short timeout and stays quiet on errors.
func completeServices(app *App) completeFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		c, err := app.client()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
		defer cancel()
		projects, err := c.ListProjects(ctx)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		want := ""
		if f := cmd.Flag("project"); f != nil {
			want = f.Value.String()
		}
		seen := map[string]bool{}
		var out []string
		for _, p := range projects {
			if want != "" && want != p.Name && want != p.ID {
				continue
			}
			svcs, err := c.ListServices(ctx, p.ID)
			if err != nil {
				continue
			}
			for _, s := range svcs {
				if !seen[s.Name] && strings.HasPrefix(s.Name, toComplete) {
					seen[s.Name] = true
					out = append(out, s.Name+"\t"+p.Name)
				}
			}
		}
		sort.Strings(out)
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

func completeProjects(app *App) completeFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		c, err := app.client()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
		defer cancel()
		projects, err := c.ListProjects(ctx)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []string
		for _, p := range projects {
			if strings.HasPrefix(p.Name, toComplete) {
				out = append(out, p.Name)
			}
		}
		sort.Strings(out)
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// addCompletions wires name completion into the tree: a first argument called SERVICE, the
// -s/--service and -p/--project flags, and --panel. `atoll completion bash|zsh|fish` prints the
// shell script (cobra's built-in command).
func addCompletions(app *App, root *cobra.Command) {
	_ = root.RegisterFlagCompletionFunc("panel", completePanels(app))
	svc, proj := completeServices(app), completeProjects(app)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if f := strings.Fields(c.Use); len(f) > 1 && strings.HasPrefix(f[1], "SERVICE") && c.ValidArgsFunction == nil {
			c.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
				if len(args) > 0 {
					return nil, cobra.ShellCompDirectiveDefault
				}
				return svc(cmd, args, toComplete)
			}
		}
		for name, fn := range map[string]completeFunc{"service": svc, "project": proj} {
			if c.LocalNonPersistentFlags().Lookup(name) != nil || c.PersistentFlags().Lookup(name) != nil {
				_ = c.RegisterFlagCompletionFunc(name, fn)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
