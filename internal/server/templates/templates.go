// Package templates defines the one-click database services.
package templates

import (
	"crypto/rand"
	"math/big"
	"sort"
	"strings"

	"github.com/atoll-sh/atoll/internal/api"
)

func ev(k, v string, secret, exposed bool) api.EnvVar {
	return api.EnvVar{Key: k, Value: v, Secret: secret, Exposed: exposed}
}

// HostPlaceholder is replaced by the service name when the template is instantiated.
const HostPlaceholder = "{{SERVICE_NAME}}"

var all = map[string]api.Template{
	"postgres": {
		Kind: "postgres", Name: "PostgreSQL", Description: "PostgreSQL 16",
		Image:   "postgres:16-alpine",
		Ports:   []api.Port{{Name: "database", ContainerPort: 5432, Protocol: "tcp"}},
		Volumes: []api.Volume{{Name: "data", MountPath: "/var/lib/postgresql/data"}},
		Env: []api.EnvVar{
			ev("PASSWORD", "${PASSWORD}", true, false),
			ev("POSTGRES_HOST", HostPlaceholder, false, true),
			ev("POSTGRES_PORT", "5432", false, true),
			ev("POSTGRES_USERNAME", "root", false, true),
			ev("POSTGRES_PASSWORD", "${PASSWORD}", true, true),
			ev("POSTGRES_DATABASE", "atoll", false, true),
			ev("POSTGRES_CONNECTION_STRING", "postgresql://${POSTGRES_USERNAME}:${POSTGRES_PASSWORD}@${POSTGRES_HOST}:${POSTGRES_PORT}/${POSTGRES_DATABASE}", true, true),
			ev("POSTGRES_USER", "${POSTGRES_USERNAME}", false, false),
			ev("POSTGRES_DB", "${POSTGRES_DATABASE}", false, false),
			ev("PGDATA", "/var/lib/postgresql/data/pgdata", false, false),
		},
	},
	"mysql": {
		Kind: "mysql", Name: "MySQL", Description: "MySQL 8.4",
		Image:   "mysql:8.4",
		Ports:   []api.Port{{Name: "database", ContainerPort: 3306, Protocol: "tcp"}},
		Volumes: []api.Volume{{Name: "data", MountPath: "/var/lib/mysql"}},
		Env: []api.EnvVar{
			ev("PASSWORD", "${PASSWORD}", true, false),
			ev("MYSQL_HOST", HostPlaceholder, false, true),
			ev("MYSQL_PORT", "3306", false, true),
			ev("MYSQL_USERNAME", "root", false, true),
			ev("MYSQL_PASSWORD", "${PASSWORD}", true, true),
			ev("MYSQL_DATABASE", "atoll", false, true),
			ev("MYSQL_CONNECTION_STRING", "mysql://${MYSQL_USERNAME}:${MYSQL_PASSWORD}@${MYSQL_HOST}:${MYSQL_PORT}/${MYSQL_DATABASE}", true, true),
			ev("MYSQL_ROOT_PASSWORD", "${PASSWORD}", true, false),
		},
	},
	"redis": {
		Kind: "redis", Name: "Redis", Description: "Redis 7 with AOF persistence",
		Image:   "redis:7-alpine",
		Command: []string{"redis-server", "--requirepass", "${PASSWORD}", "--appendonly", "yes"},
		Ports:   []api.Port{{Name: "database", ContainerPort: 6379, Protocol: "tcp"}},
		Volumes: []api.Volume{{Name: "data", MountPath: "/data"}},
		Env: []api.EnvVar{
			ev("PASSWORD", "${PASSWORD}", true, false),
			ev("REDIS_HOST", HostPlaceholder, false, true),
			ev("REDIS_PORT", "6379", false, true),
			ev("REDIS_PASSWORD", "${PASSWORD}", true, true),
			ev("REDIS_URI", "redis://:${REDIS_PASSWORD}@${REDIS_HOST}:${REDIS_PORT}", true, true),
			ev("REDIS_CONNECTION_STRING", "${REDIS_URI}", true, true),
		},
	},
	"mongo": {
		Kind: "mongo", Name: "MongoDB", Description: "MongoDB 7",
		Image:   "mongo:7",
		Ports:   []api.Port{{Name: "database", ContainerPort: 27017, Protocol: "tcp"}},
		Volumes: []api.Volume{{Name: "data", MountPath: "/data/db"}},
		Env: []api.EnvVar{
			ev("PASSWORD", "${PASSWORD}", true, false),
			ev("MONGO_HOST", HostPlaceholder, false, true),
			ev("MONGO_PORT", "27017", false, true),
			ev("MONGO_USERNAME", "root", false, true),
			ev("MONGO_PASSWORD", "${PASSWORD}", true, true),
			ev("MONGO_URI", "mongodb://${MONGO_USERNAME}:${MONGO_PASSWORD}@${MONGO_HOST}:${MONGO_PORT}", true, true),
			ev("MONGO_CONNECTION_STRING", "${MONGO_URI}", true, true),
			ev("MONGO_INITDB_ROOT_USERNAME", "${MONGO_USERNAME}", false, false),
			ev("MONGO_INITDB_ROOT_PASSWORD", "${PASSWORD}", true, false),
		},
	},
}

// List returns all templates sorted by kind.
func List() []api.Template {
	out := make([]api.Template, 0, len(all))
	for _, t := range all {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// Get returns the template for kind.
func Get(kind string) (api.Template, bool) {
	t, ok := all[kind]
	return t, ok
}

// Instantiate returns the env for a new service of this template: PASSWORD
// becomes a fresh random value and the host placeholder becomes serviceName.
func Instantiate(t api.Template, serviceName string) []api.EnvVar {
	pw := RandomPassword(32)
	out := make([]api.EnvVar, len(t.Env))
	for i, e := range t.Env {
		if e.Key == "PASSWORD" {
			e.Value = pw
		}
		e.Value = strings.ReplaceAll(e.Value, HostPlaceholder, serviceName)
		out[i] = e
	}
	return out
}

const alnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// RandomPassword returns n random alphanumeric chars (URL-safe).
func RandomPassword(n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alnum)))
	for i := range b {
		x, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b[i] = alnum[x.Int64()]
	}
	return string(b)
}
