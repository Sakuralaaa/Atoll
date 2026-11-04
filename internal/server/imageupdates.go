package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/registry"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// Image update detection. A background loop (every minute) checks every service whose
// last check is older than the interval (setting image_check_interval_hours, default 6h):
// HEAD on the tag's manifest (cached, with per-registry 429 backoff) and, for semver-like
// tags, a tag list. The result is compared at read time with the digest the agent reports,
// so a finished deploy flips the status without another registry request.

const defaultImageCheckHours = 6

type updater struct {
	reg  *registry.Client
	now  func() time.Time
	tick time.Duration

	mu   sync.Mutex
	busy map[string]bool
}

func newUpdater() *updater {
	return &updater{reg: registry.New(), now: time.Now, tick: time.Minute, busy: map[string]bool{}}
}

func (u *updater) lock(id string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.busy[id] {
		return false
	}
	u.busy[id] = true
	return true
}

func (u *updater) unlock(id string) {
	u.mu.Lock()
	delete(u.busy, id)
	u.mu.Unlock()
}

// imageUpdateLoop runs scheduled checks until the server stops.
func (s *Server) imageUpdateLoop() {
	defer s.wg.Done()
	t := time.NewTicker(s.upd.tick)
	defer t.Stop()
	for {
		select {
		case <-s.bg.Done():
			return
		case <-t.C:
			s.runDueImageChecks(s.bg)
		}
	}
}

func (s *Server) imageCheckInterval(ctx context.Context) time.Duration {
	v, _ := s.st.GetSetting(ctx, "image_check_interval_hours")
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Hour
	}
	return defaultImageCheckHours * time.Hour
}

// trackedRef is the tag ref a service follows. A service whose image is digest-only
// (e.g. after rolling back to an image-update deployment, which pins name@digest) keeps
// following the tag recorded by its last check.
func trackedRef(image string, row *store.ImageUpdateRow) (registry.Ref, string, error) {
	ref, err := registry.ParseRef(image)
	if err != nil {
		return ref, "", err
	}
	pinned := ref.Digest
	if ref.Tag == "" && row != nil && row.Image != "" {
		if prev, err := registry.ParseRef(row.Image); err == nil && prev.Name == ref.Name && prev.Tag != "" {
			ref.Tag = prev.Tag
		}
	}
	return ref, pinned, nil
}

// svcCheckable is checkable for a service: services built from git have no registry image.
func svcCheckable(svc *api.Service, ref registry.Ref, err error) string {
	if svc.Git != nil {
		return "built from source; redeploy (or a GitHub push) builds the latest commit"
	}
	return checkable(ref, err)
}

// checkable says why an image cannot be checked ("" = it can).
func checkable(ref registry.Ref, err error) string {
	switch {
	case err != nil:
		return "unrecognised image reference"
	case strings.HasPrefix(ref.Name, strings.TrimSuffix(adoptImageRepo, "/")+"/"):
		return "local image (adopted from Docker); not in a registry"
	case strings.HasPrefix(ref.Name, "atoll-import/"):
		return "local image (imported from Zeabur); not in a registry"
	case strings.HasPrefix(ref.Name, BuildImageRepo):
		return "built from source; redeploy (or a GitHub push) builds the latest commit"
	case ref.Tag == "":
		return "pinned to a digest; nothing to follow"
	}
	return ""
}

func (s *Server) nextImageCheck(ctx context.Context, row *store.ImageUpdateRow) time.Time {
	if row.CheckedAt.IsZero() {
		return time.Time{}
	}
	if row.Failures > 0 && !row.NextCheckAt.IsZero() {
		return row.NextCheckAt
	}
	return row.CheckedAt.Add(s.imageCheckInterval(ctx))
}

// runDueImageChecks checks every due service sequentially (registry friendly) and
// applies pending auto-updates whose window is open.
func (s *Server) runDueImageChecks(ctx context.Context) {
	targets, err := s.st.ImageCheckTargets(ctx)
	if err != nil {
		s.log.Warn("image update targets", "err", err)
		return
	}
	rows, err := s.st.ListImageUpdates(ctx)
	if err != nil {
		s.log.Warn("image update rows", "err", err)
		return
	}
	now := s.upd.now()
	for _, t := range targets {
		if ctx.Err() != nil {
			return
		}
		id, image := t[0], t[1]
		row := rows[id]
		if row == nil {
			row = &store.ImageUpdateRow{ServiceID: id, Policy: api.ImageUpdatePolicyNotify}
		}
		if row.Policy == api.ImageUpdatePolicyOff {
			continue
		}
		ref, _, perr := trackedRef(image, row)
		due := row.CheckedAt.IsZero() || (perr == nil && row.Image != ref.WithTag(ref.Tag) && ref.Tag != "")
		if next := s.nextImageCheck(ctx, row); !due && !next.After(now) {
			due = true
		}
		if due {
			if _, err := s.checkImage(ctx, id); err != nil && !errors.Is(err, errBusy) {
				s.log.Debug("image check", "service", id, "err", err)
			}
			continue
		}
		if row.Policy == api.ImageUpdatePolicyAuto {
			if svc, _, err := s.st.GetService(ctx, id); err == nil {
				s.maybeAutoUpdate(ctx, svc, row)
			}
		}
	}
}

var errBusy = errors.New("check already running")

// checkImage queries the registry for one service now and stores the result.
func (s *Server) checkImage(ctx context.Context, serviceID string) (api.ImageUpdate, error) {
	if !s.upd.lock(serviceID) {
		return api.ImageUpdate{}, errBusy
	}
	defer s.upd.unlock(serviceID)
	svc, _, err := s.st.GetService(ctx, serviceID)
	if err != nil {
		return api.ImageUpdate{}, err
	}
	row, err := s.st.GetImageUpdate(ctx, serviceID)
	if err != nil {
		return api.ImageUpdate{}, err
	}
	now := s.upd.now().UTC()
	ref, _, perr := trackedRef(svc.Image, row)
	if why := svcCheckable(svc, ref, perr); why != "" {
		row.Image, row.LatestDigest, row.NewerTag, row.MajorTag, row.Error = svc.Image, "", "", "", ""
		row.CheckedAt, row.Failures, row.NextCheckAt = now, 0, time.Time{}
		s.st.SaveImageUpdate(ctx, *row)
		v := s.imageUpdateView(ctx, svc, row)
		s.publish("image_update", v)
		return v, nil
	}
	auth := s.registryAuthFor(ctx, svc)
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	tracked := ref.WithTag(ref.Tag)
	digest, err := s.upd.reg.Digest(cctx, ref, auth)
	if err != nil {
		if row.Image != tracked {
			row.LatestDigest, row.NewerTag, row.MajorTag = "", "", ""
		}
		row.Image = tracked
		row.Error = registryErrText(err)
		row.CheckedAt = now
		row.Failures++
		wait := min(5*time.Minute<<min(row.Failures-1, 6), s.imageCheckInterval(ctx))
		next := now.Add(wait)
		var rl *registry.RateLimitError
		if errors.As(err, &rl) && rl.Until.After(next) {
			next = rl.Until
		}
		row.NextCheckAt = next
		s.st.SaveImageUpdate(ctx, *row)
		v := s.imageUpdateView(ctx, svc, row)
		s.publish("image_update", v)
		return v, nil
	}
	row.Image, row.LatestDigest, row.Error = tracked, digest, ""
	row.NewerTag, row.MajorTag = "", ""
	if _, ok := registry.ParseVersion(ref.Tag); ok {
		if tags, terr := s.upd.reg.Tags(cctx, ref, auth); terr == nil {
			row.NewerTag, row.MajorTag = registry.NewerTags(ref.Tag, tags)
		} else {
			s.log.Debug("tag list", "image", tracked, "err", terr)
		}
	}
	s.lookupVersions(cctx, svc, ref, auth, digest)
	row.CheckedAt, row.Failures, row.NextCheckAt = now, 0, time.Time{}
	if err := s.st.SaveImageUpdate(ctx, *row); err != nil {
		return api.ImageUpdate{}, err
	}
	if row.Policy == api.ImageUpdatePolicyAuto {
		s.maybeAutoUpdate(ctx, svc, row)
	}
	v := s.imageUpdateView(ctx, svc, row)
	s.publish("image_update", v)
	return v, nil
}

// lookupVersions reads the version labels of the latest and the running digest (cached per
// digest, so this costs requests only when a digest is new). Failures are only logged: the
// version is a nicety on the card.
func (s *Server) lookupVersions(ctx context.Context, svc *api.Service, ref registry.Ref, auth registry.Auth, latest string) {
	_, pinned, _ := trackedRef(svc.Image, nil)
	for _, d := range []string{latest, s.runningDigest(ctx, svc, pinned)} {
		if _, err := s.upd.reg.Version(ctx, ref, d, auth); err != nil {
			s.log.Debug("image version label", "image", ref.Name, "digest", d, "err", err)
		}
	}
}

func registryErrText(err error) string {
	switch {
	case errors.Is(err, registry.ErrNotFound):
		return "tag not found in the registry (or the repository is private: set a registry credential)"
	case errors.Is(err, registry.ErrUnauthorized):
		return "registry denied access; check the service's registry credential"
	case errors.Is(err, context.DeadlineExceeded):
		return "registry did not answer in time"
	}
	return err.Error()
}

func (s *Server) registryAuthFor(ctx context.Context, svc *api.Service) registry.Auth {
	if svc.RegistryCredID == "" {
		return registry.Auth{}
	}
	var cred api.RegistryAuth
	if err := s.st.GetSecretBlob(ctx, "registry_cred:"+svc.RegistryCredID, &cred); err != nil {
		return registry.Auth{}
	}
	return registry.Auth{Username: cred.Username, Password: cred.Password}
}

// runningDigest is the digest of what actually runs: agent status first, then the
// last running deployment, then a digest pinned in the image ref.
func (s *Server) runningDigest(ctx context.Context, svc *api.Service, pinned string) string {
	if c, _, err := s.st.ContainerState(ctx, svc.ID); err == nil && c.ImageDigest != "" && c.State != "missing" {
		return c.ImageDigest
	}
	if deps, err := s.st.ListDeployments(ctx, svc.ID, 10); err == nil {
		for _, d := range deps {
			if d.Status == "running" && d.ImageDigest != "" {
				return d.ImageDigest
			}
		}
	}
	return pinned
}

// autoPausedReason explains why auto-update would not act now ("" = it may).
func (s *Server) autoPausedReason(ctx context.Context, svc *api.Service) string {
	d, err := s.st.LatestDeployment(ctx, svc.ID)
	if err != nil {
		return "service has never been deployed"
	}
	if d.Trigger == "rollback" {
		return "auto-update paused: the service was rolled back; apply an update manually to resume"
	}
	if inProgress(d.Status) {
		return "a deployment is in progress"
	}
	return ""
}

func (s *Server) imageUpdateView(ctx context.Context, svc *api.Service, row *store.ImageUpdateRow) api.ImageUpdate {
	v := api.ImageUpdate{
		ServiceID: svc.ID, ProjectID: svc.ProjectID, Service: svc.Name, Image: svc.Image,
		Policy: row.Policy, Window: row.Window, LastAutoDigest: row.LastAutoDigest,
	}
	if v.Policy == "" {
		v.Policy = api.ImageUpdatePolicyNotify
	}
	if !row.CheckedAt.IsZero() {
		t := row.CheckedAt
		v.CheckedAt = &t
	}
	ref, pinned, perr := trackedRef(svc.Image, row)
	v.CurrentDigest = s.runningDigest(ctx, svc, pinned)
	if why := svcCheckable(svc, ref, perr); why != "" {
		v.Status, v.Note = "unsupported", why
		return v
	}
	v.CurrentTag = ref.Tag
	v.ChangelogURL = ref.ChangelogURL()
	if v.Policy == api.ImageUpdatePolicyOff {
		v.Status = "disabled"
		return v
	}
	if next := s.nextImageCheck(ctx, row); !next.IsZero() {
		v.NextCheckAt = &next
	}
	if row.CheckedAt.IsZero() || row.Image != ref.WithTag(ref.Tag) {
		v.Status = "unknown"
		v.Note = "not checked yet"
		return v
	}
	v.LatestDigest, v.NewerTag, v.MajorTag = row.LatestDigest, row.NewerTag, row.MajorTag
	if row.Error != "" {
		v.Status, v.Error = "error", row.Error
		return v
	}
	v.CurrentVersion, _ = registry.CachedVersion(v.CurrentDigest)
	v.LatestVersion, _ = registry.CachedVersion(v.LatestDigest)
	v.DigestChanged = v.CurrentDigest != "" && v.LatestDigest != "" && v.CurrentDigest != v.LatestDigest
	v.UpdateAvailable = v.DigestChanged || v.NewerTag != ""
	if pinned != "" {
		// Fixed to a digest on purpose (or by a rollback): report, never nag.
		v.Pinned, v.UpdateAvailable = true, false
		v.Status = "pinned"
		return v
	}
	switch {
	case v.UpdateAvailable:
		v.Status = "update_available"
	case v.CurrentDigest == "":
		v.Status, v.Note = "unknown", "the agent has not reported the running digest yet"
	default:
		v.Status = "up_to_date"
	}
	if v.Policy == api.ImageUpdatePolicyAuto && v.DigestChanged {
		switch {
		case v.LatestDigest == row.LastAutoDigest:
			v.Note = "auto-update already deployed this digest once; it is not retried automatically"
		case !inWindow(s.upd.now(), row.Window):
			v.Note = "auto-update waits for the window " + row.Window + " UTC"
		default:
			if r := s.autoPausedReason(ctx, svc); r != "" {
				v.Note = r
			}
		}
	}
	return v
}

// maybeAutoUpdate redeploys a service pinned to the new digest of its tag when its
// policy is auto, the window is open, and nothing argues against it. Tag bumps are
// never applied automatically.
func (s *Server) maybeAutoUpdate(ctx context.Context, svc *api.Service, row *store.ImageUpdateRow) {
	if row.Policy != api.ImageUpdatePolicyAuto || row.Error != "" || row.LatestDigest == "" || row.LatestDigest == row.LastAutoDigest {
		return
	}
	ref, pinned, err := trackedRef(svc.Image, row)
	if err != nil || pinned != "" || checkable(ref, nil) != "" || row.Image != ref.WithTag(ref.Tag) {
		return
	}
	cur := s.runningDigest(ctx, svc, pinned)
	if cur == "" || cur == row.LatestDigest || !inWindow(s.upd.now(), row.Window) {
		return
	}
	if s.autoPausedReason(ctx, svc) != "" {
		return
	}
	if node, err := s.serviceNode(ctx, svc); err != nil || !s.hub.Online(node) {
		return
	}
	row.LastAutoDigest = row.LatestDigest
	if err := s.st.SaveImageUpdate(ctx, *row); err != nil {
		return
	}
	d, err := s.deployDigest(ctx, svc, ref, row.LatestDigest, "auto-update")
	if err != nil {
		s.log.Warn("auto-update failed", "service", svc.ID, "err", err)
		return
	}
	s.log.Info("auto-update", "service", svc.ID, "image", ref.WithTag(ref.Tag), "digest", row.LatestDigest, "deployment", d.ID)
	s.st.Audit(ctx, "system", "service.image_update", svc.ID, map[string]string{"digest": row.LatestDigest, "mode": "auto"})
}

// deployDigest deploys svc with its image pinned to name@digest. The service keeps its
// tag ref (svc.Image), so later redeploys follow the tag; the deployment history keeps
// the previous spec for rollback.
func (s *Server) deployDigest(ctx context.Context, svc *api.Service, ref registry.Ref, digest, why string) (*api.Deployment, error) {
	if want := ref.WithTag(ref.Tag); svc.Image != want {
		svc.Image = want // drop a stale digest pin so later redeploys follow the tag
		if err := s.st.UpdateServiceSpec(ctx, *svc); err != nil {
			return nil, err
		}
	}
	depID := newID("dep")
	spec, warnings, err := s.buildSpec(ctx, svc, depID)
	if err != nil {
		return nil, err
	}
	spec.Image = ref.Pinned(digest)
	msg := fmt.Sprintf("%s: %s → %s", why, ref.WithTag(ref.Tag), shortDigest(digest))
	return s.deploySpec(ctx, svc, spec, "image-update", append([]string{msg}, warnings...))
}

func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		d = d[:12]
	}
	return d
}

// inWindow reports whether now (UTC) is inside "HH:MM-HH:MM"; "" = always.
func inWindow(now time.Time, w string) bool {
	if w == "" {
		return true
	}
	a, b, err := parseWindow(w)
	if err != nil {
		return false
	}
	now = now.UTC()
	m := now.Hour()*60 + now.Minute()
	if a <= b {
		return m >= a && m < b
	}
	return m >= a || m < b // wraps midnight
}

func parseWindow(w string) (int, int, error) {
	from, to, ok := strings.Cut(strings.TrimSpace(w), "-")
	if !ok {
		return 0, 0, fmt.Errorf("window must look like 02:00-05:00")
	}
	p := func(s string) (int, error) {
		t, err := time.Parse("15:04", strings.TrimSpace(s))
		if err != nil {
			return 0, fmt.Errorf("invalid time %q (want HH:MM)", s)
		}
		return t.Hour()*60 + t.Minute(), nil
	}
	a, err := p(from)
	if err != nil {
		return 0, 0, err
	}
	b, err := p(to)
	if err != nil {
		return 0, 0, err
	}
	if a == b {
		return 0, 0, fmt.Errorf("window start and end must differ")
	}
	return a, b, nil
}

// ---------- handlers ----------

func (s *Server) handleGetImageUpdate(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	row, err := s.st.GetImageUpdate(r.Context(), svc.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, s.imageUpdateView(r.Context(), svc, row))
	return nil
}

// handleListImageUpdates: GET /image-updates[?available=1]
func (s *Server) handleListImageUpdates(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	targets, err := s.st.ImageCheckTargets(ctx)
	if err != nil {
		return err
	}
	rows, err := s.st.ListImageUpdates(ctx)
	if err != nil {
		return err
	}
	onlyAvail := r.URL.Query().Get("available") == "1"
	pr := principalFrom(ctx)
	out := []api.ImageUpdate{}
	for _, t := range targets {
		svc, _, err := s.st.GetService(ctx, t[0])
		if err != nil || !pr.inProject(svc.ProjectID) {
			continue
		}
		row := rows[svc.ID]
		if row == nil {
			row = &store.ImageUpdateRow{ServiceID: svc.ID, Policy: api.ImageUpdatePolicyNotify}
		}
		v := s.imageUpdateView(ctx, svc, row)
		if onlyAvail && !v.UpdateAvailable {
			continue
		}
		out = append(out, v)
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) handleCheckImageUpdate(w http.ResponseWriter, r *http.Request) error {
	if _, _, err := s.st.GetService(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	v, err := s.checkImage(r.Context(), r.PathValue("id"))
	if errors.Is(err, errBusy) {
		return errf(409, "busy", "a check for this service is already running")
	}
	if err != nil {
		return err
	}
	writeJSON(w, 200, v)
	return nil
}

func (s *Server) handlePutImageUpdatePolicy(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	svc, _, err := s.st.GetService(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	var req api.ImageUpdatePolicy
	if err := decode(r, &req); err != nil {
		return err
	}
	switch req.Policy {
	case api.ImageUpdatePolicyNotify, api.ImageUpdatePolicyAuto, api.ImageUpdatePolicyOff:
	case "":
		req.Policy = api.ImageUpdatePolicyNotify
	default:
		return badRequest("policy must be notify, auto or off")
	}
	req.Window = strings.TrimSpace(req.Window)
	if req.Window != "" {
		if _, _, err := parseWindow(req.Window); err != nil {
			return badRequest("%v", err)
		}
	}
	row, err := s.st.GetImageUpdate(ctx, svc.ID)
	if err != nil {
		return err
	}
	row.Policy, row.Window = req.Policy, req.Window
	if err := s.st.SaveImageUpdate(ctx, *row); err != nil {
		return err
	}
	s.st.Audit(ctx, principalFrom(ctx).actor(), "service.image_update_policy", svc.ID, req)
	v := s.imageUpdateView(ctx, svc, row)
	s.publish("image_update", v)
	writeJSON(w, 200, v)
	return nil
}

func (s *Server) handleApplyImageUpdate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	svc, _, err := s.st.GetService(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	var req api.ImageUpdateApplyRequest
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil {
			return err
		}
	}
	req.Tag = strings.TrimSpace(req.Tag)
	if !s.upd.lock(svc.ID) {
		return errf(409, "busy", "an update check for this service is running; try again in a moment")
	}
	defer s.upd.unlock(svc.ID)
	row, err := s.st.GetImageUpdate(ctx, svc.ID)
	if err != nil {
		return err
	}
	ref, _, perr := trackedRef(svc.Image, row)
	if why := svcCheckable(svc, ref, perr); why != "" {
		return badRequest("cannot update %s: %s", svc.Image, why)
	}
	if req.Tag != "" && req.Tag != ref.Tag {
		if strings.ContainsAny(req.Tag, "/:@ ") {
			return badRequest("tag must be a plain tag such as 1.28 or v2.0.0")
		}
		nref, err := registry.ParseRef(ref.WithTag(req.Tag))
		if err != nil {
			return badRequest("invalid tag %q", req.Tag)
		}
		ref = nref
	}
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	digest, err := s.upd.reg.Digest(cctx, ref, s.registryAuthFor(ctx, svc))
	if err != nil {
		return errf(502, "registry_error", "%s: %s", ref.WithTag(ref.Tag), registryErrText(err))
	}
	d, err := s.deployDigest(ctx, svc, ref, digest, "image update")
	if err != nil {
		return err
	}
	// Record what we know so the card is accurate before the next scheduled check.
	if row.Image != ref.WithTag(ref.Tag) {
		row.NewerTag, row.MajorTag = "", ""
		if tags, terr := s.upd.reg.Tags(cctx, ref, s.registryAuthFor(ctx, svc)); terr == nil {
			row.NewerTag, row.MajorTag = registry.NewerTags(ref.Tag, tags)
		}
	}
	row.Image, row.LatestDigest, row.Error = ref.WithTag(ref.Tag), digest, ""
	row.CheckedAt, row.Failures, row.NextCheckAt = s.upd.now().UTC(), 0, time.Time{}
	s.st.SaveImageUpdate(ctx, *row)
	s.st.Audit(ctx, principalFrom(ctx).actor(), "service.image_update", svc.ID, map[string]string{"image": ref.WithTag(ref.Tag), "digest": digest})
	if nsvc, _, err := s.st.GetService(ctx, svc.ID); err == nil {
		s.publish("image_update", s.imageUpdateView(ctx, nsvc, row))
	}
	writeJSON(w, 200, d)
	return nil
}

func intStr(n int) string { return strconv.Itoa(n) }

// pinRollbackImage pins a rollback to the digest the old deployment actually ran, so
// rolling back across an image update restores the old image even though its spec only
// names the tag (which now points to the new digest). Local images are left alone.
func pinRollbackImage(image, digest string) string {
	if !strings.HasPrefix(digest, "sha256:") {
		return image
	}
	ref, err := registry.ParseRef(image)
	if checkable(ref, err) != "" || ref.Digest != "" {
		return image
	}
	return ref.Pinned(digest)
}
