package k8s

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/atoll-sh/atoll/internal/api"
)

// ---------- images & kinds ----------

var zeaburRegistries = []string{
	"registry-oci.zeabur.cloud", "registry-oci-iad1.zeabur.cloud", "registry-oci.zeabur.cn",
	"registry.zeabur.com", "registry.zeabur.cn",
}

// IsZeaburRegistry reports whether image lives in a Zeabur registry (stops serving at shutdown).
func IsZeaburRegistry(image string) bool {
	host := strings.SplitN(image, "/", 2)[0]
	for _, r := range zeaburRegistries {
		if host == r {
			return true
		}
	}
	return strings.HasPrefix(host, "registry-oci") && strings.Contains(host, ".zeabur.")
}

// SplitImage returns repository, tag and digest of an image reference.
func SplitImage(ref string) (repo, tag, digest string) {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref, digest = ref[:i], ref[i+1:]
	}
	slash := strings.LastIndex(ref, "/")
	if i := strings.LastIndex(ref, ":"); i > slash {
		return ref[:i], ref[i+1:], digest
	}
	return ref, "", digest
}

// IsFloating reports whether the tag can move (latest or no tag, and no digest).
func IsFloating(ref string) bool {
	_, tag, digest := SplitImage(ref)
	return digest == "" && (tag == "" || tag == "latest" || tag == "stable" || tag == "main" || tag == "master")
}

// DigestOf extracts "sha256:..." from a pod imageID such as
// "docker.io/library/postgres@sha256:ab..." or "docker-pullable://...@sha256:...".
func DigestOf(imageID string) string {
	if i := strings.Index(imageID, "sha256:"); i >= 0 {
		d := imageID[i:]
		if len(d) >= 71 {
			return d[:71]
		}
		return d
	}
	return ""
}

// PinImage pins a floating image to the running digest ("repo@sha256:...").
// Non-floating references and unknown digests are returned unchanged.
func PinImage(image, imageID string) string {
	if !IsFloating(image) {
		return image
	}
	d := DigestOf(imageID)
	if d == "" {
		return image
	}
	repo, _, _ := SplitImage(image)
	return repo + "@" + d
}

// DetectKind guesses the Atoll kind and role from an image reference. Kind is one of
// image|postgres|mysql|redis|mongo; role app|database|cache|other.
//
// The exact repository-name table (DataStoreImage, ported from zeabur-oss) decides first;
// the substring heuristics below only catch variants (postgis, timescaledb, pgvector, …) and
// skip tools named after an engine (postgres-exporter, mongo-express, redis-commander, …).
func DetectKind(image string) (kind, role string) {
	switch ds := DataStoreImage(image); ds {
	case StorePostgres, StoreMysql, StoreMongo, StoreRedis:
		return AtollKind(ds, image)
	}
	repo, _, _ := SplitImage(strings.ToLower(image))
	base := repo
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		base = repo[i+1:]
	}
	for _, tool := range []string{"exporter", "express", "operator", "commander", "insight", "admin", "backup", "proxy", "bouncer", "sentinel-ui", "-ui", "web"} {
		if strings.Contains(base, tool) {
			return "image", "app"
		}
	}
	switch {
	case strings.Contains(base, "postgres") || strings.Contains(base, "postgis") || strings.Contains(base, "timescaledb") || base == "pgvector":
		return "postgres", "database"
	case base == "mysql" || strings.Contains(base, "mariadb") || strings.HasPrefix(base, "mysql-") || base == "percona-server":
		return "mysql", "database"
	case strings.Contains(base, "mongo"):
		return "mongo", "database"
	case base == "redis" || strings.HasPrefix(base, "redis-") || base == "valkey" || base == "keydb" || base == "dragonfly":
		return "redis", "cache"
	case base == "clickhouse-server" || base == "clickhouse" || base == "meilisearch" || base == "elasticsearch" || base == "qdrant":
		return "image", "database"
	case base == "minio" || base == "rabbitmq" || base == "nats" || base == "kafka":
		return "image", "other"
	}
	return "image", "app"
}

// ---------- secrets ----------

var secretKeyRe = regexp.MustCompile(`(?i)(PASS|SECRET|TOKEN|PRIVATE|CREDENTIAL|API_?KEY|ACCESS_?KEY|AUTH|SALT|SIGNING|ENCRYPT|_KEY$|^KEY$|DSN|CONNECTION_STRING|_URI$|DATABASE_URL|REDIS_URL|MONGO_URL|COOKIE|SESSION)`)
var credURLRe = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]*:[^/\s@]+@`)

// IsSecret reports whether a variable should be treated as secret.
func IsSecret(key, value string) bool {
	return secretKeyRe.MatchString(key) || credURLRe.MatchString(value)
}

// Hash returns a short sha256 fingerprint used to compare values without printing them.
func Hash(value string) string {
	s := sha256.Sum256([]byte(value))
	return hex.EncodeToString(s[:])[:12]
}

// Mask renders a value for display: secrets become "•••• (sha256:xxxx)", long values are
// truncated. Empty stays "(empty)".
func Mask(key, value string) string {
	if value == "" {
		return "(empty)"
	}
	if IsSecret(key, value) {
		return "•••• (sha256:" + Hash(value) + ")"
	}
	if len(value) > 60 {
		return value[:57] + "…"
	}
	return value
}

// ---------- expansion ----------

var refRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_.]*)\}`)

// Expand resolves ${VAR} references the way Zeabur does at deploy time: own variables first,
// then variables received from other services (readonly), recursively. Unresolved references
// are left in place and returned.
func Expand(own, received map[string]string) (map[string]string, []string) {
	out := make(map[string]string, len(own))
	unresolved := map[string]bool{}
	var resolve func(key string, depth int, stack map[string]bool) (string, bool)
	lookup := func(k string) (string, bool) {
		if v, ok := own[k]; ok {
			return v, true
		}
		v, ok := received[k]
		return v, ok
	}
	resolve = func(key string, depth int, stack map[string]bool) (string, bool) {
		raw, ok := lookup(key)
		if !ok {
			return "", false
		}
		if depth > 16 || stack[key] {
			unresolved[key] = true
			return raw, true
		}
		stack[key] = true
		defer delete(stack, key)
		return refRe.ReplaceAllStringFunc(raw, func(m string) string {
			name := m[2 : len(m)-1]
			if name == key {
				if v, ok := received[name]; ok && v != raw {
					return v
				}
				unresolved[name] = true
				return m
			}
			v, ok := resolve(name, depth+1, stack)
			if !ok {
				unresolved[name] = true
				return m
			}
			return v
		}), true
	}
	for k := range own {
		v, _ := resolve(k, 0, map[string]bool{})
		out[k] = v
	}
	var u []string
	for k := range unresolved {
		u = append(u, k)
	}
	sort.Strings(u)
	return out, u
}

// ---------- .zeabur.internal rewrite ----------

var internalRe = regexp.MustCompile(`([a-z0-9]([a-z0-9-]*[a-z0-9])?)(\.environment-[a-z0-9]+)?\.zeabur\.internal\b`)
var serviceHostRe = regexp.MustCompile(`\bservice-([a-f0-9]{24})\b`)

// Rewriter maps Zeabur hostnames to Atoll service names and old public domains to new ones.
type Rewriter struct {
	ByDNSName   map[string]string // zeabur dnsName → atoll service name
	ByServiceID map[string]string // zeabur service id → atoll service name
	// Domains maps old public hosts (typically *.zeabur.app) to new hosts. Every occurrence of
	// the old host in a value (bare host, URL, list) is replaced; "" entries are left alone.
	Domains map[string]string
	// ByK8sService maps Kubernetes Service names (api.ZeaburService.K8sServices) to Atoll
	// service names. Hosts naming them by cluster DNS (name.<ns>[.svc[.cluster.local]]) are
	// always rewritten; a bare short name only as a whole value or a URL host (after "://" or
	// "@"), since a short word elsewhere in text is not necessarily a host.
	ByK8sService map[string]string
	// Namespace is the Zeabur namespace (environment-<id>); "" accepts any environment-<id>.
	Namespace string
}

// rewriteServiceHosts replaces Kubernetes Service hosts (connections.go rules) in v.
func (r Rewriter) rewriteServiceHosts(v string) string {
	lookup := func(name string) (string, bool) {
		if n, ok := r.ByK8sService[name]; ok {
			return n, true
		}
		if m := serviceHostRe.FindStringSubmatch(name); m != nil && m[0] == name {
			n, ok := r.ByServiceID[m[1]]
			return n, ok
		}
		return "", false
	}
	if len(r.ByK8sService) == 0 && len(r.ByServiceID) == 0 {
		return v
	}
	var b strings.Builder
	i := 0
	for i < len(v) {
		if !hostChar(v[i]) {
			b.WriteByte(v[i])
			i++
			continue
		}
		j := i
		for j < len(v) && hostChar(v[j]) {
			j++
		}
		tok := v[i:j]
		out := tok
		if name, suffix, ok := splitServiceHost(tok, r.Namespace); ok {
			if n, ok := lookup(name); ok {
				urlHost := strings.HasSuffix(v[:i], "://") || strings.HasSuffix(v[:i], "@")
				if suffix != "" || urlHost || tok == v || strings.HasPrefix(name, "service-") {
					out = n
				}
			}
		}
		b.WriteString(out)
		i = j
	}
	return b.String()
}

// hostChar reports whether b can be part of a hostname (used for boundaries).
func hostChar(b byte) bool {
	return b == '-' || b == '.' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// ReplaceHost replaces whole-host occurrences of old with new in v (case-insensitive;
// "api.foo.zeabur.app" does not match inside "x-api.foo.zeabur.app" or "api.foo.zeabur.app.evil").
func ReplaceHost(v, old, new string) string {
	if old == "" || new == "" {
		return v
	}
	lv, lo := strings.ToLower(v), strings.ToLower(old)
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(lv[i:], lo)
		if j < 0 {
			b.WriteString(v[i:])
			return b.String()
		}
		j += i
		end := j + len(lo)
		before := j == 0 || !hostChar(lv[j-1])
		after := end == len(lv) || !hostChar(lv[end]) || (lv[end] == '.' && (end+1 == len(lv) || !hostChar(lv[end+1])))
		if before && after {
			b.WriteString(v[i:j])
			b.WriteString(new)
		} else {
			b.WriteString(v[i:end])
		}
		i = end
	}
}

// Rewrite replaces `<dns>.zeabur.internal` and `service-<id>` hostnames and mapped domains.
func (r Rewriter) Rewrite(v string) string {
	// longest hosts first so a.b.zeabur.app wins over b.zeabur.app
	olds := make([]string, 0, len(r.Domains))
	for o, n := range r.Domains {
		if n != "" {
			olds = append(olds, o)
		}
	}
	sort.Slice(olds, func(i, j int) bool { return len(olds[i]) > len(olds[j]) })
	for _, o := range olds {
		v = ReplaceHost(v, o, r.Domains[o])
	}
	v = r.rewriteServiceHosts(v)
	v = internalRe.ReplaceAllStringFunc(v, func(m string) string {
		sub := internalRe.FindStringSubmatch(m)
		if n, ok := r.ByDNSName[sub[1]]; ok {
			return n
		}
		return sub[1]
	})
	return serviceHostRe.ReplaceAllStringFunc(v, func(m string) string {
		id := m[len("service-"):]
		if n, ok := r.ByServiceID[id]; ok {
			return n
		}
		return m
	})
}

// IsGeneratedDomain reports whether host is a Zeabur-managed hostname that dies at shutdown.
func IsGeneratedDomain(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return strings.HasSuffix(h, ".zeabur.app") || strings.HasSuffix(h, ".onzeabur.com")
}

// DefaultDomainMap proposes a replacement for every generated (*.zeabur.app) host:
// "<same-prefix>.<baseDomain>" (prefix = the label(s) before .zeabur.app). overrides win;
// an override of "" means drop the host. Custom domains are not included (they stay).
func DefaultDomainMap(hosts []string, baseDomain string, overrides map[string]string) map[string]string {
	m := map[string]string{}
	base := strings.Trim(strings.ToLower(baseDomain), ".")
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSuffix(h, "."))
		if !IsGeneratedDomain(h) {
			continue
		}
		if base != "" {
			prefix := strings.TrimSuffix(strings.TrimSuffix(h, ".zeabur.app"), ".onzeabur.com")
			if i := strings.Index(prefix, "."); i >= 0 && strings.HasSuffix(h, ".onzeabur.com") {
				prefix = prefix[:i]
			}
			m[h] = SanitizeName(strings.ReplaceAll(prefix, ".", "-")) + "." + base
		} else {
			m[h] = ""
		}
	}
	for o, n := range overrides {
		m[strings.ToLower(o)] = strings.ToLower(n)
	}
	return m
}

// ---------- Zeabur built-in translation ----------

var dropKeys = map[string]bool{
	"ZEABUR": true, "ZEABUR_SERVICE_ID": true, "ZEABUR_PROJECT_ID": true, "ZEABUR_ENVIRONMENT_ID": true,
	"ZEABUR_USER_ID": true, "ZEABUR_REGION": true, "ZEABUR_ENVIRONMENT_NAME": true, "ZEABUR_PROJECT_NAME": true,
	"ZEABUR_SERVICE_NAME": true,
}

var zeaburURLRe = regexp.MustCompile(`^ZEABUR_([A-Z0-9_]+)_(URL|DOMAIN)$`)
var portForwardedRe = regexp.MustCompile(`^([A-Z0-9_]+)_PORT_FORWARDED_PORT$`)

// TranslateContext carries what translation needs to know about the new environment.
type TranslateContext struct {
	Service    string // atoll name of this service
	Rewriter   Rewriter
	ServerIPv4 string // target server public IP, for PORT_FORWARDED_HOSTNAME
}

// Translation is the result of TranslateEnv.
type Translation struct {
	Env         []api.EnvVar
	Dropped     []string       // ZEABUR_* identifiers removed
	PublicPorts map[string]int // port name (lowercase) → old NodePort, from <PORT>_PORT_FORWARDED_PORT
	Warnings    []string       // never contain values
}

// TranslateEnv rewrites effective Zeabur env for Atoll: drops platform identifiers, maps
// ZEABUR_<PORT>_URL/DOMAIN to Atoll's ${ATOLL_<PORT>_URL/DOMAIN}, CONTAINER_HOSTNAME and
// <SERVICE>_HOST to service names, rewrites .zeabur.internal and service-<id> hosts,
// and flags *.zeabur.app references.
func TranslateEnv(env []api.EnvVar, tc TranslateContext) Translation {
	t := Translation{PublicPorts: map[string]int{}}
	for _, e := range env {
		k := e.Key
		switch {
		case dropKeys[k] || strings.HasPrefix(k, "ZEABUR_GIT_"):
			t.Dropped = append(t.Dropped, k)
			continue
		case zeaburURLRe.MatchString(k):
			m := zeaburURLRe.FindStringSubmatch(k)
			e.Value = "${ATOLL_" + m[1] + "_" + m[2] + "}"
			t.Warnings = append(t.Warnings, fmt.Sprintf("%s: %s now points at the Atoll domain of port %s (was the Zeabur URL)", tc.Service, k, strings.ToLower(m[1])))
		case k == "CONTAINER_HOSTNAME":
			e.Value = tc.Service
		case k == "PORT_FORWARDED_HOSTNAME":
			if tc.ServerIPv4 != "" {
				e.Value = tc.ServerIPv4
			} else {
				t.Warnings = append(t.Warnings, fmt.Sprintf("%s: PORT_FORWARDED_HOSTNAME kept as-is; set it to the new server IP", tc.Service))
			}
		case portForwardedRe.MatchString(k):
			m := portForwardedRe.FindStringSubmatch(k)
			var p int
			if _, err := fmt.Sscan(e.Value, &p); err == nil && p > 0 {
				t.PublicPorts[strings.ToLower(m[1])] = p
			}
			t.Warnings = append(t.Warnings, fmt.Sprintf("%s: public port %s (%s) is kept public on the same number; add access control or remove it if no longer needed", tc.Service, strings.ToLower(m[1]), k))
		}
		// <SERVICE>_HOST values (service-<id> or <dns>.zeabur.internal) become service names here.
		e.Value = tc.Rewriter.Rewrite(e.Value)
		if strings.Contains(strings.ToLower(e.Value), ".zeabur.app") {
			t.Warnings = append(t.Warnings, fmt.Sprintf("%s: %s references a *.zeabur.app URL, which stops resolving after shutdown", tc.Service, k))
		}
		if refRe.MatchString(e.Value) && !strings.HasPrefix(e.Value, "${ATOLL_") {
			t.Warnings = append(t.Warnings, fmt.Sprintf("%s: %s still contains an unresolved ${...} reference", tc.Service, k))
		}
		e.Secret = e.Secret || IsSecret(k, e.Value)
		t.Env = append(t.Env, e)
	}
	return t
}

// SanitizeName turns a Zeabur service name into an Atoll service / DNS name.
func SanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	if out == "" {
		out = "service"
	}
	return out
}
