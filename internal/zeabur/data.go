package zeabur

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/atoll-sh/atoll/internal/api"
	"github.com/atoll-sh/atoll/internal/zeabur/k8s"
)

// DataManifest lists exported data files (written as data/manifest.json, 0600).
type DataManifest struct {
	SourceProject string      `json:"source_project"`
	Entries       []DataEntry `json:"entries"`
}

type DataEntry struct {
	Service         string           `json:"service"`           // atoll service name (plan)
	SourceServiceID string           `json:"source_service_id"` // zeabur id
	Kind            string           `json:"kind"`              // postgres|mysql|mongo|redis|volume
	Database        string           `json:"database,omitempty"`
	Volume          string           `json:"volume,omitempty"`
	File            string           `json:"file"` // relative to the data dir
	Bytes           int64            `json:"bytes"`
	SHA256          string           `json:"sha256"`
	RowCounts       map[string]int64 `json:"row_counts,omitempty"` // table → rows at dump time
}

// DataOptions configure ExportData.
type DataOptions struct {
	OutDir       string
	IncludeRedis bool // Redis used only as a cache does not need migrating
	Log          io.Writer
}

// transportFor picks how to run commands inside a service's container.
func transportFor(pi *ProjectInventory, cli Runner, s api.ZeaburService) Transport {
	if pi.Host != nil && pi.Namespace != "" && s.Workload != "" {
		return PodExec{Host: HostTransport{Access: *pi.Host, CLI: cli}, Namespace: pi.Namespace, Workload: s.Workload}
	}
	return ServiceExec{CLI: cli, ServiceID: s.ServiceID}
}

// Dump/restore/count scripts (run with sh -c inside the database container; connect via 127.0.0.1).
const (
	pgEnv       = `export PGPASSWORD="${POSTGRES_PASSWORD:-$PASSWORD}"; U="${POSTGRES_USER:-${POSTGRES_USERNAME:-postgres}}"; `
	pgListDBs   = pgEnv + `psql -h 127.0.0.1 -U "$U" -d postgres -Atc "select datname from pg_database where not datistemplate and datname <> 'postgres' order by 1"`
	pgCountSQL  = `select table_schema||'.'||table_name, (xpath('/row/c/text()', query_to_xml(format('select count(*) as c from %I.%I', table_schema, table_name), false, true, '')))[1]::text from information_schema.tables where table_type='BASE TABLE' and table_schema not in ('pg_catalog','information_schema') order by 1`
	myEnv       = `P="${MYSQL_ROOT_PASSWORD:-${MYSQL_PASSWORD:-$PASSWORD}}"; U=root; [ -n "$MYSQL_ROOT_PASSWORD" ] || U="${MYSQL_USER:-${MYSQL_USERNAME:-root}}"; MY=$(command -v mysql || command -v mariadb); `
	myDump      = myEnv + `D=$(command -v mysqldump || command -v mariadb-dump); DBS=$($MY -h 127.0.0.1 -u"$U" -p"$P" -N -e "select schema_name from information_schema.schemata where schema_name not in ('mysql','information_schema','performance_schema','sys')"); $D -h 127.0.0.1 -u"$U" -p"$P" --single-transaction --routines --triggers --events --databases $DBS 2>/dev/null`
	myCountList = myEnv + `$MY -h 127.0.0.1 -u"$U" -p"$P" -N -e "select concat('select ''', table_schema, '.', table_name, ''', count(*) from ` + "`" + `', table_schema, '` + "`.`" + `', table_name, '` + "`" + `') from information_schema.tables where table_type='BASE TABLE' and table_schema not in ('mysql','information_schema','performance_schema','sys')" 2>/dev/null`
	mongoEnv    = `A=""; [ -n "$MONGO_INITDB_ROOT_USERNAME" ] && A="-u $MONGO_INITDB_ROOT_USERNAME -p $MONGO_INITDB_ROOT_PASSWORD --authenticationDatabase admin"; `
	mongoDump   = mongoEnv + `mongodump --host 127.0.0.1 $A --archive --gzip 2>/dev/null`
	redisDump   = `A=""; P="${REDIS_PASSWORD:-$PASSWORD}"; [ -n "$P" ] && A="-a $P --no-auth-warning"; redis-cli -h 127.0.0.1 $A --rdb /tmp/atoll-dump.rdb >/dev/null 2>&1 && cat /tmp/atoll-dump.rdb; rm -f /tmp/atoll-dump.rdb`
)

// PgDumpCmd returns the command dumping one database in custom format.
func PgDumpCmd(db string) string {
	return pgEnv + `pg_dump -h 127.0.0.1 -U "$U" -Fc --no-owner --no-acl ` + shq(db)
}

// PgCountCmd returns exact row counts per table as "schema.table|n" lines.
func PgCountCmd(db string) string {
	return pgEnv + `psql -h 127.0.0.1 -U "$U" -d ` + shq(db) + ` -At -F '|' -c ` + shq(pgCountSQL)
}

// MyCountCmd runs the generated count statements (one per line from myCountList) as one query.
func MyCountCmd(stmts []string) string {
	return myEnv + `$MY -h 127.0.0.1 -u"$U" -p"$P" -N -B -e ` + shq(strings.Join(stmts, " union all ")) + ` 2>/dev/null`
}

// ParseCounts parses "table|n" or "table\tn" lines.
func ParseCounts(out string) map[string]int64 {
	m := map[string]int64{}
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		sep := "|"
		if !strings.Contains(l, sep) {
			sep = "\t"
		}
		i := strings.LastIndex(l, sep)
		if i <= 0 {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(l[i+1:]), 10, 64)
		if err != nil {
			continue
		}
		m[strings.TrimSpace(l[:i])] = n
	}
	return m
}

// SourceCounts reads row counts from a source database (postgres or mysql).
func SourceCounts(ctx context.Context, t Transport, kind, db string) (map[string]int64, error) {
	switch kind {
	case "postgres":
		out, err := t.Output(ctx, PgCountCmd(db))
		if err != nil {
			return nil, err
		}
		return prefixCounts(db, ParseCounts(out)), nil
	case "mysql":
		out, err := t.Output(ctx, myCountList)
		if err != nil {
			return nil, err
		}
		var stmts []string
		for _, l := range strings.Split(out, "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, "select ") {
				stmts = append(stmts, l)
			}
		}
		if len(stmts) == 0 {
			return map[string]int64{}, nil
		}
		out, err = t.Output(ctx, MyCountCmd(stmts))
		if err != nil {
			return nil, err
		}
		return ParseCounts(out), nil
	}
	return nil, nil
}

func prefixCounts(db string, m map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for k, v := range m {
		out[db+":"+k] = v
	}
	return out
}

// writeStream runs cmd through t into path (0600) and returns size and sha256.
func writeStream(ctx context.Context, t Transport, cmd, path string) (int64, string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, "", err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	cw := &countWriter{}
	err = t.Stream(ctx, cmd, io.MultiWriter(f, h, cw))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return cw.n, hex.EncodeToString(h.Sum(nil)), err
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// VerifyArchive checks an exported file by kind.
func VerifyArchive(kind, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 5)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if n == 0 {
		return fmt.Errorf("%s is empty", path)
	}
	f.Seek(0, io.SeekStart)
	switch kind {
	case "postgres":
		if string(head) != "PGDMP" {
			return fmt.Errorf("%s is not a pg_dump custom archive", path)
		}
	case "redis":
		if string(head) != "REDIS" {
			return fmt.Errorf("%s is not an RDB file", path)
		}
	case "mongo":
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if _, err := io.Copy(io.Discard, gz); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	case "mysql":
		st, _ := f.Stat()
		tail := make([]byte, 512)
		off := st.Size() - int64(len(tail))
		if off < 0 {
			off = 0
		}
		n, _ := f.ReadAt(tail, off)
		if !bytes.Contains(tail[:n], []byte("Dump completed")) {
			return fmt.Errorf("%s: dump looks truncated (no 'Dump completed' trailer)", path)
		}
	case "volume":
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		tr := tar.NewReader(gz)
		for {
			_, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
	}
	return nil
}

// ExportData dumps databases with their own same-version tools and archives other volumes.
// Files are chmod 600; manifest.json records sizes, sha256 and source row counts.
func ExportData(ctx context.Context, cli Runner, pi *ProjectInventory, plan api.ImportPlan, opt DataOptions) (*DataManifest, error) {
	names := map[string]string{}
	for _, s := range plan.Services {
		names[s.SourceServiceID] = s.Name
	}
	man := &DataManifest{SourceProject: pi.ID}
	for _, s := range pi.ServicesView() {
		name := names[s.ServiceID]
		if name == "" {
			name = k8s.SanitizeName(s.Name)
		}
		if s.Workload == "" && pi.Host == nil {
			logf(opt.Log, "  %s: suspended and no host access; skipped", name)
			continue
		}
		t := transportFor(pi, cli, s)
		add := func(e DataEntry) error {
			path := filepath.Join(opt.OutDir, e.File)
			if err := VerifyArchive(e.Kind, path); err != nil {
				return err
			}
			man.Entries = append(man.Entries, e)
			logf(opt.Log, "  ✓ %s %s %s  %s  sha256:%s", name, e.Kind, e.Database+e.Volume, humanBytes(e.Bytes), e.SHA256[:12])
			return nil
		}
		dbHandled := true
		switch s.Kind {
		case "postgres":
			out, err := t.Output(ctx, pgListDBs)
			if err != nil {
				return nil, fmt.Errorf("%s: list databases: %w", name, err)
			}
			dbs := strings.Fields(out)
			if len(dbs) == 0 {
				dbs = []string{"postgres"}
			}
			for _, db := range dbs {
				counts, err := SourceCounts(ctx, t, "postgres", db)
				if err != nil {
					logf(opt.Log, "  ! %s/%s: row count failed: %v", name, db, err)
				}
				if len(counts) == 0 {
					logf(opt.Log, "  ! %s/%s: database has no tables (an empty restore is not a validation)", name, db)
				}
				file := filepath.Join(name, db+".pgdump")
				n, sum, err := writeStream(ctx, t, PgDumpCmd(db), filepath.Join(opt.OutDir, file))
				if err != nil {
					return nil, fmt.Errorf("%s/%s: pg_dump: %w", name, db, err)
				}
				if err := add(DataEntry{Service: name, SourceServiceID: s.ServiceID, Kind: "postgres", Database: db, File: file, Bytes: n, SHA256: sum, RowCounts: counts}); err != nil {
					return nil, err
				}
			}
		case "mysql":
			counts, err := SourceCounts(ctx, t, "mysql", "")
			if err != nil {
				logf(opt.Log, "  ! %s: row count failed: %v", name, err)
			}
			file := filepath.Join(name, "all.sql")
			n, sum, err := writeStream(ctx, t, myDump, filepath.Join(opt.OutDir, file))
			if err != nil {
				return nil, fmt.Errorf("%s: mysqldump: %w", name, err)
			}
			if err := add(DataEntry{Service: name, SourceServiceID: s.ServiceID, Kind: "mysql", File: file, Bytes: n, SHA256: sum, RowCounts: counts}); err != nil {
				return nil, err
			}
		case "mongo":
			file := filepath.Join(name, "mongo.archive.gz")
			n, sum, err := writeStream(ctx, t, mongoDump, filepath.Join(opt.OutDir, file))
			if err != nil {
				return nil, fmt.Errorf("%s: mongodump: %w", name, err)
			}
			if err := add(DataEntry{Service: name, SourceServiceID: s.ServiceID, Kind: "mongo", File: file, Bytes: n, SHA256: sum}); err != nil {
				return nil, err
			}
		case "redis":
			if !opt.IncludeRedis {
				logf(opt.Log, "  - %s: redis skipped (cache-only Redis needs no migration; pass --include-redis if the code stores data in it)", name)
				continue
			}
			file := filepath.Join(name, "dump.rdb")
			n, sum, err := writeStream(ctx, t, redisDump, filepath.Join(opt.OutDir, file))
			if err != nil {
				return nil, fmt.Errorf("%s: redis-cli --rdb: %w", name, err)
			}
			if err := add(DataEntry{Service: name, SourceServiceID: s.ServiceID, Kind: "redis", File: file, Bytes: n, SHA256: sum}); err != nil {
				return nil, err
			}
		default:
			dbHandled = false
		}
		if dbHandled {
			continue // database volumes are migrated logically, not as raw files
		}
		for i, v := range s.Volumes {
			vol := k8s.VolumeName(v.PVC, s.ServiceID, i)
			var cmd string
			var tt Transport = t
			if pi.Host != nil && v.HostDir != "" {
				tt = HostTransport{Access: *pi.Host, CLI: cli}
				cmd = "tar czf - -C " + shq(v.HostDir) + " ."
			} else if v.MountPath != "" && s.Workload != "" {
				cmd = "tar czf - -C " + shq(v.MountPath) + " ."
			} else {
				logf(opt.Log, "  ! %s: volume %s has no reachable path; skipped", name, vol)
				continue
			}
			logf(opt.Log, "  … %s volume %s (%s)", name, vol, humanBytes(v.Bytes))
			file := filepath.Join(name, vol+".tar.gz")
			n, sum, err := writeStream(ctx, tt, cmd, filepath.Join(opt.OutDir, file))
			if err != nil {
				return nil, fmt.Errorf("%s/%s: tar: %w", name, vol, err)
			}
			if err := add(DataEntry{Service: name, SourceServiceID: s.ServiceID, Kind: "volume", Volume: vol, File: file, Bytes: n, SHA256: sum}); err != nil {
				return nil, err
			}
		}
	}
	sort.SliceStable(man.Entries, func(i, j int) bool { return man.Entries[i].Service < man.Entries[j].Service })
	return man, WriteSecretJSON(filepath.Join(opt.OutDir, "manifest.json"), man)
}
