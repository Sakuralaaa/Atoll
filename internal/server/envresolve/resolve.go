// Package envresolve expands environment variable references for one service
// of a project. It is a pure function of its input: no I/O, no randomness.
//
// Rules (see docs/api.md "Conventions"):
//   - ${KEY}: own vars, then built-ins of the service, then project built-ins
//     (<SERVICE>_HOST), then exposed vars of other services in the project.
//   - ${SERVICE.KEY}: explicit; SERVICE is matched case-insensitively and with
//     '-' / '_' treated alike. KEY may be an exposed var of that service or one
//     of its built-ins (HOST, PORT, <PORTNAME>_PORT, ATOLL_<PORTNAME>_DOMAIN/URL).
//     Referencing your own service name gives access to all your own vars.
//   - Built-ins: PORT (first port), <PORTNAME>_PORT, ATOLL_<PORTNAME>_DOMAIN,
//     ATOLL_<PORTNAME>_URL (https://domain) and <SERVICE>_HOST (the service name).
//   - "$${" is an escape for a literal "${".
//   - Cycles and unknown references are left verbatim and reported as warnings.
package envresolve

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Service is one service of the project as seen by the resolver.
type Service struct {
	Name    string
	Vars    []api.EnvVar // raw values, secrets already decrypted
	Ports   []api.Port
	Domains map[string]string // port name -> first hostname bound to it
}

// Input is everything needed to resolve the env of Self.
type Input struct {
	Self     string    // name of the service being resolved
	Services []Service // all services of the project, including Self
}

// Result is the resolved env plus human-readable warnings.
type Result struct {
	Env      map[string]string
	Warnings []string
}

// Norm turns a service or port name into its env-var form: "my-db" -> "MY_DB".
func Norm(s string) string {
	b := []byte(strings.ToUpper(s))
	for i, c := range b {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b[i] = '_'
		}
	}
	return string(b)
}

type resolver struct {
	byNorm   map[string]*Service
	order    []*Service // sorted by name, for deterministic sibling lookup
	cache    map[string]string
	visiting map[string]bool
	warnSeen map[string]bool
	warnings []string
}

func newResolver(in Input) *resolver {
	r := &resolver{
		byNorm:   map[string]*Service{},
		cache:    map[string]string{},
		visiting: map[string]bool{},
		warnSeen: map[string]bool{},
	}
	for i := range in.Services {
		s := &in.Services[i]
		r.byNorm[Norm(s.Name)] = s
		r.order = append(r.order, s)
	}
	sort.Slice(r.order, func(i, j int) bool { return r.order[i].Name < r.order[j].Name })
	return r
}

func (r *resolver) warn(format string, a ...any) {
	m := fmt.Sprintf(format, a...)
	if !r.warnSeen[m] {
		r.warnSeen[m] = true
		r.warnings = append(r.warnings, m)
	}
}

// Resolve expands every var of in.Self and injects built-ins that are not set.
func Resolve(in Input) Result {
	r := newResolver(in)
	self := r.byNorm[Norm(in.Self)]
	env := map[string]string{}
	if self == nil {
		r.warn("service %q not found in project", in.Self)
		return Result{Env: env, Warnings: r.warnings}
	}
	for _, v := range self.Vars {
		env[v.Key] = r.varValue(self, v.Key)
	}
	if len(self.Ports) > 0 {
		if _, ok := env["PORT"]; !ok {
			env["PORT"] = strconv.Itoa(self.Ports[0].ContainerPort)
		}
	}
	for _, p := range self.Ports {
		if d := self.Domains[p.Name]; d != "" {
			k := "ATOLL_" + Norm(p.Name) + "_DOMAIN"
			if _, ok := env[k]; !ok {
				env[k] = d
			}
			k = "ATOLL_" + Norm(p.Name) + "_URL"
			if _, ok := env[k]; !ok {
				env[k] = "https://" + d
			}
		}
	}
	return Result{Env: env, Warnings: r.warnings}
}

// Expand expands references in an arbitrary string (command/args) in the
// context of in.Self.
func Expand(in Input, s string) (string, []string) {
	r := newResolver(in)
	self := r.byNorm[Norm(in.Self)]
	if self == nil {
		return s, []string{fmt.Sprintf("service %q not found in project", in.Self)}
	}
	return r.expand(self, s), r.warnings
}

func findVar(s *Service, key string) (api.EnvVar, bool) {
	for _, v := range s.Vars {
		if v.Key == key {
			return v, true
		}
	}
	return api.EnvVar{}, false
}

// varValue returns the fully expanded value of var key of service s (which must exist).
func (r *resolver) varValue(s *Service, key string) string {
	id := s.Name + "\x00" + key
	if v, ok := r.cache[id]; ok {
		return v
	}
	v, _ := findVar(s, key)
	if r.visiting[id] {
		r.warn("cycle detected while resolving %s.%s; left unexpanded", s.Name, key)
		return v.Value
	}
	r.visiting[id] = true
	out := r.expand(s, v.Value)
	delete(r.visiting, id)
	r.cache[id] = out
	return out
}

func (r *resolver) expand(ctx *Service, s string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "$${") {
			b.WriteString("${")
			i += 3
			continue
		}
		if strings.HasPrefix(s[i:], "${") {
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				b.WriteString(s[i:])
				break
			}
			name := s[i+2 : i+2+end]
			raw := s[i : i+3+end]
			if val, ok := r.lookup(ctx, name); ok {
				b.WriteString(val)
			} else {
				b.WriteString(raw)
			}
			i += 3 + end
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func (r *resolver) lookup(ctx *Service, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		r.warn("%s: empty reference ${}", ctx.Name)
		return "", false
	}
	if dot := strings.IndexByte(name, '.'); dot > 0 {
		svcName, key := name[:dot], name[dot+1:]
		target := r.byNorm[Norm(svcName)]
		if target == nil {
			r.warn("%s: unknown service %q in ${%s}; left as-is", ctx.Name, svcName, name)
			return "", false
		}
		if target == ctx {
			if _, ok := findVar(ctx, key); ok {
				return r.varValue(ctx, key), true
			}
			if v, ok := r.builtin(ctx, key, true); ok {
				return v, true
			}
		} else {
			if v, ok := findVar(target, key); ok && v.Exposed {
				return r.varValue(target, key), true
			} else if ok {
				r.warn("%s: ${%s} refers to a var that %s does not expose; left as-is", ctx.Name, name, target.Name)
				return "", false
			}
			if v, ok := r.builtin(target, key, true); ok {
				return v, true
			}
		}
		r.warn("%s: unknown reference ${%s}; left as-is", ctx.Name, name)
		return "", false
	}
	if _, ok := findVar(ctx, name); ok {
		return r.varValue(ctx, name), true
	}
	if v, ok := r.builtin(ctx, name, false); ok {
		return v, true
	}
	if strings.HasSuffix(name, "_HOST") {
		if s := r.byNorm[strings.TrimSuffix(name, "_HOST")]; s != nil {
			return s.Name, true
		}
	}
	var found []*Service
	for _, s := range r.order {
		if s == ctx {
			continue
		}
		if v, ok := findVar(s, name); ok && v.Exposed {
			found = append(found, s)
		}
	}
	if len(found) > 0 {
		if len(found) > 1 {
			names := make([]string, len(found))
			for i, s := range found {
				names[i] = s.Name
			}
			r.warn("%s: ${%s} is exposed by several services (%s); using %s — use ${SERVICE.%s} to be explicit",
				ctx.Name, name, strings.Join(names, ", "), found[0].Name, name)
		}
		return r.varValue(found[0], name), true
	}
	r.warn("%s: unknown reference ${%s}; left as-is", ctx.Name, name)
	return "", false
}

// builtin resolves service-scoped built-ins. explicit enables HOST (only
// meaningful as ${SERVICE.HOST}).
func (r *resolver) builtin(s *Service, key string, explicit bool) (string, bool) {
	if explicit && key == "HOST" {
		return s.Name, true
	}
	if key == "PORT" {
		if len(s.Ports) > 0 {
			return strconv.Itoa(s.Ports[0].ContainerPort), true
		}
		return "", false
	}
	for _, p := range s.Ports {
		n := Norm(p.Name)
		switch key {
		case n + "_PORT":
			return strconv.Itoa(p.ContainerPort), true
		case "ATOLL_" + n + "_DOMAIN":
			if d := s.Domains[p.Name]; d != "" {
				return d, true
			}
		case "ATOLL_" + n + "_URL":
			if d := s.Domains[p.Name]; d != "" {
				return "https://" + d, true
			}
		}
	}
	return "", false
}

// References lists what other services can reference from s: exposed vars
// (values expanded) and built-ins. Used by GET /services/{id}/env/references.
func References(in Input) []api.EnvVar {
	r := newResolver(in)
	var out []api.EnvVar
	for _, s := range r.order {
		if Norm(s.Name) == Norm(in.Self) {
			continue
		}
		out = append(out, api.EnvVar{Key: Norm(s.Name) + "_HOST", Value: s.Name, Exposed: true, Source: s.Name})
		for _, v := range s.Vars {
			if !v.Exposed {
				continue
			}
			val := ""
			if !v.Secret {
				val = r.varValue(s, v.Key)
			}
			out = append(out, api.EnvVar{Key: v.Key, Value: val, Secret: v.Secret, Exposed: true, Source: s.Name})
		}
	}
	return out
}
