package zeabur

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/atoll-sh/atoll/internal/api"
	"github.com/atoll-sh/atoll/internal/zeabur/k8s"
)

// Inventory is everything read about the chosen Zeabur projects. It contains secret
// values (variables, effective env): keep it in memory or write it with WriteSecretJSON.
type Inventory struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Workspace   string             `json:"workspace,omitempty"`
	AuthStatus  string             `json:"auth_status"`
	Projects    []ProjectInventory `json:"projects"`
}

type ProjectInventory struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	RegionID  string              `json:"region_id"`
	ServerID  string              `json:"server_id,omitempty"` // dedicated server
	Server    *Server             `json:"server,omitempty"`
	Host      *HostAccess         `json:"host,omitempty"` // set when host reads worked
	Namespace string              `json:"namespace,omitempty"`
	Services  []ServiceInventory  `json:"services"`
	K8s       []api.ZeaburService `json:"k8s_services,omitempty"` // from the host (most reliable)
	Exposed   map[string][]string `json:"exposed,omitempty"`      // service name → exposed keys (project export)
	Export    *ExportSpec         `json:"export,omitempty"`
	Notes     []string            `json:"notes,omitempty"`
}

type ServiceInventory struct {
	Service   Service           `json:"service"`
	Variables Variables         `json:"-"`
	Own       map[string]string `json:"variables"`          // SECRET
	Readonly  map[string]string `json:"readonly_variables"` // SECRET
	Domains   []Domain          `json:"domains"`
}

// LoadInventory reads an inventory.json written by WriteSecretJSON.
func LoadInventory(path string) (*Inventory, error) {
	var inv Inventory
	if err := ReadJSON(path, &inv); err != nil {
		return nil, err
	}
	for i := range inv.Projects {
		for j := range inv.Projects[i].Services {
			s := &inv.Projects[i].Services[j]
			s.Variables = Variables{Own: s.Own, Readonly: s.Readonly}
		}
	}
	return &inv, nil
}

// Project returns the inventory of one project by id or name.
func (inv *Inventory) Project(ref string) (*ProjectInventory, bool) {
	for i := range inv.Projects {
		if inv.Projects[i].ID == ref || inv.Projects[i].Name == ref {
			return &inv.Projects[i], true
		}
	}
	return nil, false
}

// InventoryOptions select what to read.
type InventoryOptions struct {
	ProjectIDs  []string
	Environment string      // environment id when a project has several namespaces
	SSH         *HostAccess // direct SSH instead of `zeabur server exec`
	NoHost      bool        // skip host reads
	Log         io.Writer   // progress (no secrets)
}

func logf(w io.Writer, f string, a ...any) {
	if w != nil {
		fmt.Fprintf(w, f+"\n", a...)
	}
}

// AuthStatus checks the Zeabur CLI is logged in.
func AuthStatus(ctx context.Context, cli Runner) (string, error) {
	out, err := cli.Run(ctx, false, "auth", "status")
	if err != nil {
		return "", err
	}
	s := filterLines(string(out))
	if isUnauthorized([]byte(s)) {
		return "", ErrNotLoggedIn
	}
	return strings.TrimSpace(s), nil
}

// ListProjects lists the projects of the current workspace.
func ListProjects(ctx context.Context, cli Runner) ([]Project, error) {
	out, err := cli.Run(ctx, true, "project", "list")
	if err != nil {
		return nil, err
	}
	return ParseProjects(out)
}

// GatherInventory reads the chosen projects through the Zeabur CLI and, for dedicated
// servers, through the host's k3s. Nothing is changed on Zeabur.
func GatherInventory(ctx context.Context, cli Runner, opt InventoryOptions) (*Inventory, error) {
	inv := &Inventory{GeneratedAt: time.Now().UTC()}
	st, err := AuthStatus(ctx, cli)
	if err != nil {
		return nil, err
	}
	inv.AuthStatus = st
	projects, err := ListProjects(ctx, cli)
	if err != nil {
		return nil, err
	}
	if len(opt.ProjectIDs) == 0 {
		return nil, fmt.Errorf("choose the projects to move with --project (agree on scope first); found: %s", projectList(projects))
	}
	want := map[string]bool{}
	for _, id := range opt.ProjectIDs {
		want[id] = true
	}
	for _, p := range projects {
		if !want[p.ID] && !want[p.Name] {
			continue
		}
		delete(want, p.ID)
		delete(want, p.Name)
		pi, err := gatherProject(ctx, cli, p, opt)
		if err != nil {
			return nil, err
		}
		inv.Projects = append(inv.Projects, *pi)
	}
	for id := range want {
		return nil, fmt.Errorf("project %q not found in this workspace (use --workspace for team workspaces); found: %s", id, projectList(projects))
	}
	return inv, nil
}

func projectList(ps []Project) string {
	var s []string
	for _, p := range ps {
		s = append(s, p.Name+" ("+p.ID+")")
	}
	return strings.Join(s, ", ")
}

func gatherProject(ctx context.Context, cli Runner, p Project, opt InventoryOptions) (*ProjectInventory, error) {
	pi := &ProjectInventory{ID: p.ID, Name: p.Name, RegionID: p.Region.ID, ServerID: p.DedicatedServerID()}
	logf(opt.Log, "• project %s (%s), region %s", p.Name, p.ID, p.Region.ID)
	if pi.ServerID != "" {
		out, err := cli.Run(ctx, true, "server", "get", pi.ServerID)
		if err != nil {
			out, err = cli.Run(ctx, true, "server", "get", "--id", pi.ServerID)
		}
		if err == nil {
			if s, err := ParseServer(out); err == nil {
				pi.Server = &s
			}
		} else {
			pi.Notes = append(pi.Notes, "server get failed: "+err.Error())
		}
		pi.Notes = append(pi.Notes, "Read the server's expiry date from its Settings tab in the Zeabur dashboard; plan the move before it (the CLI does not return it).")
	} else {
		pi.Notes = append(pi.Notes, "Shared cluster: no host access; only the Zeabur CLI view is available (no image export, data via service exec).")
	}
	if out, err := cli.Run(ctx, false, "project", "export", "--id", p.ID); err == nil {
		if e, err := ParseExport(out); err == nil {
			pi.Export = e
			pi.Exposed = e.ExposedKeys()
		}
	} else {
		pi.Notes = append(pi.Notes, "project export failed: "+err.Error())
	}
	out, err := cli.Run(ctx, true, "service", "list", "--project-id", p.ID)
	if err != nil {
		return nil, err
	}
	svcs, err := ParseServices(out)
	if err != nil {
		return nil, err
	}
	for _, s := range svcs {
		si := ServiceInventory{Service: s}
		if out, err := cli.Run(ctx, true, "service", "get", "--id", s.ID); err == nil {
			if full, err := ParseService(out); err == nil && full.ID != "" {
				si.Service = full
			}
		}
		if out, err := cli.Run(ctx, true, "variable", "list", "--id", s.ID); err == nil {
			if v, err := ParseVariables(out); err == nil {
				si.Variables = v
			}
		} else {
			pi.Notes = append(pi.Notes, fmt.Sprintf("variable list for %s failed: %v", s.Name, err))
		}
		si.Own, si.Readonly = si.Variables.Own, si.Variables.Readonly
		if out, err := cli.Run(ctx, true, "domain", "list", "--id", s.ID); err == nil {
			si.Domains, _ = ParseDomains(out)
		}
		pi.Services = append(pi.Services, si)
	}
	if pi.ServerID != "" && !opt.NoHost {
		access := HostAccess{Mode: "server-exec", ServerID: pi.ServerID}
		if opt.SSH != nil {
			access = *opt.SSH
		}
		host := HostTransport{Access: access, CLI: cli}
		if err := gatherHost(ctx, host, pi, opt); err != nil {
			pi.Notes = append(pi.Notes, "host inventory unavailable ("+err.Error()+"); falling back to the Zeabur CLI view. Host-level blind spots (other workloads, cron, bind mounts) are unverified.")
		} else {
			pi.Host = &access
		}
	}
	return pi, nil
}

func gatherHost(ctx context.Context, host HostTransport, pi *ProjectInventory, opt InventoryOptions) error {
	logf(opt.Log, "  reading k3s on the host via %s", host.Describe())
	out, err := host.Output(ctx, "k3s kubectl get ns -l zeabur_project_id="+shq(pi.ID)+" -o name")
	if err != nil {
		return err
	}
	var nss []string
	for _, l := range strings.Fields(out) {
		nss = append(nss, strings.TrimPrefix(l, "namespace/"))
	}
	if opt.Environment != "" {
		nss = []string{"environment-" + strings.TrimPrefix(opt.Environment, "environment-")}
	}
	switch len(nss) {
	case 0:
		return fmt.Errorf("no namespace labelled zeabur_project_id=%s", pi.ID)
	case 1:
	default:
		return fmt.Errorf("project has several environments (%s); pass --environment", strings.Join(nss, ", "))
	}
	ns := nss[0]
	pi.Namespace = ns
	yamlOut, err := host.Output(ctx, "k3s kubectl get ns "+shq(ns)+" -o yaml; echo '---'; k3s kubectl -n "+shq(ns)+" get deploy,sts,svc,pvc,cm,ingress,pods -o yaml")
	if err != nil {
		return err
	}
	objs, err := k8s.Parse([]byte(yamlOut))
	if err != nil {
		return err
	}
	facts := k8s.HostFacts{}
	if du, err := host.Output(ctx, "du -sb "+k8s.StorageRoot+"/* 2>/dev/null || true"); err == nil {
		facts.DirBytes = k8s.ParseDu(du)
	}
	if imgs, err := host.Output(ctx, "k3s ctr -n k8s.io images ls"); err == nil {
		facts.CachedImages = k8s.ParseCtrImages(imgs)
	}
	for _, p := range k8s.ScanNamespaces(objs, facts) {
		if p.Namespace == ns {
			pi.K8s = p.Services
		}
	}
	return nil
}

// Services returns the project's services in the shared ZeaburService shape: the host view
// when available (effective env), else synthesized from the CLI (variables expanded locally).
func (pi *ProjectInventory) ServicesView() []api.ZeaburService {
	byID := map[string]ServiceInventory{}
	for _, s := range pi.Services {
		byID[s.Service.ID] = s
	}
	if len(pi.K8s) > 0 {
		out := make([]api.ZeaburService, len(pi.K8s))
		for i, ks := range pi.K8s {
			if si, ok := byID[ks.ServiceID]; ok {
				for _, d := range si.Domains {
					if d.Domain != "" && !containsStr(ks.Domains, d.Domain) {
						ks.Domains = append(ks.Domains, d.Domain)
					}
				}
			}
			out[i] = ks
		}
		return out
	}
	var exp map[string]ExportService
	if pi.Export != nil {
		exp = map[string]ExportService{}
		for _, s := range pi.Export.Spec.Services {
			exp[s.Name] = s
		}
	}
	var out []api.ZeaburService
	for _, si := range pi.Services {
		out = append(out, synthesize(si, exp[si.Service.Name]))
	}
	return out
}

func containsStr(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// synthesize builds a ZeaburService from the CLI view (shared clusters / no host access).
func synthesize(si ServiceInventory, ex ExportService) api.ZeaburService {
	s := si.Service
	zs := api.ZeaburService{ServiceID: s.ID, Name: k8s.SanitizeName(s.Name), Workload: "deployment/service-" + s.ID, Replicas: 1,
		Ports: []api.Port{}, Env: []api.EnvVar{}, Volumes: []api.ZeaburVolume{}, Domains: []string{}}
	if strings.EqualFold(s.Status, "SUSPENDED") {
		zs.Warnings = append(zs.Warnings, "service is suspended on Zeabur")
	}
	zs.Image = s.Spec.Source.Image
	if zs.Image == "" {
		zs.Image = ex.Spec.Source.Image
	}
	if zs.Image == "" && (strings.EqualFold(s.Template, "GIT") || s.GitTrigger != nil) {
		zs.ZeaburRegistry = true
		zs.Warnings = append(zs.Warnings, "built from Git on Zeabur; without host access the image cannot be exported — rebuild it (Dockerfile / zbpack behaviour) and push to your registry")
	}
	zs.Kind, zs.Role = k8s.DetectKind(zs.Image)
	if zs.ZeaburRegistry {
		zs.Kind, zs.Role = "image", "app"
	}
	// Zeabur built-ins, lowest priority: CONTAINER_HOSTNAME, <PORT>_PORT, ZEABUR_<PORT>_DOMAIN/URL.
	recv := map[string]string{"CONTAINER_HOSTNAME": "service-" + s.ID}
	for _, p := range ex.Spec.Ports {
		recv[strings.ToUpper(p.ID)+"_PORT"] = fmt.Sprint(p.Port)
	}
	for _, d := range si.Domains {
		if d.Domain == "" {
			continue
		}
		port := strings.ToUpper(d.PortName)
		if port == "" {
			port = "WEB"
		}
		if _, ok := recv["ZEABUR_"+port+"_DOMAIN"]; !ok || !d.IsGenerated {
			recv["ZEABUR_"+port+"_DOMAIN"] = d.Domain
			recv["ZEABUR_"+port+"_URL"] = "https://" + d.Domain
		}
	}
	for k, v := range si.Variables.Readonly {
		recv[k] = v
	}
	expanded, unresolved := k8s.Expand(si.Variables.Own, recv)
	keys := make([]string, 0, len(expanded))
	for k := range expanded {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		zs.Env = append(zs.Env, api.EnvVar{Key: k, Value: expanded[k], Secret: k8s.IsSecret(k, expanded[k])})
	}
	if len(unresolved) > 0 {
		zs.Warnings = append(zs.Warnings, "unresolved variable references (platform built-ins?): "+strings.Join(unresolved, ", "))
	}
	for _, p := range ex.Spec.Ports {
		proto := strings.ToLower(p.Type)
		if proto == "" {
			proto = "http"
		}
		zs.Ports = append(zs.Ports, api.Port{Name: k8s.SanitizeName(p.ID), ContainerPort: p.Port, Protocol: proto})
	}
	if len(zs.Ports) == 0 && zs.ZeaburRegistry {
		zs.Ports = []api.Port{{Name: "web", ContainerPort: 8080, Protocol: "http"}}
		zs.Warnings = append(zs.Warnings, "port unknown; assumed 8080 (Zeabur's default for Git services)")
	}
	for _, v := range ex.Spec.Volumes {
		zs.Volumes = append(zs.Volumes, api.ZeaburVolume{PVC: v.ID + "-service-" + s.ID, MountPath: v.Dir})
	}
	for _, c := range ex.Spec.Configs {
		zs.Configs = append(zs.Configs, api.ConfigFile{Path: c.Path, Content: c.Template})
	}
	for _, d := range si.Domains {
		if d.Domain != "" {
			zs.Domains = append(zs.Domains, d.Domain)
		}
	}
	return zs
}

// ---------- report ----------

// Risks lists migration risks for one service (no secret values).
func Risks(s api.ZeaburService, shared bool) []string {
	var r []string
	if s.ZeaburRegistry {
		r = append(r, "zeabur-registry image")
	}
	for _, d := range s.Domains {
		if k8s.IsGeneratedDomain(d) {
			r = append(r, "*.zeabur.app domain")
			break
		}
	}
	internal := false
	for _, e := range s.Env {
		if strings.Contains(e.Value, ".zeabur.internal") || strings.Contains(e.Value, "service-"+s.ServiceID) {
			internal = true
		}
	}
	for _, c := range s.Configs {
		if strings.Contains(c.Content, ".zeabur.internal") {
			internal = true
		}
	}
	if internal {
		r = append(r, ".zeabur.internal refs (rewritten)")
	}
	if len(s.NodePorts) > 0 {
		r = append(r, "public NodePort")
	}
	if shared {
		r = append(r, "shared cluster: no host access")
	}
	if s.Workload == "" {
		r = append(r, "suspended")
	}
	return r
}

func humanBytes(n int64) string {
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

// PrintReport writes one row per service; values are never printed, only variable names.
func PrintReport(w io.Writer, inv *Inventory) {
	for _, p := range inv.Projects {
		kind := "shared cluster"
		if p.ServerID != "" {
			kind = "dedicated server " + p.ServerID
			if p.Server != nil {
				if p.Server.IsManaged {
					kind += " (rented through Zeabur: deleted after expiry, disk not kept)"
				} else {
					kind += " (your own machine)"
				}
			}
		}
		fmt.Fprintf(w, "\nProject %s (%s) — %s", p.Name, p.ID, kind)
		if p.Namespace != "" {
			fmt.Fprintf(w, ", namespace %s", p.Namespace)
		}
		fmt.Fprintln(w)
		tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "SERVICE\tROLE\tSOURCE\tIMAGE\tPORTS\tDOMAINS\tVOLUMES\tRECEIVED VARS\tIMAGE\tRISKS")
		recv := map[string][]string{}
		for _, si := range p.Services {
			for k := range si.Variables.Readonly {
				recv[si.Service.ID] = append(recv[si.Service.ID], k)
			}
			sort.Strings(recv[si.Service.ID])
		}
		src := map[string]string{}
		for _, si := range p.Services {
			s := si.Service
			switch {
			case s.GitTrigger != nil && s.GitTrigger.RepoFullName != "":
				src[s.ID] = "git:" + s.GitTrigger.RepoFullName + "@" + s.GitTrigger.Branch
			case strings.EqualFold(s.Template, "GIT"):
				src[s.ID] = "git"
			default:
				src[s.ID] = "image"
			}
		}
		for _, s := range p.ServicesView() {
			var ports, vols []string
			for _, pt := range s.Ports {
				x := fmt.Sprintf("%s:%d/%s", pt.Name, pt.ContainerPort, pt.Protocol)
				if pt.PublicPort > 0 {
					x += fmt.Sprintf("→:%d", pt.PublicPort)
				}
				ports = append(ports, x)
			}
			for _, v := range s.Volumes {
				vols = append(vols, fmt.Sprintf("%s %s", v.MountPath, humanBytes(v.Bytes)))
			}
			img := k8s.PinImage(s.Image, s.ImageID)
			status := "public/pinnable"
			switch {
			case s.ZeaburRegistry && s.ImageCached && p.Host != nil:
				status = "exportable"
			case s.ZeaburRegistry:
				status = "rebuild"
			}
			source := src[s.ServiceID]
			if source == "" {
				source = "image"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, s.Role, source, shorten(img, 48),
				dash(strings.Join(ports, ",")), dash(strings.Join(s.Domains, ",")), dash(strings.Join(vols, ",")),
				dash(strings.Join(recv[s.ServiceID], ",")), status, dash(strings.Join(Risks(s, p.ServerID == ""), "; ")))
		}
		tw.Flush()
		for _, n := range p.Notes {
			fmt.Fprintln(w, "  note:", n)
		}
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// WriteSecretJSON writes v as JSON with mode 0600 (dir 0700). Use for anything holding secrets.
func WriteSecretJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// ReadJSON reads a JSON file into v.
func ReadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
