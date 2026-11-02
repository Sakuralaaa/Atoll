package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/client"
)

const permHelp = "Permissions (comma-separated or repeated):\n" +
	"  read      every GET: projects, services, env (masked), logs, metrics (always included)\n" +
	"  deploy    create/edit projects and services, env, start/stop/restart/redeploy, rollback, backups\n" +
	"  exec      run commands inside service containers (atoll exec; reads their secrets)\n" +
	"  delete    delete services and projects (with their volumes)\n" +
	"  migrate   move projects/services between servers, Zeabur cutover/rollback/cleanup\n" +
	"  domains   bind and remove domains (changes DNS)\n" +
	"  restore   restore backups (replaces live data)\n" +
	"  servers   join tokens, rename/remove servers, server network actions\n" +
	"  settings  panel settings (Cloudflare, TLS, backup storage)\n" +
	"  tokens    list/create/change/delete API tokens (never more than the token itself holds)\n" +
	"  all       every permission\n" +
	"A token without a permission gets 403 missing_permission; with it the operation runs\n" +
	"directly, unless the token was created with --require-approval (destructive operations then\n" +
	"wait for approval in the panel). servers, settings and tokens cannot be combined with --project."

// ParsePerms parses permission lists like ["read,deploy", "migrate"] (or "all") into a sorted,
// de-duplicated list in display order. Unknown names are an error.
func ParsePerms(vals []string) ([]string, error) {
	have := map[string]bool{}
	for _, v := range vals {
		for _, p := range strings.Split(v, ",") {
			p = strings.ToLower(strings.TrimSpace(p))
			if p == "" {
				continue
			}
			if p == "all" || p == "admin" {
				for _, x := range api.AllPermissions {
					have[x] = true
				}
				continue
			}
			if !slices.Contains(api.AllPermissions, p) {
				return nil, fmt.Errorf("unknown permission %q (want one of %s, or all)", p, strings.Join(api.AllPermissions, ", "))
			}
			have[p] = true
		}
	}
	var out []string
	for _, x := range api.AllPermissions {
		if have[x] {
			out = append(out, x)
		}
	}
	return out, nil
}

// resolveProjectIDs turns project names or ids into ids.
func resolveProjectIDs(ctx context.Context, c *client.Client, refs []string) ([]string, error) {
	var ids []string
	for _, ref := range refs {
		for _, r := range strings.Split(ref, ",") {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			p, err := c.ResolveProject(ctx, r)
			if err != nil {
				return nil, err
			}
			if !slices.Contains(ids, p.ID) {
				ids = append(ids, p.ID)
			}
		}
	}
	return ids, nil
}

// resolveToken finds a token by id, name or prefix.
func resolveToken(ctx context.Context, c *client.Client, ref string) (*api.APIToken, error) {
	all, err := c.ListTokens(ctx)
	if err != nil {
		return nil, err
	}
	var hits []int
	for i, t := range all {
		if t.ID == ref || t.Prefix == ref {
			return &all[i], nil
		}
		if t.Name == ref {
			hits = append(hits, i)
		}
	}
	switch len(hits) {
	case 0:
		return nil, fmt.Errorf("token %q not found (see `atoll token ls`)", ref)
	case 1:
		return &all[hits[0]], nil
	}
	return nil, fmt.Errorf("token name %q is ambiguous; use its id", ref)
}

func tokenProjects(t api.APIToken) string {
	if len(t.Projects) == 0 {
		return "all"
	}
	return strings.Join(t.Projects, ",")
}

func newTokenCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "token",
		Aliases: []string{"tokens"},
		Short:   "List, create, change and remove API tokens (needs the tokens permission)",
		Long: "API tokens authenticate the CLI, MCP and CI. Managing tokens needs the tokens permission,\n" +
			"and a token can never grant more than it holds itself (403 escalation).\n\n" + permHelp,
	}
	cmd.AddCommand(&cobra.Command{
		Use: "ls", Aliases: []string{"list"}, Short: "List API tokens", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			list, err := c.ListTokens(ctxOf(cmd))
			if err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(list)
			}
			tw := app.table()
			fmt.Fprintln(tw, "ID\tNAME\tPREFIX\tPERMISSIONS\tPROJECTS\tAPPROVAL\tLAST USED")
			for _, t := range list {
				appr := "-"
				if t.RequireApproval {
					appr = "required"
				}
				last := "never"
				if t.LastUsedAt != nil {
					last = ago(*t.LastUsedAt)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Name, t.Prefix, strings.Join(t.Permissions, ","), tokenProjects(t), appr, last)
			}
			return tw.Flush()
		},
	})
	cmd.AddCommand(newTokenCreateCmd(app), newTokenEditCmd(app))
	cmd.AddCommand(&cobra.Command{
		Use: "rm TOKEN", Aliases: []string{"delete", "revoke"}, Short: "Delete (revoke) a token by id, name or prefix", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			t, err := resolveToken(ctx, c, args[0])
			if err != nil {
				return err
			}
			if _, err := app.handle(ctx, c, c.DeleteToken(ctx, t.ID)); err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(map[string]string{"deleted": t.ID})
			}
			fmt.Fprintf(app.Out, "Deleted token %s (%s)\n", t.Name, t.ID)
			return nil
		},
	})
	return cmd
}

// TokenCreateFlags are the parsed flags of `atoll token create`.
type TokenCreateFlags struct {
	Perms           []string
	Projects        []string
	RequireApproval bool
}

// BuildTokenRequest validates create flags into a TokenRequest (projects already resolved to ids).
func BuildTokenRequest(name string, f TokenCreateFlags) (api.TokenRequest, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return api.TokenRequest{}, errors.New("token name required")
	}
	perms, err := ParsePerms(f.Perms)
	if err != nil {
		return api.TokenRequest{}, err
	}
	if len(perms) == 0 {
		perms = []string{api.PermRead}
	}
	if !slices.Contains(perms, api.PermRead) {
		perms = append([]string{api.PermRead}, perms...)
	}
	if len(f.Projects) > 0 {
		for _, g := range api.GlobalPermissions {
			if slices.Contains(perms, g) {
				return api.TokenRequest{}, fmt.Errorf("permission %q cannot be combined with --project (it is not about one project)", g)
			}
		}
	}
	return api.TokenRequest{Name: name, Permissions: perms, Projects: f.Projects, RequireApproval: f.RequireApproval}, nil
}

func newTokenCreateCmd(app *App) *cobra.Command {
	var f TokenCreateFlags
	cmd := &cobra.Command{
		Use:   "create NAME [--perm read,deploy,...] [--project PROJECT]... [--require-approval]",
		Short: "Create an API token (the secret is printed once)",
		Long:  "Creates a token. Without --perm it gets read only.\n\n" + permHelp,
		Example: "  atoll token create ci --perm deploy --project shop\n" +
			"  atoll token create agent --perm deploy,migrate,domains --require-approval",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			if _, err := ParsePerms(f.Perms); err != nil {
				return err
			}
			ctx := ctxOf(cmd)
			ids, err := resolveProjectIDs(ctx, c, f.Projects)
			if err != nil {
				return err
			}
			ff := f
			ff.Projects = ids
			req, err := BuildTokenRequest(args[0], ff)
			if err != nil {
				return err
			}
			t, err := c.CreateToken(ctx, req)
			if _, err := app.handle(ctx, c, err); err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(t)
			}
			fmt.Fprintf(app.Out, "Created token %s (%s)\n  permissions: %s\n  projects:    %s\n", t.Name, t.ID, strings.Join(t.Permissions, ","), tokenProjects(*t))
			if t.RequireApproval {
				fmt.Fprintln(app.Out, "  destructive operations wait for approval in the panel")
			}
			if t.Token != "" {
				fmt.Fprintf(app.Out, "\n%s\n\nCopy it now: it is not shown again.\n", t.Token)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringSliceVar(&f.Perms, "perm", nil, "permissions, e.g. read,deploy,migrate (repeatable; 'all' for every one)")
	fl.StringArrayVar(&f.Projects, "project", nil, "limit the token to this project (name or id; repeatable)")
	fl.BoolVar(&f.RequireApproval, "require-approval", false, "destructive operations wait for approval in the panel instead of running directly")
	return cmd
}

// TokenEditFlags are the parsed flags of `atoll token edit`; the *Set fields say which were given.
type TokenEditFlags struct {
	Name            string
	NameSet         bool
	Perms           []string
	PermsSet        bool
	AddPerms        []string
	RemovePerms     []string
	Projects        []string // resolved ids
	ProjectsSet     bool
	AllProjects     bool
	RequireApproval bool
	ApprovalSet     bool
}

// BuildTokenUpdate turns edit flags into a PATCH body, applying --add-perm/--rm-perm to cur.
func BuildTokenUpdate(cur api.APIToken, f TokenEditFlags) (api.TokenUpdate, error) {
	var u api.TokenUpdate
	if f.NameSet {
		n := strings.TrimSpace(f.Name)
		if n == "" {
			return u, errors.New("--name must not be empty")
		}
		u.Name = &n
	}
	if f.PermsSet || len(f.AddPerms) > 0 || len(f.RemovePerms) > 0 {
		base := cur.Permissions
		if f.PermsSet {
			p, err := ParsePerms(f.Perms)
			if err != nil {
				return u, err
			}
			base = p
		}
		add, err := ParsePerms(f.AddPerms)
		if err != nil {
			return u, err
		}
		rm, err := ParsePerms(f.RemovePerms)
		if err != nil {
			return u, err
		}
		if slices.Contains(rm, api.PermRead) {
			return u, errors.New("read cannot be removed (every token can read)")
		}
		merged, _ := ParsePerms(append(append([]string{}, base...), add...))
		merged = slices.DeleteFunc(merged, func(x string) bool { return slices.Contains(rm, x) })
		if !slices.Contains(merged, api.PermRead) {
			merged = append([]string{api.PermRead}, merged...)
		}
		u.Permissions = &merged
	}
	if f.AllProjects && f.ProjectsSet {
		return u, errors.New("--project and --all-projects are mutually exclusive")
	}
	if f.AllProjects {
		empty := []string{}
		u.Projects = &empty
	} else if f.ProjectsSet {
		p := append([]string{}, f.Projects...)
		u.Projects = &p
	}
	if f.ApprovalSet {
		v := f.RequireApproval
		u.RequireApproval = &v
	}
	if u.Name == nil && u.Permissions == nil && u.Projects == nil && u.RequireApproval == nil {
		return u, errors.New("nothing to change: give --name, --perm, --add-perm, --rm-perm, --project, --all-projects or --require-approval[=false]")
	}
	return u, nil
}

func newTokenEditCmd(app *App) *cobra.Command {
	var f TokenEditFlags
	var projects []string
	cmd := &cobra.Command{
		Use:   "edit TOKEN [--name N] [--perm ...|--add-perm ...|--rm-perm ...] [--project P...|--all-projects] [--require-approval[=false]]",
		Short: "Change a token's name, permissions, projects or approval requirement",
		Long:  "TOKEN is an id, name or prefix. --perm replaces the permission set; --add-perm/--rm-perm change it.\n\n" + permHelp,
		Example: "  atoll token edit ci --add-perm migrate\n" +
			"  atoll token edit tok_abc --all-projects --require-approval=false",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := app.client()
			if err != nil {
				return err
			}
			fl := cmd.Flags()
			f.NameSet = fl.Changed("name")
			f.PermsSet = fl.Changed("perm")
			f.ProjectsSet = fl.Changed("project")
			f.ApprovalSet = fl.Changed("require-approval")
			ctx := ctxOf(cmd)
			t, err := resolveToken(ctx, c, args[0])
			if err != nil {
				return err
			}
			if f.ProjectsSet {
				if f.Projects, err = resolveProjectIDs(ctx, c, projects); err != nil {
					return err
				}
			}
			upd, err := BuildTokenUpdate(*t, f)
			if err != nil {
				return err
			}
			nt, err := c.UpdateToken(ctx, t.ID, upd)
			if _, err := app.handle(ctx, c, err); err != nil {
				return err
			}
			if app.JSON {
				return app.printJSON(nt)
			}
			fmt.Fprintf(app.Out, "Updated token %s (%s)\n  permissions: %s\n  projects:    %s\n  approval:    %v\n", nt.Name, nt.ID, strings.Join(nt.Permissions, ","), tokenProjects(*nt), nt.RequireApproval)
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.Name, "name", "", "new name")
	fl.StringSliceVar(&f.Perms, "perm", nil, "replace permissions, e.g. read,deploy")
	fl.StringSliceVar(&f.AddPerms, "add-perm", nil, "add permissions")
	fl.StringSliceVar(&f.RemovePerms, "rm-perm", nil, "remove permissions")
	fl.StringArrayVar(&projects, "project", nil, "limit to these projects (name or id; repeatable; replaces the list)")
	fl.BoolVar(&f.AllProjects, "all-projects", false, "remove the project limit")
	fl.BoolVar(&f.RequireApproval, "require-approval", false, "destructive operations wait for approval in the panel (--require-approval=false to turn off)")
	return cmd
}

// ExplainError rewrites token permission errors into actionable messages; other errors pass through.
func ExplainError(err error) error {
	if err == nil {
		return nil
	}
	var ae *client.APIError
	if !errors.As(err, &ae) {
		return err
	}
	switch ae.Code {
	case client.CodeMissingPermission:
		perm, _ := client.MissingPermission(err)
		if perm == "" {
			return fmt.Errorf("permission denied: %s", ae.Message)
		}
		return fmt.Errorf("permission denied: this API token lacks the %q permission needed for this action. "+
			"Grant it in the panel (CLI & API → tokens), or with `atoll token edit TOKEN --add-perm %s` using a token that has the tokens permission and holds %s itself", perm, perm, perm)
	case client.CodeEscalation:
		return fmt.Errorf("not allowed: a token can only hand out permissions and projects it holds itself (and a token with require_approval only tokens that also require approval): %s", ae.Message)
	case client.CodeProjectScope:
		return fmt.Errorf("not allowed: %s (the token is limited to specific projects; see `atoll token ls`)", ae.Message)
	case client.CodeSessionRequired:
		return fmt.Errorf("not allowed with an API token: %s", ae.Message)
	}
	return err
}
