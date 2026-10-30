package zeabur

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/zeabur/k8s"
)

// PlanOptions configure BuildPlan.
type PlanOptions struct {
	TargetServer string
	ServerIPv4   string
	ProjectName  string
	BaseDomain   string
	DomainMap    map[string]string
}

// BuildPlan turns a project inventory into an ImportPlan (pure; changes nothing anywhere).
func BuildPlan(pi *ProjectInventory, opt PlanOptions) api.ImportPlan {
	exposed := map[string][]string{}
	received := map[string]map[string]string{}
	for _, si := range pi.Services {
		if keys, ok := pi.Exposed[si.Service.Name]; ok {
			exposed[si.Service.ID] = keys
		}
		if len(si.Variables.Readonly) > 0 {
			exp, _ := k8s.Expand(si.Variables.Readonly, nil)
			received[si.Service.ID] = exp
		}
	}
	name := opt.ProjectName
	if name == "" {
		name = k8s.SanitizeName(pi.Name)
	}
	plan := k8s.BuildPlan(pi.ServicesView(), k8s.PlanOptions{
		SourceProject: pi.ID, ProjectName: name, TargetServer: opt.TargetServer, ServerIPv4: opt.ServerIPv4,
		Exposed: exposed, Received: received, BaseDomain: opt.BaseDomain, DomainMap: opt.DomainMap,
	})
	if pi.ServerID == "" {
		plan.Warnings = append(plan.Warnings, "shared cluster: env was expanded locally from `variable list` (not read from a pod spec); verify every variable the code reads has a value")
	} else if pi.Host == nil {
		plan.Warnings = append(plan.Warnings, "dedicated server without host access: env expanded locally; host-level items (cron, bind mounts, other workloads) are unverified")
	}
	plan.Warnings = append(plan.Warnings,
		"database backups (Zeabur daily CronJob) are not migrated: set up your own backups and test a restore",
		"logs and metrics history are not migrated")
	return plan
}

// PrintPlan writes a human summary of plan with masked values.
func PrintPlan(w io.Writer, plan api.ImportPlan) {
	fmt.Fprintf(w, "Import plan: Zeabur project %s → Atoll project %q on %s\n", plan.SourceProject, plan.ProjectName, plan.TargetServer)
	for _, s := range plan.Services {
		fmt.Fprintf(w, "\n● %s  kind=%s  image=%s", s.Name, s.Kind, s.Image)
		if s.ImageNeedsPush {
			fmt.Fprint(w, "  [needs export+push]")
		}
		fmt.Fprintln(w)
		tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
		for _, p := range s.Ports {
			pub := ""
			if p.PublicPort > 0 {
				pub = fmt.Sprintf(" public:%d", p.PublicPort)
			}
			fmt.Fprintf(tw, "  port\t%s\t%d/%s%s\n", p.Name, p.ContainerPort, p.Protocol, pub)
		}
		for _, v := range s.Volumes {
			fmt.Fprintf(tw, "  volume\t%s\t%s\n", v.Name, v.MountPath)
		}
		for _, c := range s.Configs {
			fmt.Fprintf(tw, "  config\t%s\t%d bytes\n", c.Path, len(c.Content))
		}
		for _, d := range s.Domains {
			fmt.Fprintf(tw, "  domain\t%s\t\n", d)
		}
		for _, e := range s.Env {
			flags := ""
			if e.Exposed {
				flags = " (exposed)"
			}
			fmt.Fprintf(tw, "  env\t%s\t%s%s\n", e.Key, k8s.Mask(e.Key, e.Value), flags)
		}
		tw.Flush()
		if s.DataBytes > 0 {
			fmt.Fprintf(w, "  data: %s\n", humanBytes(s.DataBytes))
		}
	}
	if len(plan.Warnings) > 0 {
		fmt.Fprintln(w, "\nWarnings:")
		ws := append([]string(nil), plan.Warnings...)
		sort.Strings(ws)
		for _, x := range dedupe(ws) {
			fmt.Fprintln(w, "  -", x)
		}
	}
}

func dedupe(xs []string) []string {
	var out []string
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}

// Confirm asks a y/N question on in/out; yes skips the question.
func Confirm(in io.Reader, out io.Writer, yes bool, format string, a ...any) bool {
	if yes {
		return true
	}
	fmt.Fprintf(out, format+" [y/N] ", a...)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// ParseDomainMap parses repeated "old=new" flags ("old=" drops the host).
func ParseDomainMap(pairs []string) (map[string]string, error) {
	m := map[string]string{}
	for _, p := range pairs {
		i := strings.Index(p, "=")
		if i <= 0 {
			return nil, fmt.Errorf("--domain %q: want old=new", p)
		}
		m[strings.ToLower(strings.TrimSpace(p[:i]))] = strings.ToLower(strings.TrimSpace(p[i+1:]))
	}
	return m, nil
}
