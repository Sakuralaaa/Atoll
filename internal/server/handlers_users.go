package server

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/server/store"
	"golang.org/x/crypto/bcrypt"
)

// Admin-only account management for sub-accounts (notes/subaccounts.md). Panel sessions
// only (lvlSession); checkMember refuses members because these routes are not in
// memberAllow, and requireAdmin checks again.

type userView struct {
	ID                 int64    `json:"id"`
	Username           string   `json:"username"`
	Role               string   `json:"role"`
	Disabled           bool     `json:"disabled,omitempty"`
	Permissions        []string `json:"permissions,omitempty"`
	Servers            []string `json:"servers,omitempty"`
	MustChangePassword bool     `json:"must_change_password,omitempty"`
	CreatedAt          string   `json:"created_at"`
}

type userRequest struct {
	Username    string    `json:"username"`
	Password    *string   `json:"password"`
	Permissions *[]string `json:"permissions"`
	Servers     *[]string `json:"servers"`
	Disabled    *bool     `json:"disabled"`
}

func requireAdmin(r *http.Request) (*principal, error) {
	p := principalFrom(r.Context())
	if p == nil || !p.Session || p.Member {
		return nil, errf(403, "admin_only", "only the panel admin can manage accounts")
	}
	return p, nil
}

func (s *Server) userView(r *http.Request, u store.User) (userView, error) {
	v := userView{ID: u.ID, Username: u.Username, Role: u.Role, Disabled: u.Disabled, MustChangePassword: u.MustChangePassword, CreatedAt: store.TS(u.CreatedAt)}
	if u.IsMember() {
		v.Permissions = u.Permissions
		srv, err := s.st.AssignedServers(r.Context(), u.ID)
		if err != nil {
			return v, err
		}
		v.Servers = srv
	}
	return v, nil
}

// cleanMemberPerms validates a member permission list; read is always included.
func cleanMemberPerms(in []string) ([]string, error) {
	out := []string{"read"}
	for _, x := range in {
		x = strings.TrimSpace(x)
		if x == "" || slices.Contains(out, x) {
			continue
		}
		if !memberPermAllowed(x) {
			return nil, badRequest("permission %q cannot be given to a member account", x)
		}
		out = append(out, x)
	}
	return out, nil
}

func (s *Server) cleanServers(r *http.Request, in []string) ([]string, error) {
	out := []string{}
	for _, id := range in {
		if id = strings.TrimSpace(id); id == "" || slices.Contains(out, id) {
			continue
		}
		srv, err := s.st.GetNode(r.Context(), id)
		if err != nil {
			return nil, badRequest("unknown server %q", id)
		}
		if srv.Role == "control" {
			return nil, badRequest("the control server cannot be assigned to a member")
		}
		out = append(out, id)
	}
	return out, nil
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) error {
	if _, err := requireAdmin(r); err != nil {
		return err
	}
	us, err := s.st.ListUsers(r.Context())
	if err != nil {
		return err
	}
	out := []userView{}
	for _, u := range us {
		v, err := s.userView(r, u)
		if err != nil {
			return err
		}
		out = append(out, v)
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) error {
	p, err := requireAdmin(r)
	if err != nil {
		return err
	}
	var req userRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	req.Username = strings.TrimSpace(req.Username)
	if !usernameRe.MatchString(req.Username) {
		return badRequest("username must be 1-64 characters: letters, digits, _ . @ -")
	}
	if req.Password == nil {
		return badRequest("password is required")
	}
	if err := validPassword(*req.Password); err != nil {
		return err
	}
	var in []string
	if req.Permissions != nil {
		in = *req.Permissions
	} else {
		in = []string{"deploy"}
	}
	perms, err := cleanMemberPerms(in)
	if err != nil {
		return err
	}
	var srvIn []string
	if req.Servers != nil {
		srvIn = *req.Servers
	}
	servers, err := s.cleanServers(r, srvIn)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u, err := s.st.CreateMember(r.Context(), req.Username, string(hash), perms, servers, true)
	if err == store.ErrConflict {
		return errf(409, "conflict", "username %q is taken", req.Username)
	}
	if err != nil {
		return err
	}
	s.st.Audit(r.Context(), p.actor(), "user.create", u.Username, map[string]string{"permissions": strings.Join(perms, ","), "servers": strings.Join(servers, ",")})
	v, err := s.userView(r, *u)
	if err != nil {
		return err
	}
	writeJSON(w, 201, v)
	return nil
}

func (s *Server) memberFromPath(r *http.Request) (*store.User, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, errf(404, "not_found", "no such account")
	}
	u, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if !u.IsMember() {
		return nil, errf(409, "admin_account", "admin accounts are changed from their own session")
	}
	return u, nil
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) error {
	p, err := requireAdmin(r)
	if err != nil {
		return err
	}
	u, err := s.memberFromPath(r)
	if err != nil {
		return err
	}
	var req userRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	var up store.MemberUpdate
	meta := map[string]string{}
	if req.Permissions != nil {
		perms, err := cleanMemberPerms(*req.Permissions)
		if err != nil {
			return err
		}
		up.Permissions = &perms
		meta["permissions"] = strings.Join(perms, ",")
	}
	if req.Servers != nil {
		servers, err := s.cleanServers(r, *req.Servers)
		if err != nil {
			return err
		}
		up.Servers = &servers
		meta["servers"] = strings.Join(servers, ",")
	}
	if req.Disabled != nil {
		up.Disabled = req.Disabled
		meta["disabled"] = strconv.FormatBool(*req.Disabled)
	}
	if req.Password != nil {
		if err := validPassword(*req.Password); err != nil {
			return err
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		h, yes := string(hash), true
		up.PasswordHash, up.MustChange = &h, &yes
		meta["password"] = "reset"
	}
	if err := s.st.UpdateMember(r.Context(), u.ID, up); err != nil {
		return err
	}
	s.st.Audit(r.Context(), p.actor(), "user.update", u.Username, meta)
	u, err = s.st.UserByID(r.Context(), u.ID)
	if err != nil {
		return err
	}
	v, err := s.userView(r, *u)
	if err != nil {
		return err
	}
	writeJSON(w, 200, v)
	return nil
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) error {
	p, err := requireAdmin(r)
	if err != nil {
		return err
	}
	u, err := s.memberFromPath(r)
	if err != nil {
		return err
	}
	if err := s.st.DeleteMember(r.Context(), u.ID); err != nil {
		return err
	}
	s.st.Audit(r.Context(), p.actor(), "user.delete", u.Username, nil)
	w.WriteHeader(204)
	return nil
}
