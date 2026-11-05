package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// CreateProject inserts a project.
func (s *Store) CreateProject(ctx context.Context, p api.Project, ingressHold bool) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO projects(id, team_id, name, display_name, description, server_id, ingress_hold, created_at, root_domain)
		VALUES(?, (SELECT MIN(id) FROM teams), ?,?,?,?,?,?,?)`, p.ID, p.Name, p.DisplayName, p.Description, p.ServerID, b2i(ingressHold), TS(p.CreatedAt), p.RootDomain)
	return mapErr(err)
}

func scanProject(row interface{ Scan(...any) error }) (*api.Project, error) {
	var p api.Project
	var c string
	if err := row.Scan(&p.ID, &p.Name, &p.DisplayName, &p.Description, &p.ServerID, &c, &p.RootDomain); err != nil {
		return nil, mapErr(err)
	}
	p.CreatedAt = parseTS(c)
	return &p, nil
}

// GetProject returns a project without services.
func (s *Store) GetProject(ctx context.Context, id string) (*api.Project, error) {
	return scanProject(s.DB.QueryRowContext(ctx, `SELECT id, name, display_name, description, server_id, created_at, root_domain FROM projects WHERE id=?`, id))
}

// ListProjects returns all projects, optionally only those on serverID.
func (s *Store) ListProjects(ctx context.Context, serverID string) ([]api.Project, error) {
	q := `SELECT id, name, display_name, description, server_id, created_at, root_domain FROM projects`
	args := []any{}
	if serverID != "" {
		q += ` WHERE server_id=?`
		args = append(args, serverID)
	}
	rows, err := s.r.QueryContext(ctx, q+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// UpdateProject updates name, display name and description.
func (s *Store) UpdateProject(ctx context.Context, id, name, displayName, desc string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE projects SET name=?, display_name=?, description=? WHERE id=?`, name, displayName, desc, id)
	return mapErr(err)
}

// SetProjectRootDomain sets the root domain new domains of a project default to ("" = global default).
func (s *Store) SetProjectRootDomain(ctx context.Context, id, root string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE projects SET root_domain=? WHERE id=?`, root, id)
	return err
}

// SetProjectServer moves a project to another node (data only).
func (s *Store) SetProjectServer(ctx context.Context, id, serverID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE projects SET server_id=? WHERE id=?`, serverID, id)
	return err
}

// SetIngressHold toggles whether the project's domains are routed.
func (s *Store) SetIngressHold(ctx context.Context, id string, hold bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE projects SET ingress_hold=? WHERE id=?`, b2i(hold), id)
	return err
}

// DeleteProject deletes a project (cascades to services, env, domains, deployments).
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- services ----------

const svcCols = `id, project_id, name, kind, image, command, args, ports, volumes, configs, health,
	cpu_limit, mem_limit_mb, registry_cred_id, status, replicas, created_at, run_as, display_name`

// svcNode is a service's effective server: its own placement, else the project's server.
const svcNode = `COALESCE(NULLIF(services.server_id, ''), (SELECT p.server_id FROM projects p WHERE p.id = services.project_id), '')`

// EffNode is the same expression for queries that join services s with projects p.
const EffNode = `COALESCE(NULLIF(s.server_id, ''), p.server_id)`

// SetServiceServer places a service on serverID ("" or the project's server = follow the project).
func (s *Store) SetServiceServer(ctx context.Context, id, serverID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET server_id=CASE WHEN ?=(SELECT server_id FROM projects WHERE id=services.project_id) THEN '' ELSE ? END WHERE id=?`,
		serverID, serverID, id)
	return err
}

// ClearServicePlacements makes every service of a project follow the project's server again.
func (s *Store) ClearServicePlacements(ctx context.Context, projectID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET server_id='' WHERE project_id=?`, projectID)
	return err
}

// CountServicesOnNode counts services whose effective server is nodeID.
func (s *Store) CountServicesOnNode(ctx context.Context, nodeID string) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM services s JOIN projects p ON p.id=s.project_id WHERE `+EffNode+`=?`, nodeID).Scan(&n)
	return n, err
}

func scanService(row interface{ Scan(...any) error }) (*api.Service, bool, error) {
	var v api.Service
	var cmd, args, ports, vols, cfgs, health, c string
	var stopped int
	err := row.Scan(&v.ID, &v.ProjectID, &v.Name, &v.Kind, &v.Image, &cmd, &args, &ports, &vols, &cfgs, &health,
		&v.CPULimit, &v.MemLimitMB, &v.RegistryCredID, &v.Status, &v.Replicas, &c, &v.User, &v.DisplayName, &stopped, &v.ServerID)
	if err != nil {
		return nil, false, mapErr(err)
	}
	json.Unmarshal([]byte(cmd), &v.Command)
	json.Unmarshal([]byte(args), &v.Args)
	json.Unmarshal([]byte(ports), &v.Ports)
	json.Unmarshal([]byte(vols), &v.Volumes)
	json.Unmarshal([]byte(cfgs), &v.Configs)
	json.Unmarshal([]byte(health), &v.Health)
	if v.Ports == nil {
		v.Ports = []api.Port{}
	}
	if v.Volumes == nil {
		v.Volumes = []api.Volume{}
	}
	v.CreatedAt = parseTS(c)
	return &v, stopped == 1, nil
}

// CreateService inserts a service.
func (s *Store) CreateService(ctx context.Context, v api.Service) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO services(`+svcCols+`, desired_stopped) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)`,
		v.ID, v.ProjectID, v.Name, v.Kind, v.Image, mustJSON(v.Command), mustJSON(v.Args), mustJSON(nz(v.Ports)), mustJSON(nzv(v.Volumes)),
		mustJSON(v.Configs), mustJSON(v.Health), v.CPULimit, v.MemLimitMB, v.RegistryCredID, v.Status, v.Replicas, TS(v.CreatedAt), v.User, v.DisplayName)
	return mapErr(err)
}

func nz(p []api.Port) []api.Port {
	if p == nil {
		return []api.Port{}
	}
	return p
}
func nzv(p []api.Volume) []api.Volume {
	if p == nil {
		return []api.Volume{}
	}
	return p
}

// UpdateServiceSpec writes the editable spec fields of v.
func (s *Store) UpdateServiceSpec(ctx context.Context, v api.Service) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET name=?, image=?, command=?, args=?, ports=?, volumes=?, configs=?, health=?,
		cpu_limit=?, mem_limit_mb=?, registry_cred_id=?, run_as=? WHERE id=?`,
		v.Name, v.Image, mustJSON(v.Command), mustJSON(v.Args), mustJSON(nz(v.Ports)), mustJSON(nzv(v.Volumes)), mustJSON(v.Configs),
		mustJSON(v.Health), v.CPULimit, v.MemLimitMB, v.RegistryCredID, v.User, v.ID)
	return mapErr(err)
}

// SetServiceDisplayName sets the panel label of a service ("" = show its name).
func (s *Store) SetServiceDisplayName(ctx context.Context, id, name string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET display_name=? WHERE id=?`, name, id)
	return err
}

// SetServiceStatus updates status and replicas ("" leaves replicas unchanged).
func (s *Store) SetServiceStatus(ctx context.Context, id, status, replicas string) error {
	if replicas == "" {
		_, err := s.DB.ExecContext(ctx, `UPDATE services SET status=? WHERE id=?`, status, id)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET status=?, replicas=? WHERE id=?`, status, replicas, id)
	return err
}

// SetDesiredStopped records whether the user wants the service stopped.
func (s *Store) SetDesiredStopped(ctx context.Context, id string, stopped bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET desired_stopped=? WHERE id=?`, b2i(stopped), id)
	return err
}

// GetService returns a service and whether it is desired-stopped.
func (s *Store) GetService(ctx context.Context, id string) (*api.Service, bool, error) {
	v, stopped, err := scanService(s.DB.QueryRowContext(ctx, `SELECT `+svcCols+`, desired_stopped, `+svcNode+` FROM services WHERE id=?`, id))
	if err != nil {
		return nil, false, err
	}
	return v, stopped, s.attachGit(ctx, v)
}

// ListServices returns services of a project in their saved order (position), then by name.
func (s *Store) ListServices(ctx context.Context, projectID string) ([]api.Service, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+svcCols+`, desired_stopped, `+svcNode+` FROM services WHERE project_id=? ORDER BY position=0, position, name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.Service{}
	for rows.Next() {
		v, _, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		if err := s.attachGit(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// DeleteService removes a service row (cascades env, domains, deployments).
func (s *Store) DeleteService(ctx context.Context, id string) error {
	s.DB.ExecContext(ctx, `DELETE FROM container_states WHERE service_id=?`, id)
	s.DB.ExecContext(ctx, `DELETE FROM container_metrics WHERE service_id=?`, id)
	s.DB.ExecContext(ctx, `DELETE FROM mesh_ports WHERE service_id=?`, id)
	// its own registry credential (POST .../services registry_auth), if not shared
	s.DB.ExecContext(ctx, `DELETE FROM settings WHERE key = 'registry_cred:' || (SELECT registry_cred_id FROM services WHERE id=? AND registry_cred_id LIKE 'reg\_%' ESCAPE '\')
		AND NOT EXISTS (SELECT 1 FROM services o, services me WHERE me.id=? AND o.id<>me.id AND o.registry_cred_id=me.registry_cred_id)`, id, id)
	res, err := s.DB.ExecContext(ctx, `DELETE FROM services WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- env ----------

// ListEnv returns a service's vars with secrets decrypted.
func (s *Store) ListEnv(ctx context.Context, serviceID string) ([]api.EnvVar, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT key, value, is_secret, exposed FROM env_vars WHERE service_id=? ORDER BY position, id`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.EnvVar{}
	for rows.Next() {
		var e api.EnvVar
		var sec, exp int
		if err := rows.Scan(&e.Key, &e.Value, &sec, &exp); err != nil {
			return nil, err
		}
		e.Secret, e.Exposed = sec == 1, exp == 1
		if e.Secret {
			if e.Value, err = s.Box.Decrypt(e.Value); err != nil {
				return nil, err
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ReplaceEnv replaces all vars of a service. Values must be plaintext.
func (s *Store) ReplaceEnv(ctx context.Context, serviceID string, vars []api.EnvVar) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.replaceEnvTx(ctx, tx, serviceID, vars); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) replaceEnvTx(ctx context.Context, tx *sql.Tx, serviceID string, vars []api.EnvVar) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM env_vars WHERE service_id=?`, serviceID); err != nil {
		return err
	}
	for i, e := range vars {
		val := e.Value
		if e.Secret {
			var err error
			if val, err = s.Box.Encrypt(val); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO env_vars(service_id, key, value, is_secret, exposed, position) VALUES(?,?,?,?,?,?)`,
			serviceID, e.Key, val, b2i(e.Secret), b2i(e.Exposed), i); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// ---------- domains ----------

const domCols = `id, service_id, hostname, port_name, proxied, status, error, created_at, cf_record_id`

// DomainRow is a domain with its DNS record id.
type DomainRow struct {
	api.Domain
	RecordID string
}

func scanDomain(row interface{ Scan(...any) error }) (*DomainRow, error) {
	var d DomainRow
	var prox int
	var c string
	if err := row.Scan(&d.ID, &d.ServiceID, &d.Hostname, &d.PortName, &prox, &d.Status, &d.Error, &c, &d.RecordID); err != nil {
		return nil, mapErr(err)
	}
	d.Proxied = prox == 1
	d.CreatedAt = parseTS(c)
	return &d, nil
}

// CreateDomain inserts a domain.
func (s *Store) CreateDomain(ctx context.Context, d api.Domain) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO domains(id, service_id, hostname, port_name, proxied, status, error, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		d.ID, d.ServiceID, d.Hostname, d.PortName, b2i(d.Proxied), d.Status, d.Error, TS(d.CreatedAt))
	return mapErr(err)
}

// SetDomainStatus updates DNS status.
func (s *Store) SetDomainStatus(ctx context.Context, id, status, errMsg, recordID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE domains SET status=?, error=?, cf_record_id=CASE WHEN ?='' THEN cf_record_id ELSE ? END WHERE id=?`,
		status, errMsg, recordID, recordID, id)
	return err
}

// SetDomainProxied records whether the domain's DNS record is Cloudflare proxied.
func (s *Store) SetDomainProxied(ctx context.Context, id string, proxied bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE domains SET proxied=? WHERE id=?`, b2i(proxied), id)
	return err
}

// ClearDomainRecord sets a domain's status and forgets its DNS record ID (the record was deleted).
func (s *Store) ClearDomainRecord(ctx context.Context, id, status, errMsg string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE domains SET status=?, error=?, cf_record_id='' WHERE id=?`, status, errMsg, id)
	return err
}

// GetDomain returns one domain.
func (s *Store) GetDomain(ctx context.Context, id string) (*DomainRow, error) {
	return scanDomain(s.DB.QueryRowContext(ctx, `SELECT `+domCols+` FROM domains WHERE id=?`, id))
}

// ListDomains lists domains; serviceID/projectID filter when non-empty.
func (s *Store) ListDomains(ctx context.Context, serviceID, projectID string) ([]DomainRow, error) {
	q := `SELECT d.id, d.service_id, d.hostname, d.port_name, d.proxied, d.status, d.error, d.created_at, d.cf_record_id
		FROM domains d JOIN services s ON s.id = d.service_id WHERE 1=1`
	args := []any{}
	if serviceID != "" {
		q += ` AND d.service_id=?`
		args = append(args, serviceID)
	}
	if projectID != "" {
		q += ` AND s.project_id=?`
		args = append(args, projectID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY d.hostname`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DomainRow{}
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// DeleteDomain removes a domain.
func (s *Store) DeleteDomain(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM domains WHERE id=?`, id)
	return err
}

// RouteRow is one ingress route candidate for a node.
type RouteRow struct {
	Hostname  string
	ServiceID string
	PortName  string
	Ports     []api.Port
	Proxied   bool
}

// RoutesForNode lists domains of services on nodeID whose project is not on ingress hold.
func (s *Store) RoutesForNode(ctx context.Context, nodeID string) ([]RouteRow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT d.hostname, d.service_id, d.port_name, s.ports, d.proxied FROM domains d
		JOIN services s ON s.id=d.service_id JOIN projects p ON p.id=s.project_id
		WHERE `+EffNode+`=? AND p.ingress_hold=0 AND s.desired_stopped=0 ORDER BY d.hostname`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RouteRow
	for rows.Next() {
		var r RouteRow
		var ports string
		var prox int
		if err := rows.Scan(&r.Hostname, &r.ServiceID, &r.PortName, &ports, &prox); err != nil {
			return nil, err
		}
		r.Proxied = prox == 1
		json.Unmarshal([]byte(ports), &r.Ports)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetServiceOrder saves the manual order of a project's services: ids lists them first to
// last (1..n); services not listed keep position 0 and sort after them by name.
func (s *Store) SetServiceOrder(ctx context.Context, projectID string, ids []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE services SET position=0 WHERE project_id=?`, projectID); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE services SET position=? WHERE id=? AND project_id=?`, i+1, id, projectID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
