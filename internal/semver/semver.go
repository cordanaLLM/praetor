// Package semver parses and orders SemVer 2.0.0 version strings. It is the repo's single
// implementation of SemVer precedence (HISS-19): callers that need to recognize or rank
// versions (release tagging, flavor tag resolution) share this package instead of each
// carrying its own regex or comparator.
package semver

import (
	"regexp"
	"strconv"
	"strings"
)

// pattern is the semver.org 2.0.0 grammar, with an optional leading "v" for git tag
// conventions (v1.2.3). Capture groups: major, minor, patch, prerelease, build.
var pattern = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

// maxPrereleaseIdentifiers is the scalar upper bound (HISS-02) on the dot-separated
// prerelease identifiers Compare walks.
const maxPrereleaseIdentifiers = 32

// Version is a parsed SemVer 2.0.0 version.
type Version struct {
	Major, Minor, Patch int
	// Prerelease is the dot-separated identifier string after "-", empty for a release
	// version.
	Prerelease string
	// Build is the metadata string after "+". It is carried for completeness but ignored
	// by Compare, as SemVer 2.0.0 requires.
	Build string
}

// Parse parses s (optionally "v"-prefixed, e.g. a git tag) as SemVer 2.0.0. ok is false
// when s does not match the grammar; callers use that to ignore non-SemVer strings rather
// than fail outright.
func Parse(s string) (Version, bool) {
	m := pattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, false
	}
	major, errMaj := strconv.Atoi(m[1])
	minor, errMin := strconv.Atoi(m[2])
	patch, errPatch := strconv.Atoi(m[3])
	if errMaj != nil || errMin != nil || errPatch != nil {
		return Version{}, false
	}
	return Version{Major: major, Minor: minor, Patch: patch, Prerelease: m[4], Build: m[5]}, true
}

// IsPrerelease reports whether v carries a prerelease component (e.g. "0.2.0-rc.1").
func (v Version) IsPrerelease() bool {
	return v.Prerelease != ""
}

// Compare returns -1, 0 or 1 as a orders before, equal to, or after b, following SemVer
// 2.0.0 precedence rules. Build metadata never affects precedence.
func Compare(a, b Version) int {
	if c := compareInt(a.Major, b.Major); c != 0 {
		return c
	}
	if c := compareInt(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := compareInt(a.Patch, b.Patch); c != 0 {
		return c
	}
	return comparePrerelease(a.Prerelease, b.Prerelease)
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// comparePrerelease implements SemVer 2.0.0 rule 11: a version without a prerelease
// outranks one with a prerelease; two prereleases compare identifier by identifier.
func comparePrerelease(a, b string) int {
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	default:
		return compareIdentifiers(strings.Split(a, "."), strings.Split(b, "."))
	}
}

// compareIdentifiers compares dot-separated prerelease identifiers left to right: numeric
// identifiers compare numerically, alphanumeric identifiers compare lexically, and a
// numeric identifier always has lower precedence than an alphanumeric one. A prerelease
// with more identifiers outranks one that is otherwise identical but shorter.
func compareIdentifiers(a, b []string) int {
	for i := 0; i < len(a) && i < len(b) && i < maxPrereleaseIdentifiers; i++ {
		if c := compareIdentifier(a[i], b[i]); c != 0 {
			return c
		}
	}
	return compareInt(len(a), len(b))
}

func compareIdentifier(a, b string) int {
	na, aNum := asNumeric(a)
	nb, bNum := asNumeric(b)
	switch {
	case aNum && bNum:
		return compareInt(na, nb)
	case aNum && !bNum:
		return -1
	case !aNum && bNum:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func asNumeric(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}
