package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/client"
	"github.com/Sakuralaaa/Atoll/internal/zeabur"
)

// `atoll zeabur ...`: the in-place takeover of the user's own VPS that Zeabur manages
// (primary import path; see docs/migrate-from-zeabur.md).

func newZeaburCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "zeabur",
		Short: "Take over your own Zeabur-managed VPS in place (scan → import → rehearse → cutover)",
		Long: `Moves the services Zeabur runs on YOUR OWN VPS into Atoll on the same machine.
Install atoll-agent on that VPS first (panel → Servers → Add server), then:

  atoll zeabur scan SERVER
  atoll zeabur import SERVER --namespace environment-xxx --domain app.zeabur.app=app.example.com
  atoll zeabur rehearse IMPORT_ID     # Atoll copies start without public traffic; Zeabur keeps serving
  atoll zeabur cutover IMPORT_ID...   # Zeabur apps scaled to 0, Atoll takes ports 80/443
  atoll zeabur rollback IMPORT_ID     # back to Zeabur if anything is wrong
  atoll zeabur rollback --server SERVER   # every cut-over project of the server at once

Zeabur's ingress is shared by every Zeabur project on the server: cutting over parks it for
all of them. Import and rehearse each project (one at a time), then cut them over together
by naming all their import IDs; a cutover that would take another serving project offline is
refused (--force accepts the outage).
  atoll zeabur cleanup IMPORT_ID      # LATER: uninstall Zeabur's k3s (irreversible)

Nothing on Zeabur is deleted until cleanup, which you confirm by typing the server name.`,
	}
	cmd.AddCommand(newZeaburScanCmd(app), newZeaburImportCmd(app),
		newZeaburStepCmd(app, "rehearse"), newZeaburStepCmd(app, "cutover"), newZeaburStepCmd(app, "rollback"),
		newZeaburCleanupCmd(app), newZeaburStatusCmd(app))
	return cmd
}

func humanSize(n int64) string {
	switch {
	case n <= 0:
		return "-"
	case n < 1<<20:
		return fmt.Sprintf("%.0fK", float64(n)/1024)
	case n < 1<<30:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.2fG", float64(n)/(1<<30))
}

func newZeaburScanCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use: "scan SERVER", Short: "Show the Zeabur projects and services found on a server", Args: cobra.ExactArgs(1),
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
			inv, err := c.ZeaburScan(ctx, s.ID)
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(inv)
			}
			printInventory(app.Out, s, inv)
			return nil
		},
	}
}

func printInventory(w io.Writer, s *api.Server, inv *api.ZeaburInventory) {
	if !inv.K3s {
		fmt.Fprintf(w, "No Zeabur k3s found on %s. Is this the VPS you registered to Zeabur?\n", s.Name)
		return
	}
	ingress := "not running"
	if inv.IngressRunning {
		ingress = "running"
	}
	fmt.Fprintf(w, "Server %s: k3s %s, Zeabur ingress %s, free disk %.1fGB, free memory %dMB\n\n", s.Name, inv.K3sVersion, ingress, inv.DiskFreeGB, inv.MemFreeMB)
	var total int64
	for _, p := range inv.Projects {
		fmt.Fprintf(w, "Project %s (%s), environment %s — namespace %s\n", p.ProjectName, p.ProjectID, p.EnvironmentName, p.Namespace)
		tw := newTab(w)
		fmt.Fprintln(tw, "  SERVICE\tID\tROLE\tKIND\tSTATUS\tIMAGE\tDATA\tDOMAINS")
		for _, sv := range p.Services {
			img := sv.Image
			if sv.ZeaburRegistry {
				img = "(built on Zeabur) " + shortRef(img)
			} else {
				img = shortRef(img)
			}
			var data int64
			for _, v := range sv.Volumes {
				data += v.Bytes
			}
			total += data
			st := sv.Status
			if st == "" {
				st = "-"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", sv.Name, sv.ServiceID, sv.Role, sv.Kind, st, img, humanSize(data), strings.Join(sv.Domains, ","))
		}
		tw.Flush()
		for _, sv := range p.Services {
			for _, warn := range sv.Warnings {
				fmt.Fprintf(w, "  ! %s: %s\n", sv.Name, warn)
			}
		}
		for _, warn := range p.Warnings {
			fmt.Fprintf(w, "  ! %s\n", warn)
		}
		fmt.Fprintln(w)
	}
	if total > 0 && inv.DiskFreeGB > 0 && float64(total)*1.2 > inv.DiskFreeGB*(1<<30) {
		fmt.Fprintf(w, "WARNING: rehearsal copies %s of data but only %.1fGB is free (needs 1.2×). Free disk space first.\n", humanSize(total), inv.DiskFreeGB)
	}
	fmt.Fprintln(w, "Next: atoll zeabur import", s.Name, "--namespace NAMESPACE [--domain old.zeabur.app=new.example.com]")
}

func shortRef(s string) string {
	if len(s) > 60 {
		return s[:59] + "…"
	}
	return s
}

func newZeaburImportCmd(app *App) *cobra.Command {
	var ns, name, baseDomain string
	var services, domains []string
	cmd := &cobra.Command{
		Use:   "import SERVER --namespace NS [--services a,b] [--name NAME] [--domain old=new]... [--base-domain example.com]",
		Short: "Create the Atoll project for a Zeabur namespace (nothing starts and Zeabur is untouched)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			if ns == "" {
				return errors.New("--namespace is required (see `atoll zeabur scan " + args[0] + "`)")
			}
			ctx := ctxOf(cmd)
			s, err := c.ResolveServer(ctx, args[0])
			if err != nil {
				return err
			}
			dm, err := zeabur.ParseDomainMap(domains)
			if err != nil {
				return err
			}
			req := api.ZeaburLocalImportRequest{ServerID: s.ID, Namespace: ns, ProjectName: name, DomainMap: dm}
			// Resolve service names to ids and propose *.zeabur.app replacements from the scan.
			inv, scanErr := c.ZeaburScan(ctx, s.ID)
			var proj *api.ZeaburProject
			if scanErr == nil {
				for i := range inv.Projects {
					if inv.Projects[i].Namespace == ns {
						proj = &inv.Projects[i]
					}
				}
				if proj == nil {
					return fmt.Errorf("namespace %s not found on %s (see `atoll zeabur scan %s`)", ns, s.Name, s.Name)
				}
			}
			for _, ref := range splitList(services) {
				id := ref
				if proj != nil {
					id = ""
					for _, sv := range proj.Services {
						if sv.ServiceID == ref || strings.EqualFold(sv.Name, ref) {
							id = sv.ServiceID
						}
					}
					if id == "" {
						return fmt.Errorf("service %q not found in %s", ref, ns)
					}
				}
				req.ServiceIDs = append(req.ServiceIDs, id)
			}
			if baseDomain == "" {
				if st, err := c.GetSettings(ctx); err == nil {
					baseDomain = st.BaseDomain
				}
			}
			baseDomain = strings.Trim(strings.ToLower(baseDomain), ".")
			if proj != nil {
				for _, sv := range proj.Services {
					for _, d := range sv.Domains {
						d = strings.ToLower(d)
						if !strings.HasSuffix(d, ".zeabur.app") {
							continue
						}
						if _, set := req.DomainMap[d]; set {
							continue
						}
						if baseDomain == "" {
							fmt.Fprintf(app.Err, "! %s has no replacement: set a base domain (panel Settings or --base-domain) or --domain %s=app.example.com; it stops working at cutover\n", d, d)
							continue
						}
						req.DomainMap[d] = strings.TrimSuffix(d, ".zeabur.app") + "." + baseDomain
					}
				}
			}
			if len(req.DomainMap) == 0 {
				req.DomainMap = nil
			}
			job, err := c.ZeaburLocalImport(ctx, req)
			if ok, err := app.handle(ctx, c, err); err != nil || !ok {
				return err
			}
			if app.JSON {
				return app.printJSON(job)
			}
			fmt.Fprintf(app.Out, "Import %s created project %s on %s (not started; Zeabur keeps serving).\n\n", job.ID, job.Plan.ProjectName, s.Name)
			tw := app.table()
			fmt.Fprintln(tw, "SERVICE\tKIND\tIMAGE\tPORTS\tVOLUMES\tDOMAINS")
			for _, sv := range job.Plan.Services {
				var ports []string
				for _, p := range sv.Ports {
					ports = append(ports, fmt.Sprintf("%d/%s", p.ContainerPort, p.Protocol))
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", sv.Name, sv.Kind, shortRef(sv.Image), strings.Join(ports, ","), len(sv.Volumes), strings.Join(sv.Domains, ","))
			}
			tw.Flush()
			if len(req.DomainMap) > 0 {
				fmt.Fprintln(app.Out, "\nDomain mapping:")
				for _, k := range sortedKeys(req.DomainMap) {
					v := req.DomainMap[k]
					if v == "" {
						v = "(dropped)"
					}
					fmt.Fprintf(app.Out, "  %s → %s\n", k, v)
				}
				fmt.Fprintln(app.Out, "  New hostnames need a DNS record pointing at this server's IP (", s.PublicIPv4, ").")
			}
			for _, w := range job.Plan.Warnings {
				fmt.Fprintln(app.Out, "!", w)
			}
			fmt.Fprintf(app.Out, "\nNext: atoll zeabur rehearse %s\n", job.ID)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&ns, "namespace", "", "Zeabur namespace (environment-...) from `atoll zeabur scan`")
	f.StringSliceVar(&services, "services", nil, "only these Zeabur services (names or ids, comma separated)")
	f.StringVar(&name, "name", "", "Atoll project name (default: the Zeabur project name)")
	f.StringArrayVar(&domains, "domain", nil, "replace a hostname: old=new (old= drops it), repeatable")
	f.StringVar(&baseDomain, "base-domain", "", "base domain for *.zeabur.app replacements (default: panel Settings)")
	return cmd
}

func splitList(in []string) []string {
	var out []string
	for _, s := range in {
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

var stepText = map[string]struct{ ask, done, next string }{
	"rehearse": {
		ask:  "Start Atoll copies of %d service(s) next to Zeabur (no public traffic), copy volumes and databases and verify row counts? Zeabur keeps serving.",
		done: "Rehearsal finished. Check the services in the panel (they are not public yet).",
		next: "Next: atoll zeabur cutover %s",
	},
	"cutover": {
		ask:  "Cut over %d service(s): scale Zeabur apps to 0, do a final data sync, park Zeabur's ingress (shared by every Zeabur project on this server) and let Atoll serve ports 80/443? Expect a short downtime.",
		done: "Cutover finished: Atoll now serves your domains. Verify them now; *.zeabur.app URLs no longer work.",
		next: "If anything is wrong: atoll zeabur rollback %s   (keep this possible until things are stable; cleanup much later)",
	},
	"rollback": {
		ask:  "Roll back: stop the Atoll copies of %d service(s), restore Zeabur's ingress (only when no other import on the server stays cut over) and scale Zeabur workloads back?",
		done: "Rolled back: Zeabur serves again.",
		next: "You can rehearse again later: atoll zeabur rehearse %s",
	},
}

func newZeaburStepCmd(app *App, step string) *cobra.Command {
	var yes, force, ignoreMem bool
	var server string
	txt := stepText[step]
	short := map[string]string{
		"rehearse": "Run Atoll copies without public traffic, copy data and verify (Zeabur keeps serving)",
		"cutover":  "Switch traffic from Zeabur to Atoll on the same machine (several imports of one server together)",
		"rollback": "Undo a cutover: Zeabur serves again (--server: every cut-over import of the server)",
	}[step]
	use := step + " IMPORT_ID [--yes]"
	args := cobra.ExactArgs(1)
	switch step {
	case "cutover":
		use, args = "cutover IMPORT_ID... [--force] [--yes]", cobra.MinimumNArgs(1)
	case "rollback":
		use, args = "rollback IMPORT_ID | --server SERVER [--yes]", cobra.MaximumNArgs(1)
	}
	cmd := &cobra.Command{
		Use: use, Short: short, Args: args,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			if step == "rollback" && server != "" {
				if len(args) > 0 {
					return errors.New("give an import ID or --server, not both")
				}
				return app.zeaburNodeRollback(ctx, c, server, yes)
			}
			if len(args) == 0 {
				return errors.New("an import ID is required (or --server SERVER)")
			}
			if step == "cutover" && len(args) > 1 {
				return app.zeaburNodeCutover(ctx, c, args, force, yes)
			}
			job, err := c.GetImport(ctx, args[0])
			if err != nil {
				return err
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
				call = func(ctx context.Context, id string) (*api.ImportJob, error) {
					return c.CutoverImportWith(ctx, id, api.ZeaburCutoverRequest{Force: force})
				}
			default:
				call = c.RollbackImport
			}
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
				fmt.Fprintf(app.Out, "%s failed. Zeabur objects were not deleted.", step)
				if step == "cutover" {
					fmt.Fprintf(app.Out, " Roll back with: atoll zeabur rollback %s", job.ID)
				}
				fmt.Fprintln(app.Out)
				return err
			}
			fmt.Fprintln(app.Out, txt.done)
			fmt.Fprintf(app.Out, txt.next+"\n", job.ID)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	switch step {
	case "cutover":
		cmd.Flags().BoolVar(&force, "force", false, "park the shared Zeabur ingress even though other Zeabur projects on the server still serve (they go offline)")
	case "rehearse":
		cmd.Flags().BoolVar(&ignoreMem, "ignore-memory", false, "skip the free-memory check")
	case "rollback":
		cmd.Flags().StringVar(&server, "server", "", "roll back every cut-over import of this server (name or id) together")
	}
	return cmd
}

// zeaburNodeCutover cuts over several imports of one server together and follows each job.
func (app *App) zeaburNodeCutover(ctx context.Context, c *client.Client, ids []string, force, yes bool) error {
	var serverID string
	services := 0
	jobs := map[string]*api.ImportJob{}
	for _, id := range ids {
		j, err := c.GetImport(ctx, id)
		if err != nil {
			return err
		}
		if serverID == "" {
			serverID = j.ServerID
		} else if j.ServerID != serverID {
			return fmt.Errorf("import %s is on another server: a node cutover covers imports of one server", id)
		}
		jobs[id] = j
		services += len(j.Plan.Services)
	}
	if !zeabur.Confirm(app.In, app.Err, yes, "Cut over %d import(s) (%d service(s)) together: scale their Zeabur apps to 0, final data sync for each, park Zeabur's ingress once and let Atoll serve ports 80/443? Expect a short downtime.", len(ids), services) {
		return errors.New("cancelled; nothing was changed")
	}
	before := map[string]string{}
	op, err := c.ZeaburNodeCutover(ctx, serverID, api.ZeaburNodeCutoverRequest{ImportIDs: ids, Force: force})
	if err == nil {
		for _, j := range op.Jobs {
			before[j.ID] = j.Status
		}
	} else if ok, err := app.handle(ctx, c, err); err != nil || !ok {
		return err
	}
	return app.waitNode(ctx, c, ids, jobs, before, "cutover")
}

// zeaburNodeRollback rolls back every cut-over import of a server.
func (app *App) zeaburNodeRollback(ctx context.Context, c *client.Client, ref string, yes bool) error {
	s, err := c.ResolveServer(ctx, ref)
	if err != nil {
		return err
	}
	if !zeabur.Confirm(app.In, app.Err, yes, "Roll back every cut-over Zeabur import on %s: stop their Atoll copies, restore Zeabur's ingress and scale Zeabur workloads back?", s.Name) {
		return errors.New("cancelled; nothing was changed")
	}
	op, err := c.ZeaburNodeRollback(ctx, s.ID)
	if err != nil {
		return err
	}
	var ids []string
	before := map[string]string{}
	for _, j := range op.Jobs {
		ids = append(ids, j.ID)
		before[j.ID] = j.Status
	}
	return app.waitNode(ctx, c, ids, nil, before, "rollback")
}

// waitNode follows each job of a node operation and reports per-job outcomes.
func (app *App) waitNode(ctx context.Context, c *client.Client, ids []string, jobs map[string]*api.ImportJob, before map[string]string, step string) error {
	var finals []*api.ImportJob
	var failed []string
	for _, id := range ids {
		b := before[id]
		if b == "" && jobs[id] != nil {
			b = jobs[id].Status
		}
		if !app.JSON {
			fmt.Fprintf(app.Err, "%s:\n", id)
		}
		final, err := app.waitImport(ctx, c, id, step, b)
		if final != nil {
			finals = append(finals, final)
		}
		if err != nil {
			failed = append(failed, id)
		}
	}
	if app.JSON {
		app.printJSON(finals)
	} else {
		for _, j := range finals {
			fmt.Fprintf(app.Out, "%s  %s  %s\n", j.ID, j.Plan.ProjectName, j.Status)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s did not complete for %s", step, strings.Join(failed, ", "))
	}
	if !app.JSON {
		fmt.Fprintln(app.Out, stepText[step].done)
		if step == "cutover" {
			fmt.Fprintln(app.Out, "If anything is wrong, roll back all of them together: atoll zeabur rollback --server SERVER")
		}
	}
	return nil
}

// importOutcome lists, per step, the status that means success and the other statuses after
// which the server does no more work (see internal/server/zeabur.go).
var importOutcome = map[string]struct {
	ok   string
	ends []string
}{
	"rehearse": {"rehearsed", []string{"failed"}},
	"cutover":  {"live", []string{"failed", "rolled_back"}}, // a failed cutover rolls back by itself
	"rollback": {"rolled_back", []string{"failed"}},
	"cleanup":  {"cleaned", []string{"failed", "live"}}, // a failed uninstall leaves it live
}

// waitImport follows a job with WaitImport, printing each step once as it changes, until the
// step's outcome is reached. before is the status prior to the request: when the request went
// through a panel approval the server may not have started yet, so that status is not taken as
// the outcome until something else has been seen (or a few polls have passed).
func (app *App) waitImport(ctx context.Context, c *client.Client, id, step, before string) (*api.ImportJob, error) {
	out := importOutcome[step]
	wctx, cancel := context.WithTimeout(ctx, app.WaitTimeout)
	defer cancel()
	seen := map[string]string{}
	var last *api.ImportJob
	reached, moved, polls := false, false, 0
	job, err := c.WaitImport(wctx, id, importPoll, func(j *api.ImportJob) {
		last = j
		polls++
		for _, s := range j.Steps {
			key := s.Status + " " + s.Detail
			if seen[s.Name] != key {
				seen[s.Name] = key
				if !app.JSON {
					line := fmt.Sprintf("  [%s] %s", s.Status, s.Name)
					if s.Detail != "" {
						line += " — " + s.Detail
					}
					fmt.Fprintln(app.Err, line)
				}
			}
		}
		if j.Status != before {
			moved = true
		}
		if !moved && polls < 5 {
			return
		}
		if j.Status == out.ok || j.Status == "done" {
			reached = true
			cancel()
			return
		}
		for _, e := range out.ends {
			if j.Status == e {
				reached = true
				cancel()
			}
		}
	})
	if job == nil {
		job = last
	}
	if reached {
		if job != nil && (job.Status == out.ok || job.Status == "done") {
			return job, nil
		}
		st := ""
		if job != nil {
			st = job.Status
		}
		return job, fmt.Errorf("%s did not complete (import %s is %s)", step, id, st)
	}
	return job, err
}

// importPoll is the WaitImport interval (tests shorten it).
var importPoll = 3 * time.Second

func newZeaburStatusCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use: "status IMPORT_ID", Short: "Show an import job and its steps", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			job, err := c.GetImport(ctxOf(cmd), args[0])
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(job)
			}
			fmt.Fprintf(app.Out, "Import %s: %s (project %s, %d services)\n", job.ID, job.Status, job.ProjectID, len(job.Plan.Services))
			tw := app.table()
			fmt.Fprintln(tw, "STEP\tSTATUS\tDETAIL")
			for _, s := range job.Steps {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, s.Status, s.Detail)
			}
			return tw.Flush()
		},
	}
}

func newZeaburCleanupCmd(app *App) *cobra.Command {
	var noBackup bool
	var confirm string
	cmd := &cobra.Command{
		Use:   "cleanup IMPORT_ID [--no-backup]",
		Short: "Uninstall Zeabur's k3s from the server to free RAM/disk (IRREVERSIBLE: no rollback afterwards)",
		Long: `Runs k3s-uninstall.sh on the server after a completed cutover. This deletes Zeabur's k3s,
its containers and (unless kept as backup) its PVC directories. Rollback is impossible afterwards.
Only do this once everything has run stably on Atoll for a while. You must type the server name.`,
		Args: cobra.ExactArgs(1),
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
			if job.Plan.TargetServer == "" {
				return fmt.Errorf("import %s has no target server", job.ID)
			}
			s, err := c.GetServer(ctx, job.Plan.TargetServer)
			if err != nil {
				return err
			}
			backup := "PVC directories are archived first to /var/lib/atoll/zeabur-backup/ on the server."
			if noBackup {
				backup = "NO backup of Zeabur's PVC directories will be kept (--no-backup)."
			}
			fmt.Fprintf(app.Err, "This uninstalls Zeabur's k3s from %s (%s). It cannot be undone and `atoll zeabur rollback` will no longer work.\n%s\n", s.Name, s.PublicIPv4, backup)
			typed := confirm
			if typed == "" {
				fmt.Fprintf(app.Err, "Type the server name (%s) to confirm: ", s.Name)
				line, _ := bufio.NewReader(app.In).ReadString('\n')
				typed = strings.TrimSpace(line)
			}
			if typed != s.Name {
				return fmt.Errorf("confirmation %q does not match the server name %q; nothing was done", typed, s.Name)
			}
			before := job.Status
			if started, err := c.CleanupImport(ctx, job.ID, api.ZeaburCleanupRequest{Confirm: typed, KeepBackup: !noBackup}); err == nil && started != nil && started.Status != "" {
				before = started.Status
			} else if ok, err := app.handle(ctx, c, err); err != nil || !ok {
				return err
			}
			final, err := app.waitImport(ctx, c, job.ID, "cleanup", before)
			if app.JSON {
				if final != nil {
					app.printJSON(final)
				}
				return err
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "Zeabur's k3s was removed from %s.\n", s.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noBackup, "no-backup", false, "do not archive Zeabur's PVC directories before uninstalling")
	cmd.Flags().StringVar(&confirm, "confirm", "", "the server name, for non-interactive use (must match exactly)")
	return cmd
}

func newTab(w io.Writer) *tabwriter.Writer { return tabwriter.NewWriter(w, 2, 4, 2, ' ', 0) }
