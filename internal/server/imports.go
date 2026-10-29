package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/atoll-sh/atoll/internal/api"
)

// Import job kinds.
const (
	importKindPlan   = "plan"
	importKindZeabur = "zeabur-local"
)

// planMeta is the state of a CLI-uploaded import plan.
type planMeta struct {
	ServiceIDs map[string]string `json:"service_ids"` // plan service name -> atoll service id
}

func maskJob(j *api.ImportJob) *api.ImportJob {
	c := *j
	c.Plan.Services = make([]api.ImportService, len(j.Plan.Services))
	for i, sv := range j.Plan.Services {
		sv.Env = maskEnv(sv.Env)
		c.Plan.Services[i] = sv
	}
	if c.Steps == nil {
		c.Steps = []api.ImportStep{}
	}
	return &c
}

func (s *Server) handleListImports(w http.ResponseWriter, r *http.Request) error {
	js, err := s.st.ListImports(r.Context())
	if err != nil {
		return err
	}
	out := make([]*api.ImportJob, len(js))
	for i := range js {
		out[i] = maskJob(&js[i])
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) handleGetImport(w http.ResponseWriter, r *http.Request) error {
	j, _, err := s.st.GetImport(r.Context(), r.PathValue("id"), nil)
	if err != nil {
		return err
	}
	writeJSON(w, 200, maskJob(j))
	return nil
}

func (s *Server) saveImport(ctx context.Context, j *api.ImportJob, kind string, meta any) {
	if err := s.st.SaveImport(ctx, *j, kind, meta); err != nil {
		s.log.Error("save import", "err", err)
	}
	s.publish("import", maskJob(j))
}

// handleCreateImport creates project, services, env and domains from a plan (nothing starts).
func (s *Server) handleCreateImport(w http.ResponseWriter, r *http.Request) error {
	var plan api.ImportPlan
	if err := decode(r, &plan); err != nil {
		return err
	}
	if len(plan.Services) == 0 {
		return badRequest("plan has no services")
	}
	ctx := r.Context()
	if plan.ProjectName == "" {
		plan.ProjectName = "import-" + plan.SourceProject
	}
	// validate everything before creating anything
	names := map[string]bool{}
	specs := make([]api.Service, len(plan.Services))
	for i, is := range plan.Services {
		v := api.Service{Name: sanitizeName(is.Name), Kind: is.Kind, Image: is.Image, Command: is.Command, Ports: is.Ports, Volumes: is.Volumes, Configs: is.Configs}
		if v.Kind == "" {
			v.Kind = "image"
		}
		if names[v.Name] {
			return badRequest("duplicate service name %q in plan", v.Name)
		}
		names[v.Name] = true
		if err := validateServiceSpec(&v); err != nil {
			return badRequest("service %s: %v", is.Name, err)
		}
		if err := validateEnv(is.Env); err != nil {
			return badRequest("service %s: %v", is.Name, err)
		}
		specs[i] = v
	}
	p, err := s.createProject(ctx, plan.ProjectName, plan.TargetServer, "Imported from "+plan.Source+" project "+plan.SourceProject, false)
	if err != nil {
		return err
	}
	plan.TargetServer = p.ServerID
	job := &api.ImportJob{ID: newID("imp"), Plan: plan, ProjectID: p.ID, Status: "planned", CreatedAt: time.Now().UTC()}
	meta := planMeta{ServiceIDs: map[string]string{}}
	var created []string
	for i, is := range plan.Services {
		svc, err := s.createService(ctx, p, specs[i], is.Env, false)
		if err != nil {
			s.execDeleteProject(ctx, p.ID, "system")
			return err
		}
		meta.ServiceIDs[is.Name] = svc.ID
		created = append(created, svc.Name)
		for _, h := range is.Domains {
			if err := s.createPendingDomain(ctx, svc, h, "DNS is switched when the import starts"); err != nil {
				job.Plan.Warnings = append(job.Plan.Warnings, fmt.Sprintf("%s: domain %s skipped: %v", is.Name, h, err))
			}
		}
	}
	job.Steps = append(job.Steps, api.ImportStep{Name: "Create project and services", Status: "done", Detail: strings.Join(created, ", ")})
	for _, is := range plan.Services {
		for _, v := range is.Volumes {
			job.Steps = append(job.Steps, api.ImportStep{Name: volStep(is.Name, v.Name), Status: "pending"})
		}
	}
	job.Steps = append(job.Steps, api.ImportStep{Name: "Deploy services", Status: "pending"}, api.ImportStep{Name: "Configure DNS", Status: "pending"})
	s.saveImport(ctx, job, importKindPlan, meta)
	s.st.Audit(ctx, principalFrom(ctx).actor(), "import.create", job.ID, map[string]string{"project": p.ID})
	writeJSON(w, 201, maskJob(job))
	return nil
}

func volStep(svc, vol string) string { return fmt.Sprintf("Upload volume %s/%s", svc, vol) }

// createPendingDomain binds a hostname without touching DNS yet.
func (s *Server) createPendingDomain(ctx context.Context, svc *api.Service, host, note string) error {
	d, err := s.prepareDomain(ctx, svc, domainRequest{Hostname: host})
	if err != nil {
		return err
	}
	d.ID, d.ServiceID, d.Status, d.Error, d.CreatedAt = newID("dom"), svc.ID, "pending", note, time.Now().UTC()
	return s.st.CreateDomain(ctx, *d)
}

func (s *Server) handleImportVolume(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var meta planMeta
	j, kind, err := s.st.GetImport(ctx, r.PathValue("id"), &meta)
	if err != nil {
		return err
	}
	if kind != importKindPlan {
		return badRequest("volume upload is only for plan imports; in-place Zeabur imports copy data via rehearse")
	}
	if j.Status != "planned" && j.Status != "uploading" {
		return errf(409, "bad_state", "import is %s; volumes can only be uploaded before start", j.Status)
	}
	svcName, volName := r.PathValue("service_name"), r.PathValue("volume")
	svcID := meta.ServiceIDs[svcName]
	if svcID == "" {
		return badRequest("plan has no service %q", svcName)
	}
	svc, _, err := s.st.GetService(ctx, svcID)
	if err != nil {
		return err
	}
	found := false
	for _, v := range svc.Volumes {
		found = found || v.Name == volName
	}
	if !found {
		return badRequest("service %s has no volume %q", svcName, volName)
	}
	p, err := s.st.GetProject(ctx, j.ProjectID)
	if err != nil {
		return err
	}
	if !s.hub.Online(p.ServerID) {
		return errf(409, "server_offline", "target server is offline")
	}
	lockKey := j.ID + "/" + svcName + "/" + volName
	if !s.lockJob(lockKey) {
		return errf(409, "busy", "this volume is already being uploaded")
	}
	defer s.unlockJob(lockKey)
	step := volStep(svcName, volName)
	j.Status = "uploading"
	st := &stepper{steps: &j.Steps, save: func() { s.saveImport(context.Background(), j, kind, meta) }}
	st.run(step)
	blobID, _, getURL, err := s.newBlob(ctx, s.publicURL(ctx, r))
	if err != nil {
		st.fail(step, err)
		return err
	}
	defer s.removeBlob(blobID)
	if err := s.writeBlob(blobID, r.Body); err != nil {
		st.fail(step, err)
		return err
	}
	size, _ := statFile(s.blobPath(blobID))
	if _, err := s.hub.Call(ctx, p.ServerID, api.MsgVolumeImport, api.VolumeTransferPayload{ServiceID: svcID, Volume: volName, URL: getURL}, s.cfg.TransferTimeout); err != nil {
		st.fail(step, err)
		return errf(502, "agent_error", "%v", err)
	}
	st.done(step, humanBytes(size)+" imported")
	writeJSON(w, 200, maskJob(j))
	return nil
}

func (s *Server) handleStartImport(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var meta planMeta
	j, kind, err := s.st.GetImport(ctx, r.PathValue("id"), &meta)
	if err != nil {
		return err
	}
	if kind != importKindPlan {
		return badRequest("in-place Zeabur imports use /rehearse and /cutover instead of /start")
	}
	if j.Status != "planned" && j.Status != "uploading" && j.Status != "failed" {
		return errf(409, "bad_state", "import is already %s", j.Status)
	}
	if !s.lockJob(j.ID) {
		return errf(409, "busy", "import is busy")
	}
	j.Status = "deploying"
	s.saveImport(ctx, j, kind, meta)
	snapshot := maskJob(j)
	s.goBG(func(bg context.Context) {
		defer s.unlockJob(j.ID)
		st := &stepper{steps: &j.Steps, save: func() { s.saveImport(bg, j, kind, meta) }}
		for i := range j.Steps {
			if strings.HasPrefix(j.Steps[i].Name, "Upload volume") && j.Steps[i].Status == "pending" {
				j.Steps[i].Status, j.Steps[i].Detail = "skipped", "no data uploaded; volume starts empty"
			}
		}
		err := st.do("Deploy services", func() (string, error) {
			var ids []string
			for _, svcID := range meta.ServiceIDs {
				d, err := s.deploy(bg, svcID, "import")
				if err != nil {
					return "", err
				}
				ids = append(ids, d.ID)
			}
			j.Status = "verifying"
			if err := s.waitDeployments(bg, ids, s.cfg.DeployWaitTimeout); err != nil {
				return "", err
			}
			return fmt.Sprintf("%d service(s) running", len(ids)), nil
		})
		if err == nil {
			err = st.do("Configure DNS", func() (string, error) { return s.applyProjectDNS(bg, j.ProjectID) })
		}
		j.Status = "done"
		if err != nil {
			j.Status = "failed"
		}
		st.save()
	})
	writeJSON(w, 200, snapshot)
	return nil
}

// applyProjectDNS points every domain of a project at its server.
func (s *Server) applyProjectDNS(ctx context.Context, projectID string) (string, error) {
	p, err := s.st.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	doms, err := s.st.ListDomains(ctx, "", projectID)
	if err != nil {
		return "", err
	}
	if len(doms) == 0 {
		return "no domains", nil
	}
	var bad []string
	for _, d := range doms {
		dd := d.Domain
		s.applyDNS(ctx, &dd, p.ServerID)
		if dd.Status == "error" {
			bad = append(bad, dd.Hostname+": "+dd.Error)
		}
	}
	s.pushIngress(ctx, p.ServerID)
	if len(bad) > 0 {
		return "", fmt.Errorf("DNS failed: %s", strings.Join(bad, "; "))
	}
	return fmt.Sprintf("%d domain(s) configured", len(doms)), nil
}
