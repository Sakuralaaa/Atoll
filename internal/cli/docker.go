package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/client"
	"github.com/Sakuralaaa/Atoll/internal/zeabur"
)

// `atoll docker ...`: adopt containers the user started on a server with `docker run` or
// docker compose (see docs/adopt-docker.md).

func newDockerCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docker",
		Short: "Adopt containers you started yourself (docker run / compose) on a server (scan → import → rehearse → cutover)",
		Long: `Takes over containers you started on one of your servers with docker run or docker compose,
in place: same host ports, bind mounts and named volumes mounted where they are (no data copy).

  atoll docker scan SERVER                       # groups (compose projects / standalone containers) and their risk
  atoll docker import SERVER compose:blog        # create the Atoll project (nothing starts, nothing stops)
  atoll docker rehearse IMPORT_ID                # Atoll copies start next to yours: no ports, scratch data
  atoll docker cutover IMPORT_ID                 # your containers stop (kept), Atoll's take their ports and data
  atoll docker rollback IMPORT_ID                # your containers run again

Containers flagged "manual" (host network, privileged, Docker socket, devices, cap_add…) are
never adopted automatically. Your old containers, images and volumes are never removed.
Do not run "docker compose up" for an adopted project afterwards: it would start the old
containers next to Atoll's.`,
	}
	cmd.AddCommand(newDockerScanCmd(app), newDockerImportCmd(app),
		newDockerStepCmd(app, "rehearse"), newDockerStepCmd(app, "cutover"), newDockerStepCmd(app, "rollback"),
		newZeaburStatusCmd(app))
	return cmd
}

func newDockerScanCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use: "scan SERVER", Short: "Show the containers on a server that Atoll does not manage, grouped and classified", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			s, err := c.ResolveServer(ctx, args[0])
			if err != nil {
				return err
			}
			inv, err := c.DockerScan(ctx, s.ID)
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(inv)
			}
			printDockerInventory(app.Out, s, inv)
			return nil
		},
	}
}

func dockerPortList(ps []api.DockerPort) string {
	var out []string
	for _, p := range ps {
		if p.HostPort == 0 {
			continue
		}
		h := fmt.Sprint(p.HostPort)
		if p.HostIP != "" {
			h = p.HostIP + ":" + h
		}
		out = append(out, fmt.Sprintf("%s→%d/%s", h, p.ContainerPort, p.Protocol))
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, ",")
}

func printDockerInventory(w io.Writer, s *api.Server, inv *api.DockerInventory) {
	fmt.Fprintf(w, "Server %s: Docker %s, free disk %.1fGB, free memory %dMB\n\n", s.Name, inv.DockerVersion, inv.DiskFreeGB, inv.MemFreeMB)
	if len(inv.Groups) == 0 {
		fmt.Fprintln(w, "No containers outside Atoll.")
		return
	}
	for _, g := range inv.Groups {
		fmt.Fprintf(w, "%s (%s)", g.Key, g.Kind)
		if g.WorkingDir != "" {
			fmt.Fprintf(w, " — %s", g.WorkingDir)
		}
		fmt.Fprintln(w)
		tw := newTab(w)
		fmt.Fprintln(tw, "  CONTAINER\tSERVICE\tSTATE\tRISK\tIMAGE\tPORTS\tMOUNTS\tMEM")
		for _, c := range g.Containers {
			svc := c.Service
			if svc == "" {
				svc = "-"
			}
			mem := "-"
			if c.MemUsageMB > 0 {
				mem = fmt.Sprintf("%dM", c.MemUsageMB)
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n", c.Name, svc, c.State, c.Risk, shortRef(c.Image), dockerPortList(c.Ports), len(c.Mounts), mem)
		}
		tw.Flush()
		for _, c := range g.Containers {
			for _, r := range c.RiskReasons {
				fmt.Fprintf(w, "  ✗ %s: %s\n", c.Name, r)
			}
			for _, wn := range c.Warnings {
				fmt.Fprintf(w, "  ! %s: %s\n", c.Name, wn)
			}
		}
		for _, wn := range g.Warnings {
			fmt.Fprintf(w, "  ! %s\n", wn)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "Next: atoll docker import", s.Name, "GROUP [--containers a,b] [--name PROJECT]")
}

func newDockerImportCmd(app *App) *cobra.Command {
	var name string
	var containers []string
	cmd := &cobra.Command{
		Use:   "import SERVER GROUP [--containers a,b] [--name PROJECT]",
		Short: "Create the Atoll project for one container group (nothing starts, your containers keep running)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			s, err := c.ResolveServer(ctx, args[0])
			if err != nil {
				return err
			}
			group := args[1]
			if !strings.Contains(group, ":") {
				group = "compose:" + group // "blog" is short for compose:blog
				if inv, err := c.DockerScan(ctx, s.ID); err == nil {
					found := false
					for _, g := range inv.Groups {
						found = found || g.Key == group
					}
					if !found {
						group = "container:" + args[1]
					}
				}
			}
			job, err := c.DockerAdopt(ctx, api.DockerAdoptRequest{ServerID: s.ID, Group: group, Containers: splitList(containers), ProjectName: name})
			if ok, err := app.handle(ctx, c, err); err != nil || !ok {
				return err
			}
			if app.JSON {
				return app.printJSON(job)
			}
			fmt.Fprintf(app.Out, "Import %s created project %s on %s (not started; your containers keep running).\n\n", job.ID, job.Plan.ProjectName, s.Name)
			tw := app.table()
			fmt.Fprintln(tw, "SERVICE\tIMAGE\tPORTS\tMOUNTS IN PLACE")
			for _, sv := range job.Plan.Services {
				var ports, mounts []string
				for _, p := range sv.Ports {
					if p.PublicPort > 0 {
						ports = append(ports, fmt.Sprintf("%d→%d/%s", p.PublicPort, p.ContainerPort, p.Protocol))
					}
				}
				for _, v := range sv.Volumes {
					mounts = append(mounts, v.HostPath+":"+v.MountPath)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", sv.Name, shortRef(sv.Image), strings.Join(ports, ","), strings.Join(mounts, " "))
			}
			tw.Flush()
			for _, w := range job.Plan.Warnings {
				fmt.Fprintln(app.Out, "!", w)
			}
			fmt.Fprintf(app.Out, "\nNext: atoll docker rehearse %s\n", job.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Atoll project name (default: the compose project or container name)")
	cmd.Flags().StringSliceVar(&containers, "containers", nil, "only these containers of the group (default: every one that can be adopted automatically)")
	return cmd
}

var dockerStepText = map[string]struct{ ask, done, next string }{
	"rehearse": {
		ask:  "Start Atoll copies of %d container(s) next to yours (no published ports, scratch data dirs) and check they stay healthy? Your containers keep serving.",
		done: "Rehearsal finished: the Atoll copies started and were stopped again.",
		next: "Next: atoll docker cutover %s",
	},
	"cutover": {
		ask:  "Cut over %d container(s): stop yours (kept, restart policy set to no) and start Atoll's on the same ports and data? Expect a short downtime; a failure rolls back by itself.",
		done: "Cutover finished: Atoll now runs these services on the old ports and data.",
		next: "If anything is wrong: atoll docker rollback %s   (your old containers are kept)",
	},
	"rollback": {
		ask:  "Roll back: stop the Atoll services of %d container(s) and start your old containers again?",
		done: "Rolled back: your old containers run again.",
		next: "You can cut over again later: atoll docker cutover %s",
	},
}

func newDockerStepCmd(app *App, step string) *cobra.Command {
	var yes, ignoreMem bool
	txt := dockerStepText[step]
	short := map[string]string{
		"rehearse": "Start Atoll copies next to your containers (no ports, scratch data) and verify",
		"cutover":  "Stop your containers (kept) and run Atoll's on the same ports and data",
		"rollback": "Undo a cutover: your old containers run again",
	}[step]
	cmd := &cobra.Command{
		Use: step + " IMPORT_ID [--yes]", Short: short, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			job, err := c.GetImport(ctx, args[0])
			if err != nil {
				return err
			}
			if job.Mode != "docker" {
				return fmt.Errorf("import %s is not a Docker adoption (use `atoll zeabur %s`)", job.ID, step)
			}
			if !zeabur.Confirm(app.In, app.Err, yes, txt.ask, len(job.Plan.Services)) {
				return errors.New("cancelled; nothing was changed")
			}
			var call func(context.Context, string) (*api.ImportJob, error)
			switch step {
			case "rehearse":
				call = func(ctx context.Context, id string) (*api.ImportJob, error) {
					return c.RehearseImportWith(ctx, id, api.ZeaburRehearseRequest{IgnoreMemory: ignoreMem})
				}
			case "cutover":
				call = c.CutoverImport
			default:
				call = c.RollbackImport
			}
			return app.dockerStep(ctx, c, job, step, call)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	if step == "rehearse" {
		cmd.Flags().BoolVar(&ignoreMem, "ignore-memory", false, "skip the free-memory check")
	}
	return cmd
}

func (app *App) dockerStep(ctx context.Context, c *client.Client, job *api.ImportJob, step string, call func(context.Context, string) (*api.ImportJob, error)) error {
	txt := dockerStepText[step]
	before := job.Status
	if started, err := call(ctx, job.ID); err == nil && started != nil && started.Status != "" {
		before = started.Status
	} else if ok, err := app.handle(ctx, c, err); err != nil || !ok {
		return err
	}
	final, err := app.waitImport(ctx, c, job.ID, step, before)
	if app.JSON {
		if final != nil {
			app.printJSON(final)
		}
		return err
	}
	if err != nil {
		fmt.Fprintf(app.Out, "%s did not complete. Your old containers were not removed.", step)
		if step == "cutover" && final != nil && final.Status == "rolled_back" {
			fmt.Fprint(app.Out, " The cutover rolled back by itself: your containers run again.")
		}
		fmt.Fprintln(app.Out)
		return err
	}
	fmt.Fprintln(app.Out, txt.done)
	fmt.Fprintf(app.Out, txt.next+"\n", job.ID)
	return nil
}
