package zeabur

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/atoll-sh/atoll/internal/api"
	"github.com/atoll-sh/atoll/internal/client"
	"github.com/atoll-sh/atoll/internal/zeabur/k8s"
)

// importVolume is added to database services to carry logical dumps into the new container.
const importVolume = "atoll-import"
const importMount = "/atoll-import"

// ApplyState is written after apply (apply-state.json, 0600) and read by cutover.
type ApplyState struct {
	ImportID       string              `json:"import_id"`
	ProjectID      string              `json:"project_id"`
	Services       map[string]string   `json:"services"`        // atoll name → id
	PendingDomains map[string][]string `json:"pending_domains"` // added at cutover
}

// ApplyOptions configure Apply.
type ApplyOptions struct {
	DataDir     string // from `import zeabur data`, optional
	WithDomains bool   // create domains now (default: at cutover, so DNS is not switched early)
	StateFile   string
	Log         io.Writer
}

// tarGz packs files (name in archive → local path) into a tar.gz in memory-free streaming fashion.
func tarGz(w io.Writer, files map[string]string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f, err := os.Open(files[n])
		if err != nil {
			return err
		}
		st, _ := f.Stat()
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o600, Size: st.Size(), ModTime: st.ModTime()}); err != nil {
			f.Close()
			return err
		}
		_, err = io.Copy(tw, f)
		f.Close()
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func uploadTarGz(ctx context.Context, c *client.Client, importID, svc, vol string, files map[string]string) error {
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tarGz(pw, files)) }()
	return c.UploadImportVolume(ctx, importID, svc, vol, pr, -1)
}

func uploadFile(ctx context.Context, c *client.Client, importID, svc, vol, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, _ := f.Stat()
	return c.UploadImportVolume(ctx, importID, svc, vol, f, st.Size())
}

// PrepareForApply adjusts a plan for the data that will be uploaded and returns the domains
// held back for cutover.
func PrepareForApply(plan *api.ImportPlan, man *DataManifest, withDomains bool) map[string][]string {
	needImport := map[string]bool{}
	if man != nil {
		for _, e := range man.Entries {
			if e.Kind == "postgres" || e.Kind == "mysql" || e.Kind == "mongo" {
				needImport[e.Service] = true
			}
		}
	}
	pending := map[string][]string{}
	for i := range plan.Services {
		s := &plan.Services[i]
		if needImport[s.Name] {
			has := false
			for _, v := range s.Volumes {
				has = has || v.Name == importVolume
			}
			if !has {
				s.Volumes = append(s.Volumes, api.Volume{Name: importVolume, MountPath: importMount})
			}
		}
		if !withDomains && len(s.Domains) > 0 {
			pending[s.Name] = s.Domains
			s.Domains = []string{}
		}
	}
	return pending
}

// RestoreCmd returns the restore command for a dump inside the new database container.
func RestoreCmd(e DataEntry) string {
	file := importMount + "/" + filepath.Base(e.File)
	switch e.Kind {
	case "postgres":
		return pgEnv + `psql -h 127.0.0.1 -U "$U" -d postgres -tc "select 1 from pg_database where datname='` + strings.ReplaceAll(e.Database, "'", "''") + `'" | grep -q 1 || createdb -h 127.0.0.1 -U "$U" ` + shq(e.Database) +
			`; pg_restore -h 127.0.0.1 -U "$U" -d ` + shq(e.Database) + ` --clean --if-exists --no-owner --no-acl ` + shq(file)
	case "mysql":
		return myEnv + `$MY -h 127.0.0.1 -u"$U" -p"$P" < ` + shq(file)
	case "mongo":
		return mongoEnv + `mongorestore --host 127.0.0.1 $A --drop --gzip --archive=` + shq(file)
	}
	return ""
}

// readyCmd checks the database accepts connections.
func readyCmd(kind string) string {
	switch kind {
	case "postgres":
		return pgEnv + `pg_isready -h 127.0.0.1 -U "$U"`
	case "mysql":
		return myEnv + `$MY -h 127.0.0.1 -u"$U" -p"$P" -e "select 1"`
	case "mongo":
		return `(mongosh --quiet --eval 1 || mongo --quiet --eval 1) >/dev/null`
	}
	return "true"
}

// AtollTransport runs commands in an Atoll service through the exec API.
type AtollTransport struct {
	C         *client.Client
	ServiceID string
}

func (a AtollTransport) Describe() string { return "atoll exec " + a.ServiceID }
func (a AtollTransport) Output(ctx context.Context, cmd string) (string, error) {
	r, err := a.C.Exec(ctx, a.ServiceID, []string{"sh", "-c", cmd})
	if err != nil {
		return "", err
	}
	if !r.OK {
		return r.Output, fmt.Errorf("command failed: %s", strings.TrimSpace(r.Error))
	}
	return r.Output, nil
}
func (a AtollTransport) Stream(ctx context.Context, cmd string, w io.Writer) error {
	out, err := a.Output(ctx, cmd)
	io.WriteString(w, out)
	return err
}

func waitReady(ctx context.Context, t Transport, kind string, tries int) error {
	var err error
	for i := 0; i < tries; i++ {
		if _, err = t.Output(ctx, readyCmd(kind)); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return err
}

// RestoreDumps restores every dump of man into the matching Atoll service and returns
// a row-count comparison.
func RestoreDumps(ctx context.Context, c *client.Client, man *DataManifest, ids map[string]string, log io.Writer) ([]CountRow, error) {
	var rows []CountRow
	for _, e := range man.Entries {
		cmd := RestoreCmd(e)
		if cmd == "" {
			continue
		}
		id := ids[e.Service]
		if id == "" {
			return rows, fmt.Errorf("service %s not found in the Atoll project", e.Service)
		}
		t := AtollTransport{C: c, ServiceID: id}
		if err := waitReady(ctx, t, e.Kind, 24); err != nil {
			return rows, fmt.Errorf("%s did not become ready: %w", e.Service, err)
		}
		logf(log, "  restoring %s %s", e.Service, e.Database)
		if _, err := t.Output(ctx, cmd); err != nil {
			return rows, fmt.Errorf("restore %s %s: %w", e.Service, e.Database, err)
		}
		if e.Kind == "postgres" || e.Kind == "mysql" {
			got, err := SourceCounts(ctx, t, e.Kind, e.Database)
			if err != nil {
				logf(log, "  ! row count on target failed: %v", err)
			}
			rows = append(rows, CompareCounts(e.Service, e.RowCounts, got)...)
		}
	}
	return rows, nil
}

// CountRow compares one table's row count.
type CountRow struct {
	Service, Table string
	Source, Target int64
}

func (r CountRow) OK() bool { return r.Source == r.Target }

// CompareCounts lines up source and target counts (missing tables count as -1).
func CompareCounts(service string, src, dst map[string]int64) []CountRow {
	keys := map[string]bool{}
	for k := range src {
		keys[k] = true
	}
	for k := range dst {
		keys[k] = true
	}
	var rows []CountRow
	for k := range keys {
		s, ok := src[k]
		if !ok {
			s = -1
		}
		d, ok := dst[k]
		if !ok {
			d = -1
		}
		rows = append(rows, CountRow{Service: service, Table: k, Source: s, Target: d})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Table < rows[j].Table })
	return rows
}

// PrintCounts prints the comparison and reports whether everything matched.
func PrintCounts(w io.Writer, rows []CountRow) bool {
	if len(rows) == 0 {
		fmt.Fprintln(w, "No tables to compare (empty databases are not a validation of a populated migration).")
		return true
	}
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tTABLE\tSOURCE\tTARGET\t")
	ok := true
	for _, r := range rows {
		mark := "✓"
		if !r.OK() {
			mark, ok = "✗", false
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n", r.Service, r.Table, r.Source, r.Target, mark)
	}
	tw.Flush()
	return ok
}

// Apply creates the Atoll project from plan, uploads volumes and dumps, starts it, restores
// databases and compares row counts.
func Apply(ctx context.Context, c *client.Client, plan api.ImportPlan, opt ApplyOptions) (*ApplyState, error) {
	var man *DataManifest
	if opt.DataDir != "" {
		man = &DataManifest{}
		if err := ReadJSON(filepath.Join(opt.DataDir, "manifest.json"), man); err != nil {
			return nil, fmt.Errorf("read data manifest: %w", err)
		}
	}
	for _, s := range plan.Services {
		if s.ImageNeedsPush {
			return nil, fmt.Errorf("service %s still uses a Zeabur-registry image; run `atoll import zeabur images` or set its image in plan.json first", s.Name)
		}
	}
	pending := PrepareForApply(&plan, man, opt.WithDomains)
	job, err := c.CreateImport(ctx, plan)
	if err != nil {
		return nil, err
	}
	logf(opt.Log, "import %s created (project %s)", job.ID, job.ProjectID)
	st := &ApplyState{ImportID: job.ID, ProjectID: job.ProjectID, PendingDomains: pending, Services: map[string]string{}}
	if man != nil {
		dumps := map[string]map[string]string{}
		for _, e := range man.Entries {
			path := filepath.Join(opt.DataDir, e.File)
			switch e.Kind {
			case "volume":
				logf(opt.Log, "  uploading %s/%s (%s)", e.Service, e.Volume, humanBytes(e.Bytes))
				if err := uploadFile(ctx, c, job.ID, e.Service, e.Volume, path); err != nil {
					return st, fmt.Errorf("upload %s/%s: %w", e.Service, e.Volume, err)
				}
			case "redis":
				vol := "data"
				for _, s := range plan.Services {
					if s.Name == e.Service && len(s.Volumes) > 0 {
						vol = s.Volumes[0].Name
					}
				}
				logf(opt.Log, "  uploading %s RDB into volume %s", e.Service, vol)
				if err := uploadTarGz(ctx, c, job.ID, e.Service, vol, map[string]string{"dump.rdb": path}); err != nil {
					return st, err
				}
			default:
				if dumps[e.Service] == nil {
					dumps[e.Service] = map[string]string{}
				}
				dumps[e.Service][filepath.Base(e.File)] = path
			}
		}
		for svc, files := range dumps {
			logf(opt.Log, "  uploading %d dump(s) for %s", len(files), svc)
			if err := uploadTarGz(ctx, c, job.ID, svc, importVolume, files); err != nil {
				return st, err
			}
		}
	}
	if _, err := c.StartImport(ctx, job.ID); err != nil {
		return st, err
	}
	last := ""
	job, err = c.WaitImport(ctx, job.ID, 3*time.Second, func(j *api.ImportJob) {
		for _, s := range j.Steps {
			line := s.Name + ": " + s.Status
			if s.Status == "running" && line != last {
				logf(opt.Log, "  %s %s", line, s.Detail)
				last = line
			}
		}
	})
	if err != nil {
		return st, err
	}
	if job.ProjectID != "" {
		st.ProjectID = job.ProjectID
	}
	if p, err := c.GetProject(ctx, st.ProjectID); err == nil {
		for _, s := range p.Services {
			st.Services[s.Name] = s.ID
		}
	}
	if opt.StateFile != "" {
		if err := WriteSecretJSON(opt.StateFile, st); err != nil {
			return st, err
		}
	}
	if man != nil {
		rows, err := RestoreDumps(ctx, c, man, st.Services, opt.Log)
		if !PrintCounts(opt.Log, rows) {
			logf(opt.Log, "Row counts differ: investigate before cutover.")
		}
		if err != nil {
			return st, err
		}
	}
	return st, nil
}

// ---------- images (remote path; stopgap, the long-term answer is a reproducible build) ----------

// ExportImages exports Zeabur-built images from the dedicated server's containerd, pushes them
// to pushTo with local docker (or skopeo/crane) and rewrites the plan's image refs.
func ExportImages(ctx context.Context, cli Runner, pi *ProjectInventory, plan *api.ImportPlan, pushTo, outDir string, log io.Writer) error {
	if pi.Host == nil {
		return fmt.Errorf("image export needs host access to the dedicated server (shared clusters cannot export images; rebuild from source instead)")
	}
	host := HostTransport{Access: *pi.Host, CLI: cli}
	src := map[string]api.ZeaburService{}
	for _, s := range pi.ServicesView() {
		src[s.ServiceID] = s
	}
	for i := range plan.Services {
		s := &plan.Services[i]
		if !s.ImageNeedsPush {
			continue
		}
		zs := src[s.SourceServiceID]
		ref := zs.Image
		tmp := "/tmp/atoll-export-" + s.SourceServiceID + ".tar"
		file := filepath.Join(outDir, "images", s.Name+".tar")
		logf(log, "  exporting %s from containerd", s.Name)
		n, _, err := writeStream(ctx, host, "k3s ctr -n k8s.io images export "+shq(tmp)+" "+shq(ref)+" >/dev/null && cat "+shq(tmp)+"; rm -f "+shq(tmp), file)
		if err != nil {
			return fmt.Errorf("%s: export: %w", s.Name, err)
		}
		tag := "zeabur"
		if _, t, _ := k8s.SplitImage(ref); t != "" {
			tag = t
		}
		dst := strings.TrimRight(pushTo, "/") + "/" + k8s.SanitizeName(plan.ProjectName) + "-" + s.Name + ":" + tag
		logf(log, "  pushing %s (%s) → %s", s.Name, humanBytes(n), dst)
		if err := pushArchive(ctx, file, ref, dst); err != nil {
			return fmt.Errorf("%s: push: %w", s.Name, err)
		}
		s.Image, s.ImageNeedsPush = dst, false
	}
	return nil
}

func pushArchive(ctx context.Context, file, ref, dst string) error {
	run := func(name string, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s %s: %v: %s", name, args[0], err, lastLines(out.String(), 3))
		}
		return nil
	}
	if _, err := exec.LookPath("docker"); err == nil {
		if err := run("docker", "load", "-i", file); err != nil {
			return err
		}
		if err := run("docker", "tag", ref, dst); err != nil {
			return err
		}
		return run("docker", "push", dst)
	}
	if _, err := exec.LookPath("skopeo"); err == nil {
		return run("skopeo", "copy", "oci-archive:"+file, "docker://"+dst)
	}
	if _, err := exec.LookPath("crane"); err == nil {
		return run("crane", "push", file, dst)
	}
	return fmt.Errorf("need docker, skopeo or crane on this machine to push %s", file)
}

// ---------- cutover (remote path) ----------

// DNSRecord is a domain's current resolution, recorded for rollback.
type DNSRecord struct {
	Host  string   `json:"host"`
	CNAME string   `json:"cname,omitempty"`
	IPs   []string `json:"ips"`
}

// LookupDNS records current A/AAAA/CNAME answers for hosts.
func LookupDNS(ctx context.Context, hosts []string) []DNSRecord {
	var r net.Resolver
	var out []DNSRecord
	for _, h := range hosts {
		rec := DNSRecord{Host: h}
		if c, err := r.LookupCNAME(ctx, h); err == nil && strings.TrimSuffix(c, ".") != h {
			rec.CNAME = strings.TrimSuffix(c, ".")
		}
		if ips, err := r.LookupHost(ctx, h); err == nil {
			rec.IPs = ips
		}
		out = append(out, rec)
	}
	return out
}

// AppServiceIDs returns the Zeabur service ids that cutover may suspend: applications only,
// never databases or caches.
func AppServiceIDs(services []api.ZeaburService) []string {
	var ids []string
	for _, s := range services {
		if s.Role == "app" && s.Workload != "" {
			ids = append(ids, s.ServiceID)
		}
	}
	sort.Strings(ids)
	return ids
}
