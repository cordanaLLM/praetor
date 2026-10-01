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

// tagPattern matches a version tag cut short at major ("v4") or major.minor ("v4.1")
// precision, the moving tags GitHub Actions publish beside exact releases.
var tagPattern = regexp.MustCompile(`^v?(0|[1-9]\d*)(?:\.(0|[1-9]\d*))?$`)

// Precision values ParseTag reports: how many of major, minor and patch a tag names.
const (
	PrecisionMajor = 1
	PrecisionMinor = 2
	PrecisionPatch = 3
)

// ParseTag parses s as a full SemVer version (precision PrecisionPatch) or as a tag that
// stops at major ("v4", PrecisionMajor) or major.minor ("v4.1", PrecisionMinor). Such a tag
// names a release line rather than one release, so callers compare it only at its own
// precision (see Truncate). ok is false for anything else, such as a commit SHA or "main".
func ParseTag(s string) (v Version, precision int, ok bool) {
	if full, ok := Parse(s); ok {
		return full, PrecisionPatch, true
	}
	m := tagPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, 0, false
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return Version{}, 0, false
	}
	if m[2] == "" {
		return Version{Major: major}, PrecisionMajor, true
	}
	minor, err := strconv.Atoi(m[2])
	if err != nil {
		return Version{}, 0, false
	}
	return Version{Major: major, Minor: minor}, PrecisionMinor, true
}

// Truncate cuts v to precision: PrecisionMajor keeps the major, PrecisionMinor the major
// and minor, and both drop the prerelease and build. PrecisionPatch or more returns v.
func (v Version) Truncate(precision int) Version {
	switch {
	case precision <= PrecisionMajor:
		return Version{Major: v.Major}
	case precision == PrecisionMinor:
		return Version{Major: v.Major, Minor: v.Minor}
	default:
		return v
	}
}

// IsPrerelease reports whether v carries a prerelease component (e.g. "0.2.0-rc.1").
func (v Version) IsPrerelease() bool {
	return v.Prerelease != ""
}

// comparatorOperators are the operators a node-semver range comparator may open with -- the
// grammar of npm package.json ranges and of actions/setup-go's go-version -- longest first so
// ">=" is not read as ">".
var comparatorOperators = [...]string{">=", "<=", "^", "~", ">", "<", "="}

// CutOperator splits the operator off the front of one range comparator such as "^1.2.3",
// ">=1.27" or "<2"; a bare version has no operator. rest is everything after the operator,
// unchecked, because the version syntax a caller accepts differs: npm specs need full SemVer,
// a Go toolchain pin may stop at major.minor.
func CutOperator(comparator string) (operator, rest string) {
	for _, candidate := range comparatorOperators {
		if after, found := strings.CutPrefix(comparator, candidate); found {
			return candidate, after
		}
	}
	return "", comparator
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
