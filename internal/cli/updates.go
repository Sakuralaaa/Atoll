package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func newUpdatesCmd(app *App) *cobra.Command {
	var project, apply, check, tag string
	var available bool
	cmd := &cobra.Command{
		Use:   "updates [--apply SERVICE [--tag TAG]] [--check SERVICE]",
		Short: "Show image updates of every service, check one now, or apply one",
		Long: "Lists services whose image tag points to a newer digest than the running container,\n" +
			"or that have a newer semver tag (same major; a major bump is shown separately).\n" +
			"  atoll updates                     all services\n" +
			"  atoll updates --available         only services with an update\n" +
			"  atoll updates --check SERVICE     ask the registry now\n" +
			"  atoll updates --apply SERVICE     redeploy to the new digest of the current tag\n" +
			"  atoll updates --apply SERVICE --tag 1.28   switch to another tag\n" +
			"  atoll updates policy SERVICE notify|auto|off [--window 02:00-05:00]",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			if apply != "" && check != "" {
				return fmt.Errorf("use either --apply or --check")
			}
			if tag != "" && apply == "" {
				return fmt.Errorf("--tag needs --apply SERVICE")
			}
			if apply != "" {
				s, err := c.ResolveService(ctx, project, apply)
				if err != nil {
					return err
				}
				d, err := c.ApplyImageUpdate(ctx, s.ID, tag)
				if _, err := app.handle(ctx, c, err); err != nil {
					return err
				}
				if app.JSON {
					return app.printJSON(d)
				}
				what := "latest digest of its tag"
				if tag != "" {
					what = "tag " + tag
				}
				fmt.Fprintf(app.Out, "%s: deploying %s (%s); roll back with `atoll rollback %s`\n", s.Name, what, d.ID, s.Name)
				return nil
			}
			if check != "" {
				s, err := c.ResolveService(ctx, project, check)
				if err != nil {
					return err
				}
				u, err := c.CheckImageUpdate(ctx, s.ID)
				if err != nil {
					return err
				}
				if app.JSON {
					return app.printJSON(u)
				}
				return app.printUpdates([]api.ImageUpdate{*u}, nil)
			}
			list, err := c.ListImageUpdates(ctx, available)
			if err != nil {
				return err
			}
			if project != "" {
				p, err := c.ResolveProject(ctx, project)
				if err != nil {
					return err
				}
				kept := list[:0]
				for _, u := range list {
					if u.ProjectID == p.ID {
						kept = append(kept, u)
					}
				}
				list = kept
			}
			if app.JSON {
				return app.printJSON(list)
			}
			names := map[string]string{}
			if ps, err := c.ListProjects(ctx); err == nil {
				for _, p := range ps {
					names[p.ID] = p.Name
				}
			}
			if len(list) == 0 {
				if available {
					fmt.Fprintln(app.Out, "All images are up to date.")
				} else {
					fmt.Fprintln(app.Out, "No services.")
				}
				return nil
			}
			return app.printUpdates(list, names)
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name or id (filter / disambiguate service names)")
	cmd.Flags().StringVar(&apply, "apply", "", "service to update now")
	cmd.Flags().StringVar(&tag, "tag", "", "with --apply: switch to this tag instead of the current tag's new digest")
	cmd.Flags().StringVar(&check, "check", "", "service to check against its registry now")
	cmd.Flags().BoolVar(&available, "available", false, "only list services with an update")
	cmd.AddCommand(newUpdatesPolicyCmd(app))
	return cmd
}

func newUpdatesPolicyCmd(app *App) *cobra.Command {
	var project, window string
	cmd := &cobra.Command{
		Use:   "policy SERVICE notify|auto|off [--window HH:MM-HH:MM]",
		Short: "Set a service's image update policy (auto redeploys new digests of the same tag, inside the UTC window)",
		Args:  cobra.ExactArgs(2),
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
			u, err := c.SetImageUpdatePolicy(ctx, s.ID, api.ImageUpdatePolicy{Policy: args[1], Window: window})
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(u)
			}
			w := ""
			if u.Window != "" {
				w = " (window " + u.Window + " UTC)"
			}
			fmt.Fprintf(app.Out, "%s: image update policy %s%s\n", s.Name, u.Policy, w)
			return nil
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project name or id")
	cmd.Flags().StringVar(&window, "window", "", "daily auto-update window in UTC, e.g. 02:00-05:00")
	return cmd
}

func shortDigestCLI(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	if d == "" {
		return "-"
	}
	return d
}

func (app *App) printUpdates(list []api.ImageUpdate, projects map[string]string) error {
	tw := app.table()
	fmt.Fprintln(tw, "SERVICE\tPROJECT\tIMAGE\tSTATUS\tRUNNING\tLATEST\tNEWER TAG\tMAJOR\tPOLICY\tCHECKED")
	for _, u := range list {
		proj := projects[u.ProjectID]
		if proj == "" {
			proj = u.ProjectID
		}
		status := u.Status
		if u.Error != "" {
			status += ": " + u.Error
		} else if u.Note != "" && u.Status != "up_to_date" {
			status += " (" + u.Note + ")"
		}
		checked := "-"
		if u.CheckedAt != nil {
			checked = ago(*u.CheckedAt)
		}
		dash := func(s string) string {
			if s == "" {
				return "-"
			}
			return s
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", u.Service, proj, u.Image, status,
			shortDigestCLI(u.CurrentDigest), shortDigestCLI(u.LatestDigest), dash(u.NewerTag), dash(u.MajorTag), u.Policy, checked)
	}
	return tw.Flush()
}
