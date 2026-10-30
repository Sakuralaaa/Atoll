package zeabur

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

// DBResult is the zeabur.db result Output.
type DBResult struct {
	Kind       string           `json:"kind"`
	Databases  []string         `json:"databases,omitempty"`
	Bytes      int64            `json:"bytes"`
	Source     map[string]int64 `json:"source_counts,omitempty"` // "db.schema.table" (pg), "db.table" (mysql), "keys" (redis)
	Target     map[string]int64 `json:"target_counts,omitempty"`
	Match      bool             `json:"match"`
	Mismatches []string         `json:"mismatches,omitempty"`
	Warnings   []string         `json:"warnings,omitempty"`
}

var dbNameRe = regexp.MustCompile(`^[A-Za-z0-9_$-]{1,63}$`)

// pod is the source pod of a Zeabur service.
type pod struct{ Namespace, Name, Container string }

// ParsePods picks the first Running pod (and its first container) from `kubectl get pods -o json`.
func ParsePods(b []byte) (pod, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Name string `json:"name"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return pod{}, err
	}
	for _, it := range list.Items {
		if it.Status.Phase == "Running" && len(it.Spec.Containers) > 0 {
			return pod{Namespace: it.Metadata.Namespace, Name: it.Metadata.Name, Container: it.Spec.Containers[0].Name}, nil
		}
	}
	return pod{}, errors.New("no running pod found (is the Zeabur service scaled to 0?)")
}

func (t *Takeover) sourcePod(ctx context.Context, ns, serviceID string) (pod, error) {
	if !nsRe.MatchString(ns) || !regexp.MustCompile(`^[a-z0-9]+$`).MatchString(serviceID) {
		return pod{}, fmt.Errorf("invalid namespace/service %q/%q", ns, serviceID)
	}
	out, err := t.kubectl(ctx, "get", "pods", "-n", ns, "-l", "zeabur_service_id="+serviceID, "-o", "json")
	if err != nil {
		return pod{}, err
	}
	p, err := ParsePods(out)
	if p.Namespace == "" {
		p.Namespace = ns
	}
	return p, err
}

func (t *Takeover) srcArgs(p pod, script string) []string {
	return []string{"kubectl", "exec", "-n", p.Namespace, p.Name, "-c", p.Container, "--", "sh", "-c", script}
}

func (t *Takeover) srcOutput(ctx context.Context, p pod, script string) (string, error) {
	bin, err := t.k3sBin()
	if err != nil {
		return "", err
	}
	out, err := t.X.Output(ctx, bin, t.srcArgs(p, script)...)
	return string(out), err
}

func (t *Takeover) dstOutput(ctx context.Context, container, script string) (string, error) {
	var out, errb bytes.Buffer
	code, err := t.RT.Exec(ctx, container, []string{"sh", "-c", script}, nil, &out, &errb)
	if err != nil {
		return out.String(), err
	}
	if code != 0 {
		return out.String(), fmt.Errorf("exit %d: %s", code, lastN(errb.String(), 2000))
	}
	return out.String(), nil
}

type countWriter struct {
	w io.Writer
	n atomic.Int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}

// pipe streams the source script's stdout into the target script's stdin.
func (t *Takeover) pipe(ctx context.Context, p pod, srcScript, container, dstScript string) (int64, error) {
	bin, err := t.k3sBin()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	cw := &countWriter{w: pw}
	srcErr := make(chan error, 1)
	go func() {
		err := t.X.Stream(ctx, nil, cw, bin, t.srcArgs(p, srcScript)...)
		pw.CloseWithError(err)
		srcErr <- err
	}()
	var errb bytes.Buffer
	code, derr := t.RT.Exec(ctx, container, []string{"sh", "-c", dstScript}, pr, io.Discard, &errb)
	pr.CloseWithError(errors.New("restore finished"))
	if derr != nil || code != 0 {
		cancel()
	}
	serr := <-srcErr
	switch {
	case serr != nil && !errors.Is(serr, context.Canceled):
		return cw.n.Load(), fmt.Errorf("dump: %w", serr)
	case derr != nil:
		return cw.n.Load(), fmt.Errorf("restore: %w", derr)
	case code != 0:
		return cw.n.Load(), fmt.Errorf("restore exited %d: %s", code, lastN(errb.String(), 2000))
	}
	return cw.n.Load(), nil
}

// MigrateDB dumps the Zeabur database and restores it into the atoll container (same major
// version expected). Re-runnable: restores replace existing objects (final sync).
func (t *Takeover) MigrateDB(ctx context.Context, p api.ZeaburDBPayload) (DBResult, error) {
	res := DBResult{Kind: p.Kind}
	src, err := t.sourcePod(ctx, p.Namespace, p.SourceServiceID)
	if err != nil {
		return res, err
	}
	target := agent.ContainerName(p.TargetServiceID)
	if p.Kind != "redis" { // redis is restored by file while stopped
		info, err := t.RT.InspectContainer(ctx, target)
		if err != nil {
			return res, fmt.Errorf("target %s: %w", target, err)
		}
		if info.State != "running" {
			return res, fmt.Errorf("target %s must be running (state %s)", target, info.State)
		}
	}
	switch p.Kind {
	case "postgres", "postgresql":
		err = t.migratePostgres(ctx, src, target, &res)
	case "mysql", "mariadb":
		err = t.migrateMySQL(ctx, src, target, &res)
	case "mongo", "mongodb":
		err = t.migrateMongo(ctx, src, target, &res)
	case "redis":
		err = t.migrateRedis(ctx, src, p.TargetServiceID, &res)
	default:
		return res, fmt.Errorf("unsupported database kind %q", p.Kind)
	}
	if err != nil {
		return res, err
	}
	// A mismatch is reported, not raised: during rehearsal Zeabur still serves writes, so
	// counts can legitimately drift. The server requires Match only for the final sync.
	res.Match, res.Mismatches = CompareCounts(res.Source, res.Target)
	return res, nil
}

// CompareCounts reports whether both maps are equal, listing differences.
func CompareCounts(src, dst map[string]int64) (bool, []string) {
	var diffs []string
	keys := map[string]bool{}
	for k := range src {
		keys[k] = true
	}
	for k := range dst {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		s, okS := src[k]
		d, okD := dst[k]
		if !okS || !okD || s != d {
			diffs = append(diffs, fmt.Sprintf("%s: source=%s target=%s", k, fmtCount(s, okS), fmtCount(d, okD)))
		}
	}
	return len(diffs) == 0, diffs
}

func fmtCount(n int64, ok bool) string {
	if !ok {
		return "missing"
	}
	return strconv.FormatInt(n, 10)
}

// ParseCounts parses "name<TAB or |>count" lines.
func ParseCounts(out, prefix string) map[string]int64 {
	m := map[string]int64{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.LastIndexAny(line, "\t|")
		if i <= 0 {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(line[i+1:]), 10, 64)
		if err != nil {
			continue
		}
		m[prefix+strings.TrimSpace(line[:i])] = n
	}
	return m
}

func splitNames(out string, skip map[string]bool, res *DBResult) []string {
	var dbs []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || skip[l] {
			continue
		}
		if !dbNameRe.MatchString(l) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipped database with unusual name %q", l))
			continue
		}
		dbs = append(dbs, l)
	}
	return dbs
}

// ---------- postgres ----------

const pgEnv = `U="${POSTGRES_USER:-${POSTGRES_USERNAME:-${PGUSER:-postgres}}}"; export PGPASSWORD="${POSTGRES_PASSWORD:-$PGPASSWORD}"; export PGHOST=127.0.0.1; `

// pgCountSQL counts rows of every user table exactly in one statement.
const pgCountSQL = `select table_schema||'.'||table_name, (xpath('/row/c/text()', query_to_xml(format('select count(*) as c from %I.%I', table_schema, table_name), false, true, '')))[1]::text::bigint from information_schema.tables where table_schema not in ('pg_catalog','information_schema') and table_type='BASE TABLE' order by 1`

func (t *Takeover) migratePostgres(ctx context.Context, src pod, target string, res *DBResult) error {
	out, err := t.srcOutput(ctx, src, pgEnv+`psql -U "$U" -d postgres -Atc "select datname from pg_database where not datistemplate and datallowconn order by 1"`)
	if err != nil {
		return fmt.Errorf("list databases: %w", err)
	}
	res.Databases = splitNames(out, nil, res)
	res.Source, res.Target = map[string]int64{}, map[string]int64{}
	for _, db := range res.Databases {
		q := shq(db)
		dump := pgEnv + `pg_dump -U "$U" -Fc --no-owner --no-acl -d ` + q
		restore := pgEnv + `psql -U "$U" -d postgres -Atc "select 1 from pg_database where datname='` + db + `'" | grep -q 1 || createdb -U "$U" ` + q +
			`; pg_restore -U "$U" -d ` + q + ` --no-owner --no-acl --clean --if-exists`
		n, err := t.pipe(ctx, src, dump, target, restore)
		res.Bytes += n
		if err != nil {
			return fmt.Errorf("database %s: %w", db, err)
		}
		count := pgEnv + `psql -U "$U" -d ` + q + ` -At -F '	' -c ` + shq(pgCountSQL)
		so, err := t.srcOutput(ctx, src, count)
		if err != nil {
			return fmt.Errorf("count source %s: %w", db, err)
		}
		do, err := t.dstOutput(ctx, target, count)
		if err != nil {
			return fmt.Errorf("count target %s: %w", db, err)
		}
		for k, v := range ParseCounts(so, db+".") {
			res.Source[k] = v
		}
		for k, v := range ParseCounts(do, db+".") {
			res.Target[k] = v
		}
	}
	return nil
}

// ---------- mysql ----------

const myEnv = `if [ -n "$MYSQL_ROOT_PASSWORD" ]; then U=root; export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; else U="${MYSQL_USER:-${MYSQL_USERNAME:-root}}"; export MYSQL_PWD="${MYSQL_PASSWORD:-$PASSWORD}"; fi; M=$(command -v mysql || command -v mariadb); D=$(command -v mysqldump || command -v mariadb-dump); `

var mysqlSystem = map[string]bool{"mysql": true, "information_schema": true, "performance_schema": true, "sys": true}

func mysqlCountScripts(dbs []string, tables []string) string {
	var parts []string
	for _, tb := range tables {
		i := strings.IndexByte(tb, '.')
		if i <= 0 {
			continue
		}
		d, n := tb[:i], tb[i+1:]
		parts = append(parts, fmt.Sprintf("select '%s', count(*) from `%s`.`%s`",
			strings.ReplaceAll(tb, "'", "''"), strings.ReplaceAll(d, "`", "``"), strings.ReplaceAll(n, "`", "``")))
	}
	return strings.Join(parts, " union all ")
}

func (t *Takeover) mysqlCounts(ctx context.Context, run func(string) (string, error), dbs []string) (map[string]int64, error) {
	in := make([]string, len(dbs))
	for i, d := range dbs {
		in[i] = "'" + d + "'"
	}
	list, err := run(myEnv + `"$M" -h127.0.0.1 -u"$U" -N -B -e ` + shq("select concat(table_schema,'.',table_name) from information_schema.tables where table_type='BASE TABLE' and table_schema in ("+strings.Join(in, ",")+") order by 1"))
	if err != nil {
		return nil, err
	}
	var tables []string
	for _, l := range strings.Split(list, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			tables = append(tables, l)
		}
	}
	if len(tables) == 0 {
		return map[string]int64{}, nil
	}
	out, err := run(myEnv + `"$M" -h127.0.0.1 -u"$U" -N -B -e ` + shq(mysqlCountScripts(dbs, tables)))
	if err != nil {
		return nil, err
	}
	return ParseCounts(out, ""), nil
}

func (t *Takeover) migrateMySQL(ctx context.Context, src pod, target string, res *DBResult) error {
	out, err := t.srcOutput(ctx, src, myEnv+`"$M" -h127.0.0.1 -u"$U" -N -B -e "show databases"`)
	if err != nil {
		return fmt.Errorf("list databases: %w", err)
	}
	res.Databases = splitNames(out, mysqlSystem, res)
	if len(res.Databases) == 0 {
		res.Warnings = append(res.Warnings, "no user databases found")
		return nil
	}
	var q []string
	for _, d := range res.Databases {
		q = append(q, shq(d))
	}
	dump := myEnv + `"$D" -h127.0.0.1 -u"$U" --single-transaction --routines --triggers --events --add-drop-database --databases ` + strings.Join(q, " ")
	restore := myEnv + `"$M" -h127.0.0.1 -u"$U"`
	n, err := t.pipe(ctx, src, dump, target, restore)
	res.Bytes = n
	if err != nil {
		return err
	}
	if res.Source, err = t.mysqlCounts(ctx, func(s string) (string, error) { return t.srcOutput(ctx, src, s) }, res.Databases); err != nil {
		return fmt.Errorf("count source: %w", err)
	}
	if res.Target, err = t.mysqlCounts(ctx, func(s string) (string, error) { return t.dstOutput(ctx, target, s) }, res.Databases); err != nil {
		return fmt.Errorf("count target: %w", err)
	}
	return nil
}

// ---------- mongo ----------

const mongoEnv = `set --; if [ -n "$MONGO_INITDB_ROOT_USERNAME" ]; then set -- --username "$MONGO_INITDB_ROOT_USERNAME" --password "$MONGO_INITDB_ROOT_PASSWORD" --authenticationDatabase admin; fi; `

func (t *Takeover) migrateMongo(ctx context.Context, src pod, target string, res *DBResult) error {
	n, err := t.pipe(ctx, src, mongoEnv+`mongodump --host 127.0.0.1 "$@" --archive --gzip`,
		target, mongoEnv+`mongorestore --host 127.0.0.1 "$@" --archive --gzip --drop`)
	res.Bytes = n
	if err != nil {
		return err
	}
	res.Warnings = append(res.Warnings, "per-collection counts are not verified for MongoDB")
	return nil
}

// ---------- redis ----------

const redisEnv = `P="${REDIS_PASSWORD:-$PASSWORD}"; set --; if [ -n "$P" ]; then set -- -a "$P" --no-auth-warning; fi; `

func (t *Takeover) migrateRedis(ctx context.Context, src pod, targetServiceID string, res *DBResult) error {
	spec, err := agent.NewStateStore(t.Paths).Load(targetServiceID)
	if err != nil {
		return fmt.Errorf("target service %s: %w", targetServiceID, err)
	}
	var dataDir string
	for _, v := range spec.Volumes {
		if v.MountPath == "/data" {
			dataDir = t.Paths.VolumeDir(targetServiceID, v.Name)
		}
	}
	if dataDir == "" {
		return errors.New("target redis has no volume mounted at /data")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	srcKeys, err := t.srcOutput(ctx, src, redisEnv+`redis-cli -h 127.0.0.1 "$@" dbsize`)
	if err != nil {
		return fmt.Errorf("source dbsize: %w", err)
	}
	bin, err := t.k3sBin()
	if err != nil {
		return err
	}
	tmp := filepath.Join(dataDir, ".atoll-dump.rdb.tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	cw := &countWriter{w: f}
	err = t.X.Stream(ctx, nil, cw, bin, t.srcArgs(src, redisEnv+`redis-cli -h 127.0.0.1 "$@" --rdb /tmp/atoll-dump.rdb >/dev/null && cat /tmp/atoll-dump.rdb; rc=$?; rm -f /tmp/atoll-dump.rdb; exit $rc`)...)
	f.Close()
	res.Bytes = cw.n.Load()
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("dump rdb: %w", err)
	}
	if res.Bytes < 9 {
		os.Remove(tmp)
		return errors.New("rdb dump is empty")
	}
	container := agent.ContainerName(targetServiceID)
	if err := t.RT.StopContainer(ctx, container, 30*time.Second); err != nil && !errors.Is(err, agent.ErrNotFound) {
		return err
	}
	if fi, err := os.Stat(dataDir); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			_ = os.Chown(tmp, int(st.Uid), int(st.Gid))
		}
	}
	_ = os.Chmod(tmp, 0o644)
	// An AOF would take precedence over the RDB at startup; set it aside.
	stamp := time.Now().UTC().Format("20060102T150405")
	for _, aof := range []string{"appendonlydir", "appendonly.aof"} {
		if _, err := os.Stat(filepath.Join(dataDir, aof)); err == nil {
			_ = os.Rename(filepath.Join(dataDir, aof), filepath.Join(dataDir, aof+".atoll-bak-"+stamp))
			res.Warnings = append(res.Warnings, "moved existing "+aof+" aside")
		}
	}
	if err := os.Rename(tmp, filepath.Join(dataDir, "dump.rdb")); err != nil {
		return err
	}
	if !spec.Stopped {
		if err := t.RT.StartContainer(ctx, container); err != nil {
			return err
		}
	}
	res.Source = ParseCounts("keys|"+strings.TrimSpace(srcKeys), "")
	if spec.Stopped {
		res.Target = res.Source
		res.Warnings = append(res.Warnings, "target is stopped; key count not verified")
		return nil
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		out, err := t.dstOutput(ctx, container, redisEnv+`redis-cli -h 127.0.0.1 "$@" dbsize`)
		if err == nil {
			if n, perr := strconv.ParseInt(strings.TrimSpace(out), 10, 64); perr == nil {
				res.Target = map[string]int64{"keys": n}
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("target redis did not come back: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(t.PollEvery):
		}
	}
}
