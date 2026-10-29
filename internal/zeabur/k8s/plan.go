package k8s

import (
	"fmt"
	"sort"
	"strings"

	"github.com/atoll-sh/atoll/internal/api"
)

// PlanOptions configure BuildPlan.
type PlanOptions struct {
	SourceProject string
	ProjectName   string
	TargetServer  string
	ServerIPv4    string
	// Exposed lists keys (per Zeabur service id) that Zeabur marks as exposed, when known
	// from the Zeabur CLI (`variable list` / `project export`).
	Exposed map[string][]string
	// Received maps Zeabur service id → variables it receives from other services
	// (`readonlyVariables`). Used to add received keys missing from the pod spec.
	Received map[string]map[string]string
	// ImageOverride maps Zeabur service id → image ref to use (e.g. after re-push).
	ImageOverride map[string]string
	// BaseDomain and DomainMap decide where *.zeabur.app hosts go (see DefaultDomainMap).
	// Old hosts are replaced in env values and config files, and the new host is added to
	// the service's domains.
	BaseDomain string
	DomainMap  map[string]string
}

// BuildPlan converts scanned Zeabur services into an Atoll ImportPlan: env translated and
// `.zeabur.internal` rewritten, floating images pinned to the running digest, Zeabur-registry
// images flagged ImageNeedsPush (unless overridden), *.zeabur.app domains moved to Warnings.
func BuildPlan(services []api.ZeaburService, opt PlanOptions) api.ImportPlan {
	plan := api.ImportPlan{
		Source:        "zeabur",
		SourceProject: opt.SourceProject,
		ProjectName:   opt.ProjectName,
		TargetServer:  opt.TargetServer,
		Services:      []api.ImportService{},
	}
	var hosts []string
	for _, s := range services {
		hosts = append(hosts, s.Domains...)
	}
	dm := DefaultDomainMap(hosts, opt.BaseDomain, opt.DomainMap)
	rw := Rewriter{ByDNSName: map[string]string{}, ByServiceID: map[string]string{}, Domains: dm}
	used := map[string]bool{}
	names := map[string]string{}
	for _, s := range services {
		n := SanitizeName(s.Name)
		base := n
		for i := 2; used[n]; i++ {
			n = fmt.Sprintf("%s-%d", base, i)
		}
		used[n] = true
		names[s.ServiceID] = n
		rw.ByDNSName[s.Name] = n
		rw.ByServiceID[s.ServiceID] = n
	}
	warn := func(f string, a ...any) { plan.Warnings = append(plan.Warnings, fmt.Sprintf(f, a...)) }

	for _, s := range services {
		name := names[s.ServiceID]
		if s.Workload == "" {
			warn("%s: no running workload (suspended?); only its volumes can be migrated — skipped from the plan", name)
			continue
		}
		is := api.ImportService{
			SourceServiceID: s.ServiceID,
			Name:            name,
			Kind:            s.Kind,
			Image:           PinImage(s.Image, s.ImageID),
			Ports:           append([]api.Port{}, s.Ports...),
			Volumes:         []api.Volume{},
			Env:             []api.EnvVar{},
			Domains:         []string{},
		}
		if is.Kind == "" {
			is.Kind = "image"
		}
		if img, ok := opt.ImageOverride[s.ServiceID]; ok && img != "" {
			is.Image = img
		} else if s.ZeaburRegistry {
			is.ImageNeedsPush = true
			warn("%s: image was built by Zeabur (%s registry stops serving); it must be exported from the server or rebuilt", name, strings.SplitN(s.Image, "/", 2)[0])
		}
		is.Command = append([]string{}, s.Command...)
		if len(s.Args) > 0 {
			is.Args = append([]string{}, s.Args...)
		}
		env := append([]api.EnvVar{}, s.Env...)
		have := map[string]bool{}
		for _, e := range env {
			have[e.Key] = true
		}
		for k, v := range opt.Received[s.ServiceID] {
			if !have[k] {
				env = append(env, api.EnvVar{Key: k, Value: v})
			}
		}
		exposed := map[string]bool{}
		for _, k := range opt.Exposed[s.ServiceID] {
			exposed[k] = true
		}
		tr := TranslateEnv(env, TranslateContext{Service: name, Rewriter: rw, ServerIPv4: opt.ServerIPv4})
		for _, e := range tr.Env {
			e.Exposed = e.Exposed || exposed[e.Key]
			is.Env = append(is.Env, e)
		}
		sort.SliceStable(is.Env, func(i, j int) bool { return is.Env[i].Key < is.Env[j].Key })
		plan.Warnings = append(plan.Warnings, tr.Warnings...)
		for i := range is.Ports {
			if np, ok := tr.PublicPorts[is.Ports[i].Name]; ok && is.Ports[i].Protocol != "http" {
				is.Ports[i].PublicPort = np
			}
		}
		for i, v := range s.Volumes {
			vn := VolumeName(v.PVC, s.ServiceID, i)
			is.Volumes = append(is.Volumes, api.Volume{Name: vn, MountPath: v.MountPath})
			is.DataBytes += v.Bytes
		}
		for _, c := range s.Configs {
			is.Configs = append(is.Configs, api.ConfigFile{Path: c.Path, Content: rw.Rewrite(c.Content)})
		}
		for _, d := range s.Domains {
			if nd, ok := dm[strings.ToLower(d)]; ok {
				if nd == "" {
					warn("%s: generated domain %s stops resolving at shutdown and has no replacement; pass --domain %s=NEW or --base-domain", name, d, d)
					continue
				}
				warn("%s: %s → %s (replaced in env and config files); update OAuth callbacks, webhooks and payment settings that used the old URL", name, d, nd)
				d = nd
			}
			if !contains(is.Domains, d) {
				is.Domains = append(is.Domains, d)
			}
		}
		for _, w := range s.Warnings {
			warn("%s: %s", name, w)
		}
		plan.Services = append(plan.Services, is)
	}
	return plan
}

// VolumeName derives a volume name from a Zeabur PVC name (<volumeID>-service-<serviceID>).
func VolumeName(pvc, serviceID string, i int) string {
	n := strings.TrimSuffix(pvc, "-service-"+serviceID)
	if n == "" || n == pvc && strings.Contains(pvc, serviceID) {
		n = fmt.Sprintf("data%d", i)
	}
	return SanitizeName(n)
}
