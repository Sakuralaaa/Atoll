package server

import (
	"net/http"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/envresolve"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

func (s *Server) handleGetEnv(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	vars, err := s.st.ListEnv(r.Context(), svc.ID)
	if err != nil {
		return err
	}
	if r.URL.Query().Get("reveal") == "1" {
		p := principalFrom(r.Context())
		if !p.Session {
			return errf(403, "session_required", "revealing secrets is only allowed from the panel")
		}
		s.st.Audit(r.Context(), p.actor(), "env.reveal", svc.ID, nil)
		writeJSON(w, 200, vars)
		return nil
	}
	writeJSON(w, 200, maskEnv(vars))
	return nil
}

func (s *Server) handlePutEnv(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var vars []api.EnvVar
	if err := decode(r, &vars); err != nil {
		return err
	}
	if vars == nil {
		vars = []api.EnvVar{}
	}
	if err := validateEnv(vars); err != nil {
		return err
	}
	old, err := s.st.ListEnv(r.Context(), svc.ID)
	if err != nil {
		return err
	}
	oldBy := map[string]api.EnvVar{}
	for _, e := range old {
		oldBy[e.Key] = e
	}
	for i, e := range vars {
		vars[i].Source = ""
		if e.Secret && e.Value == "" {
			if o, ok := oldBy[e.Key]; ok {
				vars[i].Value = o.Value // "" keeps the stored secret
			}
		}
	}
	if err := s.st.ReplaceEnv(r.Context(), svc.ID, vars); err != nil {
		return err
	}
	keys := []string{}
	for _, e := range vars {
		keys = append(keys, e.Key)
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "env.update", svc.ID, keys)
	// Redeploy only services that were deployed before, unless the caller opts out (?redeploy=0)
	// to stage several edits and deploy once.
	if r.URL.Query().Get("redeploy") == "0" {
		writeJSON(w, 200, maskEnv(vars))
		return nil
	}
	if _, err := s.st.LatestDeployment(r.Context(), svc.ID); err == nil {
		if _, err := s.deploy(r.Context(), svc.ID, "env"); err != nil {
			return err
		}
	} else if err != store.ErrNotFound {
		return err
	}
	writeJSON(w, 200, maskEnv(vars))
	return nil
}

func (s *Server) handleEnvReferences(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	svcs, err := s.st.ListServices(r.Context(), svc.ProjectID)
	if err != nil {
		return err
	}
	in := envresolve.Input{Self: svc.Name}
	for _, o := range svcs {
		vars, err := s.st.ListEnv(r.Context(), o.ID)
		if err != nil {
			return err
		}
		in.Services = append(in.Services, envresolve.Service{Name: o.Name, Vars: vars, Ports: o.Ports})
	}
	refs := envresolve.References(in)
	if refs == nil {
		refs = []api.EnvVar{}
	}
	writeJSON(w, 200, refs)
	return nil
}
