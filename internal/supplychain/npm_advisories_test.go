package supplychain

// The Markdown gate's npm lock is written byte for byte into every adopting repository, and
// audit holds it there, so an adopter cannot patch an advisory out of it: the fix has to ship
// as a new Praetor text. These tests keep the packages whose advisories the lock once carried
// at or above their fixed versions, reading the lock through lockRuntimePackages, the read the
// notices render takes.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/semver"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
)

// npmAdvisoryFloors names each package of the Markdown gate's lock that had a security
// advisory fixed, with the first version on the installed line free of it: smol-toml
// GHSA-7w5x-hrqm-74c2 (<= 1.7.0), js-yaml GHSA-r3ph-w7gj-g6xm (5.0.0 to 5.4.0) and markdown-it
// GHSA-253c-mchw-3w2r (< 14.3.1 and 15.0.0). The lock installed all three affected versions
// through markdownlint-cli2 0.23.2 (#643).
var npmAdvisoryFloors = map[string]string{
	"smol-toml":   "1.7.1",
	"js-yaml":     "5.4.1",
	"markdown-it": "15.0.1",
}

// advisoryFloorViolations lists every installed name@version of a floor package that orders
// before its floor or is not SemVer (a floor that is not SemVer fails every version), sorted
// for stable reports.
func advisoryFloorViolations(installed []noticeRow, floors map[string]string) []string {
	var violations []string
	for _, row := range installed {
		floor, listed := floors[row.name]
		if !listed {
			continue
		}
		fixed, floorOK := semver.Parse(floor)
		version, versionOK := semver.Parse(row.version)
		if !floorOK || !versionOK || semver.Compare(version, fixed) < 0 {
			violations = append(violations, row.name+" "+row.version+" is below "+floor)
		}
	}
	slices.Sort(violations)
	return violations
}

func mustLockRuntimePackages(t *testing.T, lock []byte) []noticeRow {
	t.Helper()
	rows, err := lockRuntimePackages(lock)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// Positive: the embedded lock installs every floor package, each at or above its floor.
func TestMarkdownGateLockClearsFixedAdvisories(t *testing.T) {
	lock, err := markdownassets.Read("package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	rows := mustLockRuntimePackages(t, lock)
	for name := range npmAdvisoryFloors {
		if !slices.ContainsFunc(rows, func(row noticeRow) bool { return row.name == name }) {
			t.Fatalf("the lock no longer installs %s; drop its advisory floor", name)
		}
	}
	if violations := advisoryFloorViolations(rows, npmAdvisoryFloors); len(violations) != 0 {
		t.Fatalf("the lock installs versions with fixed advisories: %v", violations)
	}
}

// Negative: the lock shipped before #643 fails on each of the three affected packages.
func TestMarkdownGateLockAdvisoryFloorsRejectPriorLock(t *testing.T) {
	prior := filepath.Join("..", "..", markdownassets.Directory, "testdata", "prior",
		"package-lock.markdownlint-cli2-0.23.2.json")
	lock, err := os.ReadFile(prior)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"js-yaml 5.2.2 is below 5.4.1",
		"markdown-it 14.3.0 is below 15.0.1",
		"smol-toml 1.7.0 is below 1.7.1",
	}
	got := advisoryFloorViolations(mustLockRuntimePackages(t, lock), npmAdvisoryFloors)
	if !slices.Equal(got, want) {
		t.Fatalf("violations = %v, want %v", got, want)
	}
}

// Boundary: exactly the floor passes, one patch below fails, a version that is not SemVer
// fails, a package whose name only starts with a floor name is not read, and an empty lock
// reports nothing.
func TestMarkdownGateLockAdvisoryFloorsBoundary(t *testing.T) {
	floors := map[string]string{"smol-toml": "1.7.1"}
	installed := []noticeRow{
		{name: "smol-toml", version: "1.7.1"},
		{name: "smol-toml", version: "1.7.0"},
		{name: "smol-toml", version: "latest"},
		{name: "smol-toml-extra", version: "0.0.1"},
	}
	want := []string{"smol-toml 1.7.0 is below 1.7.1", "smol-toml latest is below 1.7.1"}
	if got := advisoryFloorViolations(installed, floors); !slices.Equal(got, want) {
		t.Fatalf("violations = %v, want %v", got, want)
	}
	if got := advisoryFloorViolations(nil, floors); len(got) != 0 {
		t.Fatalf("an empty lock reported %v", got)
	}
	nested := []byte(`{"packages": {"": {"name": "root", "version": "1.0.0"},
		"node_modules/a/node_modules/smol-toml": {"version": "1.7.0"}}}`)
	got := advisoryFloorViolations(mustLockRuntimePackages(t, nested), floors)
	if want := []string{"smol-toml 1.7.0 is below 1.7.1"}; !slices.Equal(got, want) {
		t.Fatalf("nested copy: violations = %v, want %v", got, want)
	}
}
