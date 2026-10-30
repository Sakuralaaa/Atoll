package client

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ResolveServer finds a server by id or name.
func (c *Client) ResolveServer(ctx context.Context, ref string) (*api.Server, error) {
	if strings.HasPrefix(ref, "srv_") {
		s, err := c.GetServer(ctx, ref)
		if err == nil {
			return s, nil
		}
		if !IsNotFound(err) {
			return nil, err
		}
	}
	all, err := c.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	i, err := pick(len(all), ref, "server", func(i int) (string, string) { return all[i].ID, all[i].Name })
	if err != nil {
		return nil, err
	}
	return &all[i], nil
}

// ResolveProject finds a project by id or name and returns it with its services.
func (c *Client) ResolveProject(ctx context.Context, ref string) (*api.Project, error) {
	if strings.HasPrefix(ref, "prj_") {
		p, err := c.GetProject(ctx, ref)
		if err == nil {
			return p, nil
		}
		if !IsNotFound(err) {
			return nil, err
		}
	}
	all, err := c.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	i, err := pick(len(all), ref, "project", func(i int) (string, string) { return all[i].ID, all[i].Name })
	if err != nil {
		return nil, err
	}
	return c.GetProject(ctx, all[i].ID)
}

// ResolveService finds a service by id or name. projectRef (id or name) narrows the
// search; without it every project is searched and the name must be unique.
func (c *Client) ResolveService(ctx context.Context, projectRef, ref string) (*api.Service, error) {
	if strings.HasPrefix(ref, "svc_") {
		s, err := c.GetService(ctx, ref)
		if err == nil {
			return s, nil
		}
		if !IsNotFound(err) {
			return nil, err
		}
	}
	var candidates []api.Service
	if projectRef != "" {
		p, err := c.ResolveProject(ctx, projectRef)
		if err != nil {
			return nil, err
		}
		candidates = p.Services
		if candidates == nil {
			if candidates, err = c.ListServices(ctx, p.ID); err != nil {
				return nil, err
			}
		}
	} else {
		projects, err := c.ListProjects(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range projects {
			svcs, err := c.ListServices(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, svcs...)
		}
	}
	i, err := pick(len(candidates), ref, "service", func(i int) (string, string) { return candidates[i].ID, candidates[i].Name })
	if err != nil {
		if projectRef == "" && strings.Contains(err.Error(), "ambiguous") {
			return nil, fmt.Errorf("%w; pass -p PROJECT", err)
		}
		return nil, err
	}
	return &candidates[i], nil
}

// pick chooses one item by exact id, exact name, then case-insensitive name.
func pick(n int, ref, kind string, get func(int) (id, name string)) (int, error) {
	for i := 0; i < n; i++ {
		if id, _ := get(i); id == ref {
			return i, nil
		}
	}
	var exact, fold []int
	for i := 0; i < n; i++ {
		_, name := get(i)
		if name == ref {
			exact = append(exact, i)
		} else if strings.EqualFold(name, ref) {
			fold = append(fold, i)
		}
	}
	for _, m := range [][]int{exact, fold} {
		if len(m) == 1 {
			return m[0], nil
		}
		if len(m) > 1 {
			ids := make([]string, len(m))
			for j, i := range m {
				ids[j], _ = get(i)
			}
			sort.Strings(ids)
			return -1, fmt.Errorf("%s name %q is ambiguous (%s); use the id", kind, ref, strings.Join(ids, ", "))
		}
	}
	return -1, fmt.Errorf("%s %q not found", kind, ref)
}

// PreviousDeployment picks the deployment to roll back to: the newest deployment that
// ran successfully before the current one.
func PreviousDeployment(deps []api.Deployment) (*api.Deployment, error) {
	if len(deps) == 0 {
		return nil, fmt.Errorf("no deployments")
	}
	sorted := append([]api.Deployment(nil), deps...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartedAt.After(sorted[j].StartedAt) })
	current := -1
	for i, d := range sorted {
		if d.Status == "running" {
			current = i
			break
		}
	}
	if current < 0 {
		current = 0
	}
	for _, d := range sorted[current+1:] {
		if d.Status == "superseded" || d.Status == "running" {
			d := d
			return &d, nil
		}
	}
	return nil, fmt.Errorf("no earlier successful deployment to roll back to")
}
