package server

import (
	"context"
	"reflect"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/envresolve"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// Binding or unbinding a domain is a route change: the agent gets a new route table for its
// Caddy and the service's container is left alone. The container is only redeployed when
// the change reaches something it was explicitly configured with: one of its own env vars
// (or a sibling's, via ${SERVICE.ATOLL_<PORT>_URL}) or its command/args referencing
// ${ATOLL_<PORT>_DOMAIN} / ${ATOLL_<PORT>_URL}. The built-ins injected into every env
// without being referenced are refreshed at the service's next deploy instead: redeploying
// (and so restarting) a service for a variable it never asked for is what made every domain
// change a full redeploy.

// redeployForDomainChange redeploys the deployed, running services of a project whose spec
// changes, apart from implicitly injected domain built-ins, when the project's domains go
// from before to after.
func (s *Server) redeployForDomainChange(ctx context.Context, projectID string, before, after []store.DomainRow) {
	svcs, err := s.st.ListServices(ctx, projectID)
	if err != nil {
		s.log.Warn("domain: list services for env check", "project", projectID, "err", err)
		return
	}
	for i := range svcs {
		svc := &svcs[i]
		if _, err := s.st.LatestDeployment(ctx, svc.ID); err != nil {
			continue // never deployed: its first deploy resolves the current domains
		}
		if _, stopped, err := s.st.GetService(ctx, svc.ID); err != nil || stopped {
			continue // a deploy would start it; the next start/deploy picks the value up
		}
		changed, err := s.domainChangeAffects(ctx, svc, before, after)
		if err != nil {
			s.log.Warn("domain: env check", "service", svc.ID, "err", err)
			continue
		}
		if changed {
			if _, err := s.deploy(ctx, svc.ID, "env"); err != nil {
				s.log.Warn("domain: env redeploy", "service", svc.ID, "err", err)
			}
		}
	}
}

// domainChangeAffects reports whether svc's resolved spec differs between the two domain
// sets once implicitly injected domain built-ins are left out.
func (s *Server) domainChangeAffects(ctx context.Context, svc *api.Service, before, after []store.DomainRow) (bool, error) {
	vars, err := s.st.ListEnv(ctx, svc.ID)
	if err != nil {
		return false, err
	}
	a, _, err := s.buildSpecWith(ctx, svc, "", before)
	if err != nil {
		return false, err
	}
	b, _, err := s.buildSpecWith(ctx, svc, "", after)
	if err != nil {
		return false, err
	}
	stripInjectedDomainEnv(&a, svc, vars)
	stripInjectedDomainEnv(&b, svc, vars)
	return !reflect.DeepEqual(a, b), nil
}

// stripInjectedDomainEnv drops the ATOLL_<PORT>_DOMAIN/URL keys the resolver injected on
// its own (keys the service does not define as its own vars).
func stripInjectedDomainEnv(spec *api.ResolvedSpec, svc *api.Service, vars []api.EnvVar) {
	own := map[string]bool{}
	for _, v := range vars {
		own[v.Key] = true
	}
	for _, p := range svc.Ports {
		n := envresolve.Norm(p.Name)
		for _, k := range []string{"ATOLL_" + n + "_DOMAIN", "ATOLL_" + n + "_URL"} {
			if !own[k] {
				delete(spec.Env, k)
			}
		}
	}
}

// withoutDomain returns doms minus the domain with the given id.
func withoutDomain(doms []store.DomainRow, id string) []store.DomainRow {
	out := make([]store.DomainRow, 0, len(doms))
	for _, d := range doms {
		if d.ID != id {
			out = append(out, d)
		}
	}
	return out
}
