// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package managedasset

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
)

// maxRangeBranches bounds evaluation of disjunctive ranges (HISS-02).
const maxRangeBranches = 16

// maxLockedPackages bounds iteration over locked package entries (HISS-02).
const maxLockedPackages = 8192

// maxPresetScanDirs bounds the scan of preset directories (HISS-02).
const maxPresetScanDirs = 32

var semverPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,2}$`)

// optionalPlatformExceptions names optional platform binaries a preset may go without on its
// declared Node floor, each with the reason. npm skips an optional package whose engines do not
// match, so only that platform lacks the binary; every other optional package is checked like a
// required one, because a platform binary raising its floor breaks every adopter on that platform.
var optionalPlatformExceptions = map[string]string{
	"node_modules/@img/sharp-win32-ia32": "sharp publishes its 32-bit Windows binary for Node ^20.9.0 only; " +
		"npm skips it on Node 22, so sharp is unavailable on win32-ia32 alone",
}

type presetManifest struct {
	Engines struct {
		Node string `json:"node"`
	} `json:"engines"`
}

type presetPackageLock struct {
	Packages map[string]struct {
		Optional bool `json:"optional"`
		Engines  struct {
			Node string `json:"node"`
		} `json:"engines"`
	} `json:"packages"`
}

// PresetFloorViolation records a locked package requiring a newer Node than declared
// or a range that does not admit the declared Node floor.
type PresetFloorViolation struct {
	Package  string
	Range    string
	Required markdownassets.NodeVersion
	Declared markdownassets.NodeVersion
}

// Error names the package, its range and the declared floor it fails.
func (v PresetFloorViolation) Error() string {
	if v.Required.Compare(v.Declared) > 0 {
		return fmt.Sprintf("locked package %s requires node %s (minimum %s), exceeding declared floor %s",
			v.Package, v.Range, v.Required, v.Declared)
	}
	return fmt.Sprintf("locked package %s requires node %s, which does not admit declared floor %s",
		v.Package, v.Range, v.Declared)
}

// parseBranchVersion extracts and validates the semver string from an allowed branch form:
// >= (optional spaces) X[.Y[.Z]], ^X[.Y[.Z]], or bare X[.Y[.Z]].
func parseBranchVersion(b string) (prefix string, v markdownassets.NodeVersion, err error) {
	if strings.HasPrefix(b, ">=") {
		rest := strings.TrimSpace(b[2:])
		if !semverPattern.MatchString(rest) {
			return "", markdownassets.NodeVersion{}, errors.New("unsupported or malformed range")
		}
		ver, err := markdownassets.ParseNodeVersion(rest)
		return ">=", ver, err
	}
	if strings.HasPrefix(b, "^") {
		rest := b[1:]
		if !semverPattern.MatchString(rest) {
			return "", markdownassets.NodeVersion{}, errors.New("unsupported or malformed range")
		}
		ver, err := markdownassets.ParseNodeVersion(rest)
		return "^", ver, err
	}
	if !semverPattern.MatchString(b) {
		return "", markdownassets.NodeVersion{}, errors.New("unsupported or malformed range")
	}
	ver, err := markdownassets.ParseNodeVersion(b)
	return "", ver, err
}

// branchFloor evaluates a single range branch. It enforces an allow-list of exactly
// >= (optional spaces) X[.Y[.Z]], ^X[.Y[.Z]], and bare X[.Y[.Z]]. Any other form returns
// an error naming the package and full range. If the branch admits declaredFloor, it returns
// the floor bound and true; otherwise it returns the floor bound and false.
func branchFloor(pkgName, fullRange, branch string, declaredFloor markdownassets.NodeVersion) (*markdownassets.NodeVersion, bool, error) {
	b := strings.TrimSpace(branch)
	prefix, v, err := parseBranchVersion(b)
	if err != nil {
		return nil, false, fmt.Errorf("package %s engines.node %q: unsupported range %q", pkgName, fullRange, b)
	}
	if prefix == ">=" {
		return &v, declaredFloor.Compare(v) >= 0, nil
	}
	upper := markdownassets.NodeVersion{Major: v.Major + 1, Minor: 0, Patch: 0}
	admits := declaredFloor.Compare(v) >= 0 && declaredFloor.Compare(upper) < 0
	return &v, admits, nil
}

// packageRequiredFloor evaluates all branches in spec against declaredFloor. If at least
// one branch admits declaredFloor, it returns the lowest bound of admitting branches.
// It also returns the highest lower bound across all valid branches in highestBound.
func packageRequiredFloor(pkgName, spec string, declaredFloor markdownassets.NodeVersion) (best *markdownassets.NodeVersion, highestBound markdownassets.NodeVersion, err error) {
	branches := strings.Split(spec, "||")
	n := min(len(branches), maxRangeBranches)
	for i := 0; i < n; i++ {
		floor, ok, bErr := branchFloor(pkgName, spec, branches[i], declaredFloor)
		if bErr != nil {
			return nil, markdownassets.NodeVersion{}, bErr
		}
		if floor != nil && floor.Compare(highestBound) > 0 {
			highestBound = *floor
		}
		if ok && (best == nil || floor.Compare(*best) > 0) {
			best = floor
		}
	}
	return best, highestBound, nil
}

// declaredPresetFloor reads the engines.node minimum a preset package.json declares.
func declaredPresetFloor(manifestData []byte) (markdownassets.NodeVersion, error) {
	var manifest presetManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return markdownassets.NodeVersion{}, fmt.Errorf("decode preset package.json: %w", err)
	}
	if strings.TrimSpace(manifest.Engines.Node) == "" {
		return markdownassets.NodeVersion{}, errors.New("preset package.json lacks engines.node field")
	}
	floor, err := markdownassets.ParseNodeFloor(manifest.Engines.Node)
	if err != nil {
		return markdownassets.NodeVersion{}, fmt.Errorf("invalid declared engines.node: %w", err)
	}
	return floor, nil
}

// lockedPackageNames returns the sorted package keys of a preset lock, the root entry left out.
func lockedPackageNames(lock presetPackageLock) []string {
	keys := make([]string, 0, len(lock.Packages))
	for k := range lock.Packages {
		if k != "" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

// judgeLockedPackage returns the floor a locked package needs on declaredFloor, or the
// violation when no branch of its range admits declaredFloor. A package without engines.node,
// or an optional one named in optionalPlatformExceptions, needs nothing (nil, nil, nil).
func judgeLockedPackage(lock presetPackageLock, pkgName string, declaredFloor markdownassets.NodeVersion) (*markdownassets.NodeVersion, *PresetFloorViolation, error) {
	entry := lock.Packages[pkgName]
	if entry.Optional && optionalPlatformExceptions[pkgName] != "" {
		return nil, nil, nil
	}
	spec := strings.TrimSpace(entry.Engines.Node)
	if spec == "" {
		return nil, nil, nil
	}
	req, highestBound, err := packageRequiredFloor(pkgName, spec, declaredFloor)
	if err != nil || req != nil {
		return req, nil, err
	}
	return nil, &PresetFloorViolation{Package: pkgName, Range: spec, Required: highestBound, Declared: declaredFloor}, nil
}

// CheckPresetNodeFloor verifies that the declared engines.node minimum in manifestData
// is at least the highest plain minimum or admitting branch bound in lockData.
func CheckPresetNodeFloor(manifestData, lockData []byte) (*markdownassets.NodeVersion, error) {
	declaredFloor, err := declaredPresetFloor(manifestData)
	if err != nil {
		return nil, err
	}
	var lock presetPackageLock
	if err := json.Unmarshal(lockData, &lock); err != nil {
		return nil, fmt.Errorf("decode preset package-lock.json: %w", err)
	}
	keys := lockedPackageNames(lock)
	var highestLocked markdownassets.NodeVersion
	var worstViolation *PresetFloorViolation
	n := min(len(keys), maxLockedPackages)
	for i := 0; i < n; i++ {
		req, violation, err := judgeLockedPackage(lock, keys[i], declaredFloor)
		if err != nil {
			return nil, err
		}
		if violation != nil && (worstViolation == nil || violation.Required.Compare(worstViolation.Required) > 0) {
			worstViolation = violation
		}
		if req != nil && req.Compare(highestLocked) > 0 {
			highestLocked = *req
		}
	}
	if worstViolation != nil {
		return &highestLocked, worstViolation
	}
	return &highestLocked, nil
}

// Positive: each preset package.json with a package-lock.json declares an engines.node floor
// that satisfies the highest minimum required across its locked dependency tree.
func TestPresetNodeFloorMatchesLockedDependencies(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	presetsDir := filepath.Join(repoRoot, "docs", "presets")
	entries, err := os.ReadDir(presetsDir)
	if err != nil {
		t.Fatalf("read docs/presets: %v", err)
	}
	verified := 0
	n := min(len(entries), maxPresetScanDirs)
	for i := 0; i < n; i++ {
		if !entries[i].IsDir() {
			continue
		}
		presetPath := filepath.Join(presetsDir, entries[i].Name())
		manifestPath := filepath.Join(presetPath, "package.json")
		lockPath := filepath.Join(presetPath, "package-lock.json")

		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			continue // preset has no package.json (e.g. mkdocs)
		}
		lockData, err := os.ReadFile(lockPath)
		if err != nil {
			continue // preset has no package-lock.json
		}

		highest, err := CheckPresetNodeFloor(manifestData, lockData)
		if err != nil {
			t.Fatalf("preset %s floor check failed: %v", entries[i].Name(), err)
		}
		verified++
		t.Logf("preset %s: declared floor satisfies highest locked minimum %s", entries[i].Name(), highest)
	}
	if verified == 0 {
		t.Fatal("no documentation preset with package-lock.json was checked")
	}
}

// Negative: a planted locked package requiring a newer Node than declared fails, naming
// both the offending package and its range.
func TestPresetNodeFloorRejectsHigherLockedDependency(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	starlightDir := filepath.Join(repoRoot, "docs", "presets", "starlight")
	manifestData, err := os.ReadFile(filepath.Join(starlightDir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock presetPackageLock
	lockData, err := os.ReadFile(filepath.Join(starlightDir, "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(lockData, &lock); err != nil {
		t.Fatal(err)
	}
	// Plant a package needing >=99.0.0
	const plantedPkg = "node_modules/planted-future-pkg"
	const plantedRange = ">=99.0.0"
	lock.Packages[plantedPkg] = struct {
		Optional bool `json:"optional"`
		Engines  struct {
			Node string `json:"node"`
		} `json:"engines"`
	}{
		Engines: struct {
			Node string `json:"node"`
		}{Node: plantedRange},
	}
	tamperedLock, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	_, err = CheckPresetNodeFloor(manifestData, tamperedLock)
	if err == nil {
		t.Fatal("planted >=99.0.0 package requirement was accepted")
	}
	var violation *PresetFloorViolation
	if !errors.As(err, &violation) {
		t.Fatalf("expected PresetFloorViolation, got %T: %v", err, err)
	}
	if violation.Package != plantedPkg {
		t.Fatalf("violation package = %q, want %q", violation.Package, plantedPkg)
	}
	if violation.Range != plantedRange {
		t.Fatalf("violation range = %q, want %q", violation.Range, plantedRange)
	}
	if !strings.Contains(err.Error(), plantedPkg) || !strings.Contains(err.Error(), plantedRange) {
		t.Fatalf("error message %q does not name package %q and range %q", err.Error(), plantedPkg, plantedRange)
	}
}

// Boundary: a declared floor exactly equal to the highest locked minimum passes.
func TestPresetNodeFloorBoundaryEqualPasses(t *testing.T) {
	manifest := []byte(`{"name":"preset","engines":{"node":">=22.23.4"}}`)
	lock := []byte(`{
		"packages": {
			"": {},
			"node_modules/astro": {"engines":{"node":">=22.23.4"}}
		}
	}`)
	highest, err := CheckPresetNodeFloor(manifest, lock)
	if err != nil {
		t.Fatalf("exact floor boundary failed: %v", err)
	}
	if highest == nil || highest.String() != "22.23.4" {
		t.Fatalf("highest = %v, want 22.23.4", highest)
	}
}

// Boundary: a preset manifest missing the engines field fails closed.
func TestPresetNodeFloorBoundaryMissingEnginesFails(t *testing.T) {
	manifestWithoutEngines := []byte(`{"name":"preset"}`)
	lock := []byte(`{
		"packages": {
			"": {},
			"node_modules/astro": {"engines":{"node":">=22.23.4"}}
		}
	}`)
	_, err := CheckPresetNodeFloor(manifestWithoutEngines, lock)
	if err == nil {
		t.Fatal("missing engines field was accepted")
	}
	if !strings.Contains(err.Error(), "lacks engines.node") {
		t.Fatalf("error %q does not describe missing engines.node", err.Error())
	}
}

// Negative: a lone ^20.19.0 against declared >=22.23.4 fails closed, naming the package.
func TestPresetNodeFloorRejectsLoneCaretMismatch(t *testing.T) {
	manifest := []byte(`{"name":"preset","engines":{"node":">=22.23.4"}}`)
	const pkg = "node_modules/planted-caret-pkg"
	const spec = "^20.19.0"
	lock := []byte(fmt.Sprintf(`{
		"packages": {
			"": {},
			%q: {"engines":{"node":%q}}
		}
	}`, pkg, spec))
	_, err := CheckPresetNodeFloor(manifest, lock)
	if err == nil {
		t.Fatal("planted lone ^20.19.0 package requirement was accepted against declared >=22.23.4")
	}
	if !strings.Contains(err.Error(), pkg) {
		t.Fatalf("error %q does not name package %q", err.Error(), pkg)
	}
	var violation *PresetFloorViolation
	if !errors.As(err, &violation) {
		t.Fatalf("expected PresetFloorViolation, got %T: %v", err, err)
	}
	if violation.Package != pkg {
		t.Fatalf("violation package = %q, want %q", violation.Package, pkg)
	}
	if violation.Range != spec {
		t.Fatalf("violation range = %q, want %q", violation.Range, spec)
	}
}

// Negative: ~18.0.0 fails as an unsupported form, naming the package and full range.
func TestPresetNodeFloorRejectsUnsupportedRangeForm(t *testing.T) {
	manifest := []byte(`{"name":"preset","engines":{"node":">=22.23.4"}}`)
	const pkg = "node_modules/planted-tilde-pkg"
	const spec = "~18.0.0"
	lock := []byte(fmt.Sprintf(`{
		"packages": {
			"": {},
			%q: {"engines":{"node":%q}}
		}
	}`, pkg, spec))
	_, err := CheckPresetNodeFloor(manifest, lock)
	if err == nil {
		t.Fatal("unsupported ~18.0.0 package requirement was accepted")
	}
	if !strings.Contains(err.Error(), pkg) || !strings.Contains(err.Error(), spec) {
		t.Fatalf("error %q does not name package %q and range %q", err.Error(), pkg, spec)
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error %q does not indicate unsupported range", err.Error())
	}
}

// Positive: ^20.19.0 || ^22.13.0 || >=23.5.0 against >=22.23.4 passes with required 22.13.0.
func TestPresetNodeFloorDisjunctionAdmittingBranchPasses(t *testing.T) {
	manifest := []byte(`{"name":"preset","engines":{"node":">=22.23.4"}}`)
	const pkg = "node_modules/multi-branch-pkg"
	const spec = "^20.19.0 || ^22.13.0 || >=23.5.0"
	lock := []byte(fmt.Sprintf(`{
		"packages": {
			"": {},
			%q: {"engines":{"node":%q}}
		}
	}`, pkg, spec))
	highest, err := CheckPresetNodeFloor(manifest, lock)
	if err != nil {
		t.Fatalf("multi-branch admitting declared floor failed: %v", err)
	}
	if highest == nil || highest.String() != "22.13.0" {
		t.Fatalf("highest = %v, want 22.13.0", highest)
	}
}

// Negative: an optional platform binary outside optionalPlatformExceptions is checked like a
// required package, so one that needs a newer Node than declared fails, naming the package.
// Boundary: the one named exception is skipped, and only while it is optional.
func TestPresetNodeFloorOptionalPlatformBinaries(t *testing.T) {
	manifest := []byte(`{"name":"preset","engines":{"node":">=22.23.4"}}`)
	const planted = "node_modules/@astrojs/compiler-binding-linux-x64-gnu"
	lock := []byte(fmt.Sprintf(`{"packages": {"": {}, %q: {"optional": true, "engines":{"node":">=99.0.0"}}}}`, planted))
	_, err := CheckPresetNodeFloor(manifest, lock)
	var violation *PresetFloorViolation
	if !errors.As(err, &violation) || violation.Package != planted {
		t.Fatalf("optional %s needing >=99.0.0 was not reported: %v", planted, err)
	}
	const excepted = "node_modules/@img/sharp-win32-ia32"
	if optionalPlatformExceptions[excepted] == "" {
		t.Fatalf("%s lost its exception reason", excepted)
	}
	optional := []byte(fmt.Sprintf(`{"packages": {"": {}, %q: {"optional": true, "engines":{"node":"^20.9.0"}}}}`, excepted))
	if _, err := CheckPresetNodeFloor(manifest, optional); err != nil {
		t.Fatalf("the named optional exception %s was refused: %v", excepted, err)
	}
	required := []byte(fmt.Sprintf(`{"packages": {"": {}, %q: {"engines":{"node":"^20.9.0"}}}}`, excepted))
	if _, err := CheckPresetNodeFloor(manifest, required); err == nil {
		t.Fatalf("%s as a required package was skipped by its optional exception", excepted)
	}
}
