// Adapted from zeabur-oss/zeabur (Apache-2.0), crates/zeabur-gaps/src/durability.rs
// (ephemeral-data rule). Copyright Zeabur contributors; modified for atoll (warning text in
// Chinese, tied to the migration). See THIRD_PARTY_NOTICES.md.

package k8s

import (
	"fmt"
	"strconv"
	"strings"
)

// A container's own filesystem lasts as long as the container, and an emptyDir as long as the
// pod; only a PersistentVolumeClaim outlives it. Image configurations are not read, so the
// data directories of the Docker official images are known by repository name. Images from
// other publishers (bitnami/postgresql) keep data elsewhere and are not judged.

// DataDirectory returns the software and the data directory of an official image, or "", "".
// postgres 18+ (and untagged/latest) keeps data in /var/lib/postgresql, earlier majors in
// /var/lib/postgresql/data.
func DataDirectory(image string) (software, dir string) {
	path, tag := image, ""
	if i := strings.LastIndex(image, ":"); i >= 0 && !strings.Contains(image[i+1:], "/") {
		path, tag = image[:i], image[i+1:]
	}
	if i := strings.Index(path, "@"); i >= 0 {
		path = path[:i]
	}
	official := func(name string) bool {
		switch path {
		case name, "library/" + name, "docker.io/" + name, "docker.io/library/" + name:
			return true
		}
		return false
	}
	if official("postgres") {
		majorStr := strings.FieldsFunc(tag, func(r rune) bool { return r == '.' || r == '-' })
		if len(majorStr) > 0 {
			if m, err := strconv.Atoi(majorStr[0]); err == nil && m < 18 {
				return "postgres", "/var/lib/postgresql/data"
			}
		}
		return "postgres", "/var/lib/postgresql"
	}
	for _, c := range [][2]string{{"mysql", "/var/lib/mysql"}, {"mariadb", "/var/lib/mysql"}, {"mongo", "/data/db"}, {"redis", "/data"}, {"rabbitmq", "/var/lib/rabbitmq"}} {
		if official(c[0]) {
			return c[0], c[1]
		}
	}
	if path == "valkey/valkey" {
		return "valkey", "/data"
	}
	return "", ""
}

// pathsOverlap reports whether one of a and b is the other or a directory above it.
func pathsOverlap(a, b string) bool {
	a, b = strings.TrimRight(a, "/"), strings.TrimRight(b, "/")
	within := func(inner, outer string) bool {
		return inner == outer || outer == "" || strings.HasPrefix(inner, outer+"/")
	}
	return within(a, b) || within(b, a)
}

// mountCategory classifies the pod volume (or claim template) a mount refers to.
func mountCategory(w Object, name string) string {
	if t := podTemplate(w); t != nil {
		for _, v := range t.Spec.Volumes {
			if v.Name != name {
				continue
			}
			switch {
			case v.PersistentVolumeClaim != nil:
				return "persistent"
			case v.HostPath != nil:
				// atoll addition: a hostPath outlives the pod on a single-node k3s.
				return "persistent"
			case v.ConfigMap != nil, v.Secret != nil, v.Projected != nil, v.DownwardAPI != nil:
				return "config"
			default:
				return "ephemeral"
			}
		}
	}
	for _, ct := range w.Spec.VolumeClaimTemplates {
		if ct.Metadata.Name == name {
			return "persistent"
		}
	}
	return "ephemeral"
}

// DurabilityWarnings checks every regular container of a workload whose official image has a
// known data directory: when no persistent mount is at, above or below it (a mount below may
// not hold all of it, but "nothing is kept" would then be wrong), a warning is returned.
func DurabilityWarnings(w Object) []string {
	t := podTemplate(w)
	if t == nil {
		return nil
	}
	var out []string
	for _, c := range t.Spec.Containers {
		software, dir := DataDirectory(c.Image)
		if dir == "" {
			continue
		}
		if software == "postgres" {
			// atoll addition: the official image honours PGDATA.
			for _, e := range c.Env {
				if e.Name == "PGDATA" && e.ValueFrom == nil && strings.HasPrefix(e.Value, "/") {
					dir = e.Value
				}
			}
		}
		kept := ""
		for _, m := range c.VolumeMounts {
			if !pathsOverlap(m.MountPath, dir) {
				continue
			}
			switch cat := mountCategory(w, m.Name); cat {
			case "persistent":
				kept = cat
			case "ephemeral":
				if kept == "" {
					kept = cat
				}
			}
		}
		if kept == "persistent" {
			continue
		}
		where := "未挂载任何卷"
		if kept == "ephemeral" {
			where = "只在 emptyDir 临时卷上"
		}
		msg := fmt.Sprintf("容器 %s（%s）的数据目录 %s 不在持久卷上（%s），容器重建会丢数据；迁移前必须用 dump 导出", c.Name, software, dir, where)
		if software == "redis" || software == "valkey" {
			msg += "（若仅作缓存可忽略）"
		}
		out = append(out, msg)
	}
	return out
}
