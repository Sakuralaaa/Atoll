package server

import (
	"context"
	"slices"
	"strings"
)

// Sub-accounts B7 (notes/subaccounts.md): a member puts hostnames under a configured root
// domain only when an admin allowed it that root (store.member_root_domains), and then only
// strict subdomains: never the root itself and never a wildcard. A hostname outside every
// configured root is the member's own domain and stays allowed (it waits in pending until
// the member points an A record at the server). Admins are not limited; domains bound before
// the upgrade are left as they are.

// memberRootsOf returns the roots a member principal may use, or nil for anyone else.
func (s *Server) memberRootsOf(ctx context.Context, p *principal) ([]string, error) {
	if p == nil || !p.Member || p.User == nil {
		return nil, nil
	}
	return s.st.MemberRootDomains(ctx, p.User.ID)
}

// checkMemberHost refuses a normalized hostname the calling member may not bind.
func (s *Server) checkMemberHost(ctx context.Context, host string) error {
	p := principalFrom(ctx)
	if p == nil || !p.Member {
		return nil
	}
	if strings.Contains(host, "*") {
		return errf(403, "member_domain_wildcard", "sub-accounts cannot bind wildcard hostnames")
	}
	rs, err := s.st.ListRootDomains(ctx)
	if err != nil {
		return err
	}
	root := ""
	for _, r := range rs {
		if (host == r.Name || strings.HasSuffix(host, "."+r.Name)) && len(r.Name) > len(root) {
			root = r.Name
		}
	}
	if root == "" {
		return nil // the member's own domain: it adds the DNS record itself
	}
	allowed, err := s.memberRootsOf(ctx, p)
	if err != nil {
		return err
	}
	if !slices.Contains(allowed, root) {
		return errf(403, "member_root_denied", "this account may not use the root domain %s; ask an admin to allow it", root)
	}
	if host == root {
		return errf(403, "member_domain_apex", "sub-accounts can only bind subdomains of %s, not %s itself", root, root)
	}
	return nil
}

// checkMemberRoot refuses a root domain (a project default or an explicit root_domain)
// the calling member was not given.
func (s *Server) checkMemberRoot(ctx context.Context, root string) error {
	p := principalFrom(ctx)
	if root == "" || p == nil || !p.Member {
		return nil
	}
	allowed, err := s.memberRootsOf(ctx, p)
	if err != nil {
		return err
	}
	if !slices.Contains(allowed, root) {
		return errf(403, "member_root_denied", "this account may not use the root domain %s; ask an admin to allow it", root)
	}
	return nil
}

// cleanMemberRoots validates an admin's list of roots for a member.
func (s *Server) cleanMemberRoots(ctx context.Context, in []string) ([]string, error) {
	out := []string{}
	for _, n := range in {
		n = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), ".")
		if n == "" || slices.Contains(out, n) {
			continue
		}
		if _, err := s.st.GetRootDomain(ctx, n); err != nil {
			return nil, badRequest("unknown root domain %q", n)
		}
		out = append(out, n)
	}
	return out, nil
}

// checkMemberTemplateHosts applies checkMemberHost to the hostnames of a one-click app
// (POST /app-templates/{id}) before its project is created, completing a bare name the
// way prepareDomain will (a new project defaults to the default root).
func (s *Server) checkMemberTemplateHosts(ctx context.Context, hostname, root string, extra map[string]string) error {
	if p := principalFrom(ctx); p == nil || !p.Member {
		return nil
	}
	check := func(h, root string) error {
		host, err := s.domainHost(ctx, s.projectRoot(ctx, ""), h, root)
		if err != nil {
			return err
		}
		return s.checkMemberHost(ctx, host)
	}
	if hostname != "" {
		if err := check(hostname, root); err != nil {
			return err
		}
	}
	for _, h := range extra {
		r := root
		if strings.Contains(h, ".") {
			r = ""
		}
		if err := check(h, r); err != nil {
			return err
		}
	}
	return nil
}
