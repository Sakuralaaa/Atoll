package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/atoll-sh/atoll/internal/api"
	"github.com/atoll-sh/atoll/internal/client"
	"github.com/atoll-sh/atoll/internal/zeabur"
)

// `atoll import zeabur ...`: the SECONDARY migration path, driven by the Zeabur CLI signed in
// on the user's own machine. Use it when the services do not run on a VPS the user owns
// (Zeabur shared cluster, or a server rented through Zeabur) or when moving to a different
// server. For the user's own Zeabur-managed VPS use `atoll zeabur` (in place) instead.

func newImportCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "import", Short: "Import projects from other platforms"}
	cmd.AddCommand(newImportZeaburCmd(app))
	return cmd
}

type importZeaburFlags struct {
	dir         string
	workspace   string
	inventory   string
	project     string
	plan        string
	environment string
	sshDest     string
	sshKey      string
	sshPort     int
	sudo        bool
	noHost      bool
}

func (f *importZeaburFlags) path(name, explicit string) string {
	if explicit != "" {
		return explicit
	}
	return filepath.Join(f.dir, name)
}

func (f *importZeaburFlags) loadProject() (*zeabur.ProjectInventory, error) {
	inv, err := zeabur.LoadInventory(f.path("inventory.json", f.inventory))
	if err != nil {
		return nil, fmt.Errorf("read inventory (run `atoll import zeabur inventory` first): %w", err)
	}
	if f.project == "" {
		if len(inv.Projects) == 1 {
			return &inv.Projects[0], nil
		}
		return nil, errors.New("--project is required (the inventory has several projects)")
	}
	pi, ok := inv.Project(f.project)
	if !ok {
		return nil, fmt.Errorf("project %q not in the inventory", f.project)
	}
	return pi, nil
}

func (f *importZeaburFlags) loadPlan() (api.ImportPlan, string, error) {
	p := f.path("plan.json", f.plan)
	var plan api.ImportPlan
	if err := zeabur.ReadJSON(p, &plan); err != nil {
		return plan, p, fmt.Errorf("read plan (run `atoll import zeabur plan` first): %w", err)
	}
	return plan, p, nil
}

func newImportZeaburCmd(app *App) *cobra.Command {
	f := &importZeaburFlags{}
	cmd := &cobra.Command{
		Use:   "zeabur",
		Short: "Secondary path: migrate via the Zeabur CLI (shared cluster / to another server)",
		Long: `Secondary migration path, run on your own computer where the Zeabur CLI is already logged in
(` + "`npx zeabur@latest auth login`" + `; atoll never asks for Zeabur tokens or passwords).

Use it when your services run on Zeabur's shared cluster or a server rented through Zeabur, or
when you want them on a different Atoll server. If they run on YOUR OWN VPS registered to Zeabur,
use ` + "`atoll zeabur scan`" + ` instead: it takes the machine over in place.

  atoll import zeabur inventory            # read projects, services, variables (secrets → 0600 file)
  atoll import zeabur plan --server S      # build plan.json (changes nothing)
  atoll import zeabur images --push-to R   # dedicated server only: export Zeabur-built images
  atoll import zeabur data                 # dump databases / volumes locally
  atoll import zeabur apply                # create the Atoll project, upload data, verify counts
  atoll import zeabur cutover              # suspend Zeabur apps, add domains; prints rollback`,
	}
	pf := cmd.PersistentFlags()
	pf.StringVar(&f.dir, "dir", "atoll-zeabur-import", "working directory for inventory/plan/data (files are chmod 600)")
	pf.StringVar(&f.workspace, "workspace", "", "Zeabur workspace id")
	pf.StringVar(&f.inventory, "inventory", "", "inventory file (default DIR/inventory.json)")
	pf.StringVar(&f.project, "project", "", "Zeabur project id or name")
	pf.StringVar(&f.plan, "plan", "", "plan file (default DIR/plan.json)")
	pf.StringVar(&f.sshDest, "ssh", "", "read the dedicated server over SSH (user@host) instead of `zeabur server exec`")
	pf.StringVar(&f.sshKey, "ssh-key", "", "SSH private key file")
	pf.IntVar(&f.sshPort, "ssh-port", 22, "SSH port")
	pf.BoolVar(&f.sudo, "sudo", false, "use sudo on the host")
	cmd.AddCommand(importZeaburInventory(app, f), importZeaburPlan(app, f), importZeaburImages(app, f),
		importZeaburData(app, f), importZeaburApply(app, f), importZeaburCutover(app, f))
	return cmd
}

func importZeaburInventory(app *App, f *importZeaburFlags) *cobra.Command {
	var projects []string
	cmd := &cobra.Command{
		Use: "inventory [--project ID]...", Short: "Read Zeabur projects through the Zeabur CLI (read-only)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := ctxOf(cmd)
			cli := zeabur.NewCLI(f.workspace)
			opt := zeabur.InventoryOptions{ProjectIDs: splitList(projects), Environment: f.environment, NoHost: f.noHost, Log: app.Err}
			if f.project != "" && len(opt.ProjectIDs) == 0 {
				opt.ProjectIDs = []string{f.project}
			}
			if f.sshDest != "" {
				opt.SSH = &zeabur.HostAccess{Mode: "ssh", SSHDest: f.sshDest, SSHKey: f.sshKey, SSHPort: f.sshPort, Sudo: f.sudo}
			}
			inv, err := zeabur.GatherInventory(ctx, cli, opt)
			if err != nil {
				return err
			}
			p := f.path("inventory.json", f.inventory)
			if err := zeabur.WriteSecretJSON(p, inv); err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(map[string]any{"inventory": p, "projects": len(inv.Projects)})
			}
			zeabur.PrintReport(app.Out, inv)
			fmt.Fprintf(app.Out, "\nInventory (contains secrets, mode 0600): %s\nNext: atoll import zeabur plan --server SERVER\n", p)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&projects, "projects", nil, "Zeabur project ids (default: all)")
	cmd.Flags().StringVar(&f.environment, "environment", "", "Zeabur environment id when a project has several")
	cmd.Flags().BoolVar(&f.noHost, "no-host", false, "do not read the dedicated server's k3s")
	return cmd
}

func importZeaburPlan(app *App, f *importZeaburFlags) *cobra.Command {
	var server, name, baseDomain string
	var domains []string
	cmd := &cobra.Command{
		Use: "plan --server SERVER [--name NAME] [--domain old=new]...", Short: "Build plan.json from the inventory (changes nothing)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if server == "" {
				return errors.New("--server is required: the Atoll server the project should run on")
			}
			pi, err := f.loadProject()
			if err != nil {
				return err
			}
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			s, err := c.ResolveServer(ctx, server)
			if err != nil {
				return err
			}
			dm, err := zeabur.ParseDomainMap(domains)
			if err != nil {
				return err
			}
			if baseDomain == "" {
				if st, err := c.GetSettings(ctx); err == nil {
					baseDomain = st.BaseDomain
				}
			}
			plan := zeabur.BuildPlan(pi, zeabur.PlanOptions{TargetServer: s.ID, ServerIPv4: s.PublicIPv4, ProjectName: name, BaseDomain: baseDomain, DomainMap: dm})
			p := f.path("plan.json", f.plan)
			if err := zeabur.WriteSecretJSON(p, plan); err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(map[string]any{"plan": p, "services": len(plan.Services), "warnings": plan.Warnings})
			}
			zeabur.PrintPlan(app.Out, plan)
			fmt.Fprintf(app.Out, "\nPlan (contains secrets, mode 0600): %s — review it, then `atoll import zeabur data` and `apply`.\n", p)
			return nil
		},
	}
	cmd.Flags().StringVarP(&server, "server", "s", "", "target Atoll server name or id")
	cmd.Flags().StringVar(&name, "name", "", "Atoll project name (default: Zeabur project name)")
	cmd.Flags().StringVar(&baseDomain, "base-domain", "", "base domain for *.zeabur.app replacements (default: panel Settings)")
	cmd.Flags().StringArrayVar(&domains, "domain", nil, "replace a hostname: old=new (old= drops it), repeatable")
	return cmd
}

func importZeaburImages(app *App, f *importZeaburFlags) *cobra.Command {
	var pushTo string
	cmd := &cobra.Command{
		Use: "images --push-to REGISTRY/PREFIX", Short: "Export Zeabur-built images from a dedicated server and push them to your registry", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if pushTo == "" {
				return errors.New("--push-to is required, e.g. ghcr.io/you (long term: build from source with GitHub Actions → GHCR)")
			}
			pi, err := f.loadProject()
			if err != nil {
				return err
			}
			plan, p, err := f.loadPlan()
			if err != nil {
				return err
			}
			if err := zeabur.ExportImages(ctxOf(cmd), zeabur.NewCLI(f.workspace), pi, &plan, strings.TrimRight(pushTo, "/"), filepath.Join(f.dir, "images"), app.Err); err != nil {
				return err
			}
			if err := zeabur.WriteSecretJSON(p, plan); err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "Images pushed; %s updated.\n", p)
			return nil
		},
	}
	cmd.Flags().StringVar(&pushTo, "push-to", "", "registry prefix to push to, e.g. ghcr.io/you")
	return cmd
}

func importZeaburData(app *App, f *importZeaburFlags) *cobra.Command {
	var redis bool
	cmd := &cobra.Command{
		Use: "data [--include-redis]", Short: "Dump databases and volumes to DIR/data (read-only on Zeabur)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			pi, err := f.loadProject()
			if err != nil {
				return err
			}
			plan, _, err := f.loadPlan()
			if err != nil {
				return err
			}
			out := filepath.Join(f.dir, "data")
			man, err := zeabur.ExportData(ctxOf(cmd), zeabur.NewCLI(f.workspace), pi, plan, zeabur.DataOptions{OutDir: out, IncludeRedis: redis, Log: app.Err})
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(man)
			}
			tw := app.table()
			fmt.Fprintln(tw, "SERVICE\tKIND\tFILE\tSIZE\tTABLES")
			for _, e := range man.Entries {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", e.Service, e.Kind, e.File, humanSize(e.Bytes), len(e.RowCounts))
			}
			tw.Flush()
			fmt.Fprintf(app.Out, "\nData in %s (mode 0600). Next: atoll import zeabur apply\n", out)
			return nil
		},
	}
	cmd.Flags().BoolVar(&redis, "include-redis", false, "also copy Redis (skip when it is only a cache)")
	return cmd
}

func importZeaburApply(app *App, f *importZeaburFlags) *cobra.Command {
	var yes, withDomains, noData bool
	cmd := &cobra.Command{
		Use: "apply [--yes]", Short: "Create the Atoll project from plan.json, upload data, start and verify row counts", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, _, err := f.loadPlan()
			if err != nil {
				return err
			}
			c, err := app.client()
			if err != nil {
				return err
			}
			dataDir := filepath.Join(f.dir, "data")
			if _, err := os.Stat(filepath.Join(dataDir, "manifest.json")); err != nil || noData {
				dataDir = ""
			}
			if !zeaburConfirm(app, yes, "Create Atoll project %q with %d service(s) on %s and upload data? Zeabur is not changed.", plan.ProjectName, len(plan.Services), plan.TargetServer) {
				return errors.New("cancelled; nothing was changed")
			}
			st, err := zeabur.Apply(ctxOf(cmd), c, plan, zeabur.ApplyOptions{DataDir: dataDir, WithDomains: withDomains, StateFile: filepath.Join(f.dir, "apply-state.json"), Log: app.Err})
			if app.JSON && st != nil {
				app.printJSON(st)
			}
			if err != nil {
				return err
			}
			if !app.JSON {
				fmt.Fprintf(app.Out, "Import %s applied (project %s). Test the services, then: atoll import zeabur cutover\n", st.ImportID, st.ProjectID)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVar(&withDomains, "with-domains", false, "add domains now instead of at cutover (switches DNS early)")
	cmd.Flags().BoolVar(&noData, "no-data", false, "do not upload DIR/data even if present")
	return cmd
}

func zeaburConfirm(app *App, yes bool, format string, a ...any) bool {
	return zeabur.Confirm(app.In, app.Err, yes, format, a...)
}

func importZeaburCutover(app *App, f *importZeaburFlags) *cobra.Command {
	var yes, noSuspend bool
	cmd := &cobra.Command{
		Use: "cutover [--yes]", Short: "Suspend Zeabur app services and add the held-back domains to Atoll (prints rollback)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := ctxOf(cmd)
			var st zeabur.ApplyState
			sp := filepath.Join(f.dir, "apply-state.json")
			if err := zeabur.ReadJSON(sp, &st); err != nil {
				return fmt.Errorf("read %s (run `atoll import zeabur apply` first): %w", sp, err)
			}
			pi, err := f.loadProject()
			if err != nil {
				return err
			}
			c, err := app.client()
			if err != nil {
				return err
			}
			var hosts []string
			for _, ds := range st.PendingDomains {
				hosts = append(hosts, ds...)
			}
			before := zeabur.LookupDNS(ctx, hosts)
			_ = zeabur.WriteSecretJSON(filepath.Join(f.dir, "dns-before.json"), before)
			apps := zeabur.AppServiceIDs(pi.ServicesView())
			if noSuspend {
				apps = nil
			}
			if !zeaburConfirm(app, yes, "Suspend %d Zeabur app service(s) (databases keep running, nothing is deleted) and add %d domain(s) to Atoll?", len(apps), len(hosts)) {
				return errors.New("cancelled; nothing was changed")
			}
			zc := zeabur.NewCLI(f.workspace)
			var suspended []string
			for _, id := range apps {
				if _, err := zc.Run(ctx, false, "service", "suspend", "--id", id, "-y"); err != nil {
					fmt.Fprintf(app.Err, "! could not suspend %s: %v\n", id, err)
					continue
				}
				suspended = append(suspended, id)
			}
			var pending []string
			for svc, ds := range st.PendingDomains {
				id := st.Services[svc]
				if id == "" {
					continue
				}
				for _, d := range ds {
					_, err := c.AddDomain(ctx, id, client.AddDomainRequest{Hostname: d, Proxied: true})
					if cf, ok := client.AsConfirmation(err); ok {
						pending = append(pending, fmt.Sprintf("%s: approve %s in the panel", d, cf.ID))
						continue
					}
					if err != nil {
						fmt.Fprintf(app.Err, "! domain %s: %v\n", d, err)
					}
				}
			}
			fmt.Fprintf(app.Out, "Suspended on Zeabur: %s\n", strings.Join(suspended, ", "))
			for _, p := range pending {
				fmt.Fprintln(app.Out, "pending approval:", p)
			}
			fmt.Fprintln(app.Out, "\nDNS: point these hostnames at the Atoll server (you change DNS yourself unless the panel manages the zone):")
			for _, r := range before {
				cur := strings.Join(r.IPs, ",")
				if r.CNAME != "" {
					cur = "CNAME " + r.CNAME
				}
				fmt.Fprintf(app.Out, "  %s (now %s)\n", r.Host, cur)
			}
			fmt.Fprintln(app.Out, "\nRollback: restore the DNS records above (saved in dns-before.json) and resume the Zeabur services:")
			for _, id := range suspended {
				fmt.Fprintf(app.Out, "  npx zeabur@latest service redeploy --id %s   (or Resume in the Zeabur dashboard)\n", id)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVar(&noSuspend, "no-suspend", false, "do not suspend Zeabur services (only add domains)")
	return cmd
}
