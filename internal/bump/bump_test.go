package bump

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =========================================================================
// Positive 3D Tests
// =========================================================================

func TestClassifyChannel_Positive(t *testing.T) {
	cases := []struct {
		ver      string
		expected ReleaseChannel
	}{
		{"v1.2.3", ChannelStable},
		{"v2.0.0-rc.1", ChannelRC},
		{"v1.0.0-beta.2", ChannelBeta},
		{"v0.5.0-alpha.1", ChannelAlpha},
		{"v0.1.0-nightly.20260901", ChannelNightly},
		{"v3.0.0-preview.1", ChannelNightly},
	}

	for _, tc := range cases {
		actual := ClassifyChannel(tc.ver)
		if actual != tc.expected {
			t.Errorf("for version %s: expected %s, got %s", tc.ver, tc.expected, actual)
		}
	}
}

// Positive: the online scan's success branch. The stand-in go reports an upgrade for two of
// the three requirements, so the classification below can only come from `go list`: the
// offline inventory of the same go.mod has no candidate at all.
func TestScanDependencies_Positive_GoMod(t *testing.T) {
	tmpDir := t.TempDir()
	writeGoMod(t, tmpDir, `module test-service

go 1.24

require (
	github.com/gin-gonic/gin v1.9.1
	github.com/spf13/cobra v1.8.0-rc.1
	github.com/stretchr/testify v1.9.0-beta.1
)
`)
	report := `{"Path":"test-service","Main":true}
{"Path":"github.com/gin-gonic/gin","Version":"v1.9.1","Update":{"Version":"v1.10.0"}}
{"Path":"github.com/spf13/cobra","Version":"v1.8.0-rc.1","Update":{"Version":"v1.8.0-rc.2"}}
{"Path":"github.com/stretchr/testify","Version":"v1.9.0-beta.1"}
`
	bin, log := standInToolchain(t, map[string]standInReply{goListCall: {Out: report}}, "go")
	t.Setenv("PATH", bin)

	withPrerelease, err := ScanDependencies(testDeadline(t), tmpDir, true)
	if err != nil {
		t.Fatalf("ScanDependencies failed: %v", err)
	}
	stables := candidateNames(&BumpReport{Stables: withPrerelease.Stables})
	prereleases := candidateNames(&BumpReport{Prereleases: withPrerelease.Prereleases})
	if withPrerelease.TotalScanned != 3 || strings.Join(stables, " ") != "github.com/gin-gonic/gin:v1.9.1->v1.10.0" ||
		strings.Join(prereleases, " ") != "github.com/spf13/cobra:v1.8.0-rc.1->v1.8.0-rc.2" {
		t.Fatalf("scanned=%d stables=%v prereleases=%v", withPrerelease.TotalScanned, stables, prereleases)
	}

	stableOnly, err := ScanDependencies(testDeadline(t), tmpDir, false)
	if err != nil || stableOnly.TotalScanned != 3 || strings.Join(candidateNames(stableOnly), " ") != stables[0] {
		t.Fatalf("stable-only scan = %v, %v; want only the gin upgrade of 3 scanned", candidateNames(stableOnly), err)
	}
	calls := standInCalls(t, log)
	if len(calls) != 2 || calls[0].Call != goListCall || !samePath(calls[0].Dir, tmpDir) {
		t.Fatalf("go calls = %+v, want one go list per scan in the module", calls)
	}
}

// Positive: pnpm exits 1 whenever it reports outdated packages. That exit status is the
// report, not a failure, and each outdated package is filed under its channel.
func TestScanDependencies_Positive_PackageJSON(t *testing.T) {
	tmpDir := t.TempDir()
	pkgJSON := `{"name":"web-app","dependencies":{"react":"^19.0.0-rc.1","lodash":"^4.17.21"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	outdated := `{"lodash":{"current":"4.17.21","latest":"4.18.0"},"react":{"current":"19.0.0-rc.1","latest":"19.0.0-rc.2"}}`
	bin, log := standInToolchain(t, map[string]standInReply{pnpmOutdate: {Out: outdated, Code: 1}}, "pnpm")
	t.Setenv("PATH", bin)

	report, err := ScanDependencies(testDeadline(t), tmpDir, true)
	if err != nil {
		t.Fatalf("ScanDependencies failed: %v", err)
	}
	stables := strings.Join(candidateNames(&BumpReport{Stables: report.Stables}), " ")
	prereleases := strings.Join(candidateNames(&BumpReport{Prereleases: report.Prereleases}), " ")
	if report.TotalScanned != 2 || stables != "lodash:4.17.21->4.18.0" || prereleases != "react:19.0.0-rc.1->19.0.0-rc.2" {
		t.Fatalf("scanned=%d stables=%q prereleases=%q", report.TotalScanned, stables, prereleases)
	}
	if calls := standInCalls(t, log); len(calls) != 1 || calls[0].Call != pnpmOutdate || !samePath(calls[0].Dir, tmpDir) {
		t.Fatalf("pnpm calls = %+v, want one pnpm outdated in the package", calls)
	}
}

// Positive: a dry run plans the canary and stops there. The same fixture and stand-in then
// run the canary for real, so the fixture demonstrably observes the test the dry run skipped.
func TestRunCanary_Positive_DryRun(t *testing.T) {
	dir, candidate := canaryFixture(t)
	bin, log := standInToolchain(t, map[string]standInReply{pnpmTest: {Out: "ok\n"}}, "pnpm")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	opts := CanaryOptions{RepoPath: dir, Candidate: candidate, DryRun: true}

	planned, err := RunCanary(testDeadline(t), opts)
	if err != nil || planned.Status != CanaryPlanned || planned.Success || planned.CanaryCertified || planned.WorktreePath != "" {
		t.Fatalf("dry run = %+v, %v; want planned, unexecuted and uncertified", planned, err)
	}
	if !strings.Contains(planned.ExecutionLog, "fixture-dep -> 2.0.0") {
		t.Fatalf("dry-run log does not name the candidate: %q", planned.ExecutionLog)
	}
	if calls := standInCalls(t, log); len(calls) != 0 || canaryWorktrees(t, dir) != 1 {
		t.Fatalf("dry run executed %q or created a worktree", callNames(calls))
	}

	opts.DryRun = false
	ran, err := RunCanary(testDeadline(t), opts)
	if err != nil || ran.Status != CanaryPassed || strings.Join(callNames(standInCalls(t, log)), " ") != pnpmTest {
		t.Fatalf("executed canary = %+v, %v; want the pnpm test the dry run skipped", ran, err)
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

// Negative: a nil context is refused before any tool runs, and a cancelled one with its
// cause kept. The live scan that follows keeps both refusals from passing against a
// scanner that fails every call.
func TestScanDependencies_Negative_NilContext(t *testing.T) {
	repo := inventoryRepo(t)
	bin, log := standInToolchain(t, map[string]standInReply{
		goListCall: {Out: currentGoReport}, pnpmOutdate: {Out: "{}"},
	}, "go", "pnpm")
	t.Setenv("PATH", bin)

	report, err := ScanDependencies(nilTestContext(), repo, true)
	if report != nil || err == nil || !strings.Contains(err.Error(), "context cannot be nil") {
		t.Fatalf("nil context = %+v, %v; want a refusal", report, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if report, err := ScanDependencies(cancelled, repo, true); report != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context = %+v, %v; want context.Canceled", report, err)
	}
	if calls := standInCalls(t, log); len(calls) != 0 {
		t.Fatalf("a refused scan ran %q", callNames(calls))
	}

	report, err = ScanDependencies(testDeadline(t), repo, true)
	if err != nil || report.TotalScanned != 4 || len(standInCalls(t, log)) != 2 {
		t.Fatalf("live scan = %+v, %v; want 4 scanned through go list and pnpm outdated", report, err)
	}
}

// Negative: a canary with a nil context returns no result and creates no worktree; the
// same options under a live context create one, run the test and remove it again.
func TestRunCanary_Negative_NilContext(t *testing.T) {
	dir, candidate := canaryFixture(t)
	bin, log := standInToolchain(t, map[string]standInReply{pnpmTest: {}}, "pnpm")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	opts := CanaryOptions{RepoPath: dir, Candidate: candidate}

	res, err := RunCanary(nilTestContext(), opts)
	if res != nil || err == nil || !strings.Contains(err.Error(), "context cannot be nil") {
		t.Fatalf("nil context = %+v, %v; want a refusal", res, err)
	}
	if calls := standInCalls(t, log); len(calls) != 0 || canaryWorktrees(t, dir) != 1 {
		t.Fatalf("a refused canary ran %q or created a worktree", callNames(calls))
	}

	res, err = RunCanary(testDeadline(t), opts)
	if err != nil || res.Status != CanaryPassed || len(standInCalls(t, log)) != 1 || canaryWorktrees(t, dir) != 1 {
		t.Fatalf("live canary = %+v, %v; want one test run and its worktree removed", res, err)
	}
}

// Negative: ApplyBump refuses a nil or cancelled context with the manifest untouched; the
// same candidate under a live context raises the range.
func TestApplyBump_Negative_NilContext(t *testing.T) {
	const manifest = `{"dependencies":{"typescript":"^5.0.0"}}`
	dir := writePackageJSON(t, manifest)
	cand := nodeCandidate("typescript", "5.7.3")
	t.Setenv("PATH", t.TempDir())

	if err := ApplyBump(nilTestContext(), dir, cand, ""); err == nil || !strings.Contains(err.Error(), "context cannot be nil") {
		t.Fatalf("nil context: %v; want a refusal", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ApplyBump(cancelled, dir, cand, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: %v; want context.Canceled", err)
	}
	if got := readPackageJSON(t, dir); got != manifest {
		t.Fatalf("a refused bump changed package.json: %s", got)
	}

	if err := ApplyBump(testDeadline(t), dir, cand, ""); err != nil {
		t.Fatalf("live bump failed: %v", err)
	}
	if got := readPackageJSON(t, dir); got != `{"dependencies":{"typescript":"^5.7.3"}}` {
		t.Fatalf("live bump wrote %s", got)
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

// Boundary: manifests that declare nothing have an empty inventory even though the stand-in
// package managers would report upgrades, and neither is asked. One declared requirement is
// the smallest inventory: only then does go list run, and only that requirement is scanned.
func TestScanDependencies_Boundary_EmptyManifests(t *testing.T) {
	tmpDir := t.TempDir()
	writeGoMod(t, tmpDir, "module empty\n")
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	goReport := `{"Path":"example.com/transitive","Version":"v0.1.0","Update":{"Version":"v0.2.0"}}
{"Path":"example.com/dep","Version":"v1.0.0","Update":{"Version":"v1.1.0"}}
`
	bin, log := standInToolchain(t, map[string]standInReply{
		goListCall:  {Out: goReport},
		pnpmOutdate: {Out: `{"left-pad":{"current":"1.0.0","latest":"1.3.0"}}`, Code: 1},
	}, "go", "pnpm")
	t.Setenv("PATH", bin)

	rep, err := ScanDependencies(testDeadline(t), tmpDir, true)
	if err != nil || rep.TotalScanned != 0 || rep.TotalCandidates != 0 {
		t.Fatalf("empty manifests = %+v, %v; want nothing scanned", rep, err)
	}
	if calls := standInCalls(t, log); len(calls) != 0 {
		t.Fatalf("an empty manifest asked %q", callNames(calls))
	}

	writeGoMod(t, tmpDir, "module empty\n\nrequire example.com/dep v1.0.0\n")
	rep, err = ScanDependencies(testDeadline(t), tmpDir, true)
	if err != nil || rep.TotalScanned != 1 || strings.Join(candidateNames(rep), " ") != "example.com/dep:v1.0.0->v1.1.0" {
		t.Fatalf("one requirement = %+v, %v; want it alone scanned and raised", rep, err)
	}
	if got := callNames(standInCalls(t, log)); len(got) != 1 || got[0] != goListCall {
		t.Fatalf("calls = %q, want one go list", got)
	}
}

func TestClassifyChannel_Boundary_EmptyAndComplex(t *testing.T) {
	ch1 := ClassifyChannel("")
	if ch1 != ChannelStable {
		t.Fatalf("expected stable for empty string, got: %s", ch1)
	}

	ch2 := ClassifyChannel("v1.0.0-rc.1+build.123")
	if ch2 != ChannelRC {
		t.Fatalf("expected rc channel for complex metadata, got: %s", ch2)
	}
}

// TestReconcileCatalog_Positive compares the offline inventory with the fleet catalog: no
// go command is on PATH, so the result cannot depend on the network.
func TestReconcileCatalog_Positive(t *testing.T) {
	ctx := testDeadline(t)
	tmpDir := t.TempDir()
	t.Setenv("PATH", t.TempDir())

	goMod := `module test-catalog
go 1.24
require (
	gopkg.in/yaml.v3 v3.0.0
	github.com/google/uuid v1.3.0
)
`
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	candidates, err := ReconcileCatalog(ctx, tmpDir)
	if err != nil {
		t.Fatalf("ReconcileCatalog failed: %v", err)
	}

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates to reconcile against FleetCatalog, got: %d", len(candidates))
	}
}

// TestApplyUpdate_NodeFallback edits package.json in place: without a pnpm lockfile no
// package manager runs, so none is on PATH.
func TestApplyUpdate_NodeFallback(t *testing.T) {
	ctx := testDeadline(t)
	tmpDir := t.TempDir()
	t.Setenv("PATH", t.TempDir())

	pkgJSON := `{
  "name": "sample",
  "dependencies": {
    "typescript": "^5.0.0"
  }
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	cand := UpgradeCandidate{
		Package:        "typescript",
		CurrentVersion: "^5.0.0",
		TargetVersion:  "^5.7.3",
		ManifestType:   "package.json",
	}

	if err := ApplyUpdate(ctx, tmpDir, cand); err != nil {
		t.Fatalf("ApplyUpdate failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "^5.7.3") {
		t.Fatalf("expected package.json to contain ^5.7.3, got: %s", string(data))
	}
}

// nilTestContext supplies the deliberately invalid argument for guard tests.
func nilTestContext() context.Context { return nil }
