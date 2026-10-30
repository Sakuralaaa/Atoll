// Adapted from zeabur-oss/zeabur (Apache-2.0), crates/zeabur-model/src/adapters/images.rs,
// crates/zeabur-model/src/kernel/data_store.rs and crates/zeabur-graph/src/connections.rs
// (data_store). Copyright Zeabur contributors; modified for atoll. See THIRD_PARTY_NOTICES.md.

package k8s

import "strings"

// DataStore is what kind of data store a service is, as far as its image's name (or its
// storage) tells. Image names are a convention, not a contract: use them for hints.
type DataStore string

const (
	StorePostgres DataStore = "postgres" // postgres, postgresql, cockroach
	StoreMysql    DataStore = "mysql"    // mysql, mariadb
	StoreRedis    DataStore = "redis"    // redis, valkey, keydb, memcached
	StoreMongo    DataStore = "mongo"    // mongo, mongodb
	StoreQueue    DataStore = "queue"    // rabbitmq, kafka, nats
	StoreSearch   DataStore = "search"   // elasticsearch, opensearch, clickhouse
	StoreOther    DataStore = "other"    // any other, or one known only by its storage
)

// dataStores maps image repository names (last path segment, no tag/digest) of widely used
// data store images to their kind.
var dataStores = map[string]DataStore{
	"cassandra": StoreOther, "clickhouse": StoreSearch, "clickhouse-server": StoreSearch,
	"cockroach": StorePostgres, "couchdb": StoreOther, "elasticsearch": StoreSearch,
	"etcd": StoreOther, "kafka": StoreQueue, "keydb": StoreRedis, "mariadb": StoreMysql,
	"memcached": StoreRedis, "minio": StoreOther, "mongo": StoreMongo, "mongodb": StoreMongo,
	"mysql": StoreMysql, "nats": StoreQueue, "opensearch": StoreSearch, "postgres": StorePostgres,
	"postgresql": StorePostgres, "rabbitmq": StoreQueue, "redis": StoreRedis, "valkey": StoreRedis,
}

// RepositoryName returns the last path segment of image without tag or digest:
// "ghcr.io/acme/shop-api:2.1" → "shop-api", "localhost:5000/redis:7" → "redis".
func RepositoryName(image string) string {
	last := image
	if i := strings.LastIndex(image, "/"); i >= 0 {
		last = image[i+1:]
	}
	if i := strings.IndexAny(last, "@:"); i >= 0 {
		last = last[:i]
	}
	return last
}

// DataStoreImage returns the kind of data store image is named like, or "".
func DataStoreImage(image string) DataStore {
	return dataStores[strings.ToLower(RepositoryName(image))]
}

// DataStoreOf decides whether a workload is a data store: the first container (init
// containers excluded) whose image is named like one decides the kind; otherwise a
// StatefulSet with a persistent mount (PVC or claim template) is an "other" store.
func DataStoreOf(w Object) DataStore {
	t := podTemplate(w)
	if t == nil {
		return ""
	}
	for _, c := range t.Spec.Containers {
		if ds := DataStoreImage(c.Image); ds != "" {
			return ds
		}
	}
	if w.Kind == "StatefulSet" {
		for _, c := range t.Spec.Containers {
			for _, m := range c.VolumeMounts {
				if persistentMount(w, m.Name) {
					return StoreOther
				}
			}
		}
	}
	return ""
}

// persistentMount reports whether the pod volume (or claim template) called name is a PVC.
func persistentMount(w Object, name string) bool {
	if t := podTemplate(w); t != nil {
		for _, v := range t.Spec.Volumes {
			if v.Name == name {
				return v.PersistentVolumeClaim != nil
			}
		}
	}
	for _, ct := range w.Spec.VolumeClaimTemplates {
		if ct.Metadata.Name == name {
			return true
		}
	}
	return false
}

// AtollKind maps a DataStore to the Atoll kind (which drives dump/restore) and role.
// Only engines Atoll can dump become a database kind; memcached (no persistence) and
// cockroach (pg wire-compatible but not pg_dump-compatible) stay "image".
func AtollKind(ds DataStore, image string) (kind, role string) {
	repo := strings.ToLower(RepositoryName(image))
	switch ds {
	case StorePostgres:
		if repo == "cockroach" {
			return "image", "database"
		}
		return "postgres", "database"
	case StoreMysql:
		return "mysql", "database"
	case StoreMongo:
		return "mongo", "database"
	case StoreRedis:
		if repo == "memcached" {
			return "image", "cache"
		}
		return "redis", "cache"
	case StoreSearch:
		return "image", "database"
	case StoreQueue:
		return "image", "other"
	case StoreOther:
		return "image", "database"
	}
	return "image", "app"
}
