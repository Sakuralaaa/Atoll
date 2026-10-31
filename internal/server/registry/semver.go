package registry

import (
	"strconv"
	"strings"
)

// Version is a semver-like image tag: an optional "v", 1-3 numeric components and an
// optional suffix ("-alpine", "-rc1", "-bookworm"). Tags are only compared with tags of
// the same shape (same prefix, component count and suffix), so "1.27-alpine" never
// "upgrades" to "1.28" or "1.28-rc1".
type Version struct {
	Prefix string // "v" or ""
	Nums   []int
	Suffix string // includes the leading '-' or '.'/'_' separator
}

// ParseVersion parses tag as a Version; ok=false for non-version tags (latest, main, sha-abc).
func ParseVersion(tag string) (Version, bool) {
	var v Version
	s := tag
	if strings.HasPrefix(s, "v") || strings.HasPrefix(s, "V") {
		v.Prefix, s = s[:1], s[1:]
	}
	i := 0
	for len(v.Nums) < 3 {
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == i || j-i > 9 {
			return Version{}, false
		}
		n, _ := strconv.Atoi(s[i:j])
		v.Nums = append(v.Nums, n)
		i = j
		if i < len(s) && s[i] == '.' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' && len(v.Nums) < 3 {
			i++
			continue
		}
		break
	}
	v.Suffix = s[i:]
	if v.Suffix != "" && v.Suffix[0] != '-' && v.Suffix[0] != '_' && v.Suffix[0] != '+' {
		return Version{}, false // e.g. "1.2.3.4" or "2024a"
	}
	// a single huge number is a date/build id, not a version ("20240101")
	if len(v.Nums) == 1 && v.Nums[0] > 9999 {
		return Version{}, false
	}
	return v, true
}

// sameShape reports whether a and b are comparable tag variants.
func sameShape(a, b Version) bool {
	return a.Prefix == b.Prefix && len(a.Nums) == len(b.Nums) && a.Suffix == b.Suffix
}

// Compare returns -1, 0, 1 comparing numeric components.
func Compare(a, b Version) int {
	for i := 0; i < len(a.Nums) && i < len(b.Nums); i++ {
		if a.Nums[i] != b.Nums[i] {
			if a.Nums[i] < b.Nums[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.Nums) < len(b.Nums):
		return -1
	case len(a.Nums) > len(b.Nums):
		return 1
	}
	return 0
}

// NewerTags finds, among tags, the newest version higher than current with the same
// major version (sameMajor) and the newest with a higher major (major). Both are "" when
// current is not a version tag or nothing newer exists. A single-component tag ("16")
// floats within its major, so only major bumps are reported for it.
func NewerTags(current string, tags []string) (sameMajor, major string) {
	cv, ok := ParseVersion(current)
	if !ok {
		return "", ""
	}
	var bestSame, bestMajor *Version
	for _, t := range tags {
		v, ok := ParseVersion(t)
		if !ok || !sameShape(cv, v) || Compare(v, cv) <= 0 {
			continue
		}
		if v.Nums[0] == cv.Nums[0] {
			if len(cv.Nums) > 1 && (bestSame == nil || Compare(v, *bestSame) > 0) {
				bestSame, sameMajor = &v, t
			}
		} else if bestMajor == nil || Compare(v, *bestMajor) > 0 {
			bestMajor, major = &v, t
		}
	}
	return sameMajor, major
}
