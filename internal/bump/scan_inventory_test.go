// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bump

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

const inventoryGoMod = `module example.com/app

go 1.24

require (
	example.com/current v1.2.0
	example.com/stale v1.0.0
)
`

const inventoryPackageJSON = `{"dependencies":{"left-pad":"^1.3.0"},"devDependencies":{"typescript":"^5.0.0"}}`

// outdatedGoReport is a complete `go list -m -u -json all` report: the main module, an
// up-to-date requirement, an outdated one, and a transitive module go.mod never declares.
const outdatedGoReport = `{"Path":"example.com/app","Main":true}
{"Path":"example.com/current","Version":"v1.2.0"}
{"Path":"example.com/stale","Version":"v1.0.0","Update":{"Version":"v1.1.0"}}
{"Path":"example.com/transitive","Version":"v0.1.0","Update":{"Version":"v0.2.0"}}
`

// currentGoReport is a complete report in which nothing has an update.
const currentGoReport = `{"Path":"example.com/app","Main":true}
{"Path":"example.com/current","Version":"v1.2.0"}
{"Path":"example.com/stale","Version":"v1.0.0"}
`

// standInSource is a main package that prints out and exits with code, standing in for
// `go list` or `pnpm outdated` (pnpm exits 1 when it reports outdated packages).
func standInSource(out string, code int) string {
	return "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {\n\tfmt.Print(" +
		strconv.Quote(out) + ")\n\tos.Exit(" + strconv.Itoa(code) + ")\n}\n"
}

// inventoryRepo writes a repository declaring two Go and two Node dependencies.
func inventoryRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeGoMod(t, repo, inventoryGoMod)
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(inventoryPackageJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}

// reportBin builds go and pnpm stand-ins printing the given reports into a new directory.
// It must run before PATH is narrowed.
func reportBin(t *testing.T, goReport, pnpmReport string, pnpmCode int) string {
	t.Helper()
	bin := t.TempDir()
	testsupport.BuildExecutable(t, bin, "go", standInSource(goReport, 0))
	testsupport.BuildExecutable(t, bin, "pnpm", standInSource(pnpmReport, pnpmCode))
	return bin
}

// toolchainBin provides every tool auditToolchains probes, so toolchain warnings never
// enter the audits under test.
func toolchainBin(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	built := testsupport.BuildExecutable(t, t.TempDir(), "tool", standInSource("v1.0.0\n", 0))
	data, err := os.ReadFile(built)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"govulncheck", "gosec", "reuse", "lefthook"} {
		if err := os.WriteFile(filepath.Join(bin, testsupport.ExecutableName(tool)), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

func pathOf(dirs ...string) string {
	return strings.Join(dirs, string(os.PathListSeparator))
}

func candidateNames(report *BumpReport) []string {
	var names []string
	for _, c := range append(append([]UpgradeCandidate{}, report.Stables...), report.Prereleases...) {
		names = append(names, fmt.Sprintf("%s:%s->%s", c.Package, c.CurrentVersion, c.TargetVersion))
	}
	return names
}

// An online scan counts up-to-date dependencies as scanned, lists only the outdated
// ones as candidates, and ignores a module go.mod does not declare.
func TestScanInventory_Positive_CountsUpToDateAndOutdated(t *testing.T) {
	repo := inventoryRepo(t)
	pnpm := `{"typescript":{"current":"5.0.0","latest":"5.7.3","wanted":"5.0.0"}}`
	t.Setenv("PATH", reportBin(t, outdatedGoReport, pnpm, 1))

	report, err := ScanDependencies(t.Context(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(candidateNames(report), " ")
	want := "example.com/stale:v1.0.0->v1.1.0 typescript:5.0.0->5.7.3"
	if report.TotalScanned != 4 || report.TotalCandidates != 2 || got != want {
		t.Fatalf("scanned=%d candidates=%d [%s], want 4 scanned and [%s]", report.TotalScanned, report.TotalCandidates, got, want)
	}

	goInventory, err := ScanGoDependencies(t.Context(), repo, ScanOptions{})
	if err != nil || len(goInventory) != 2 || goInventory[0].CurrentVersion != goInventory[0].TargetVersion {
		t.Fatalf("Go inventory = %+v, %v; want the up-to-date requirement included", goInventory, err)
	}
}

// The audit scans the same inventory online and offline, and an up-to-date repository
// scores the same either way.
func TestScanInventory_Positive_AuditMatchesOnlineAndOffline(t *testing.T) {
	repo := inventoryRepo(t)
	tools := toolchainBin(t)
	online := reportBin(t, currentGoReport, "{}", 0)

	t.Setenv("PATH", pathOf(online, tools))
	up, err := AuditCodebaseVersions(t.Context(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools)
	down, err := AuditCodebaseVersions(t.Context(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if up.TotalScanned != 4 || down.TotalScanned != 4 {
		t.Fatalf("scanned online=%d offline=%d, want 4 both", up.TotalScanned, down.TotalScanned)
	}
	if up.ModernizationScore != down.ModernizationScore || up.ModernizationScore != 100 || !up.Passed {
		t.Fatalf("score online=%.1f offline=%.1f passed=%v, want 100 both", up.ModernizationScore, down.ModernizationScore, up.Passed)
	}
}

// Only total_scanned is independent of the network. An offline audit knows no upgrade
// target, so an outdated repository's score still differs: one of four dependencies
// outdated scores 75 online and 100 offline, over the same four scanned.
func TestScanInventory_Boundary_OfflineAuditCannotSeeUpgrades(t *testing.T) {
	repo := inventoryRepo(t)
	tools := toolchainBin(t)
	online := reportBin(t, outdatedGoReport, "{}", 0)

	t.Setenv("PATH", pathOf(online, tools))
	up, err := AuditCodebaseVersions(t.Context(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools)
	down, err := AuditCodebaseVersions(t.Context(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if up.TotalScanned != 4 || down.TotalScanned != 4 {
		t.Fatalf("scanned online=%d offline=%d, want 4 both", up.TotalScanned, down.TotalScanned)
	}
	if up.ModernizationScore != 75 || len(up.PendingUpgrades) != 1 {
		t.Fatalf("online score=%.1f pending=%d, want 75 and 1", up.ModernizationScore, len(up.PendingUpgrades))
	}
	if down.ModernizationScore != 100 || len(down.PendingUpgrades) != 0 {
		t.Fatalf("offline score=%.1f pending=%d, want 100 and 0", down.ModernizationScore, len(down.PendingUpgrades))
	}
}

// A complete upstream report with nothing outdated is an answer: it never pivots to the
// manifest-only result, and no dependency becomes a candidate.
func TestScanInventory_Negative_ZeroOutdatedIsNotAFallback(t *testing.T) {
	repo := inventoryRepo(t)
	t.Setenv("PATH", reportBin(t, currentGoReport, "{}", 0))
	report, err := ScanDependencies(t.Context(), repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalScanned != 4 || report.TotalCandidates != 0 {
		t.Fatalf("scanned=%d candidates=%v, want 4 scanned and no candidates", report.TotalScanned, candidateNames(report))
	}
}

// Offline, every declared dependency is scanned and none is a phantom candidate.
func TestScanInventory_Negative_OfflineHasNoPhantomCandidates(t *testing.T) {
	repo := inventoryRepo(t)
	t.Setenv("PATH", t.TempDir())
	report, err := ScanDependencies(t.Context(), repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalScanned != 4 || report.TotalCandidates != 0 {
		t.Fatalf("scanned=%d candidates=%v, want 4 scanned and no candidates", report.TotalScanned, candidateNames(report))
	}
}

// The catalog compares the whole inventory: a dependency with no upstream update still
// drifts from the fleet catalog. Behind its pin it is an upgrade; ahead of its pin it is
// reported and never turned into a downgrade.
func TestScanInventory_Positive_CatalogSeesUpToDateDependencies(t *testing.T) {
	repo := t.TempDir()
	writeGoMod(t, repo, "module example.com/app\n\nrequire (\n\tgithub.com/google/uuid v1.3.0\n\tgolang.org/x/sync v0.99.0\n\texample.com/stale v1.0.0\n)\n")
	report := `{"Path":"github.com/google/uuid","Version":"v1.3.0"}
{"Path":"golang.org/x/sync","Version":"v0.99.0"}
{"Path":"example.com/stale","Version":"v1.0.0","Update":{"Version":"v1.1.0"}}
`
	t.Setenv("PATH", reportBin(t, report, "{}", 0))
	drift, err := ReconcileCatalogReport(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	pin := FleetCatalog["github.com/google/uuid"].Version
	if len(drift.Upgrades) != 1 || drift.Upgrades[0].Package != "github.com/google/uuid" || drift.Upgrades[0].TargetVersion != pin {
		t.Fatalf("catalog upgrades = %+v, want uuid v1.3.0 -> %s", drift.Upgrades, pin)
	}
	if len(drift.Ahead) != 1 || drift.Ahead[0].Package != "golang.org/x/sync" {
		t.Fatalf("catalog ahead = %+v, want golang.org/x/sync reported ahead", drift.Ahead)
	}
	unified, err := ReconcileCatalog(t.Context(), repo)
	if err != nil || len(unified) != 1 || unified[0].Package != "github.com/google/uuid" {
		t.Fatalf("ReconcileCatalog = %+v, %v; want the uuid upgrade only", unified, err)
	}
}

// A repository whose only manifest bump cannot scan is not a 100 percent pass, and one
// unexamined ecosystem next to scanned dependencies still costs the score.
func TestScanInventory_Boundary_UnexaminedManifestFailsAudit(t *testing.T) {
	cargoOnly := t.TempDir()
	if err := os.WriteFile(filepath.Join(cargoOnly, "Cargo.toml"), []byte("[package]\nname = \"app\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolchainBin(t))
	report, err := AuditCodebaseVersions(t.Context(), cargoOnly, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || report.ModernizationScore != 0 || len(report.Deprecations) != 1 {
		t.Fatalf("Cargo-only audit passed=%v score=%.1f deprecations=%+v", report.Passed, report.ModernizationScore, report.Deprecations)
	}
	if d := report.Deprecations[0]; d.Kind != "unsupported-manifest" || d.Component != "rust" {
		t.Fatalf("deprecation = %+v, want unsupported-manifest for rust", d)
	}

	writeGoMod(t, cargoOnly, "module example.com/app\n\nrequire example.com/dep v1.0.0\n")
	mixed, err := AuditCodebaseVersions(t.Context(), cargoOnly, false)
	if err != nil {
		t.Fatal(err)
	}
	if mixed.Passed || mixed.TotalScanned != 1 || mixed.ModernizationScore != 50 {
		t.Fatalf("Go+Cargo audit passed=%v scanned=%d score=%.1f, want failed, 1, 50", mixed.Passed, mixed.TotalScanned, mixed.ModernizationScore)
	}
}

// A prerelease update the channel policy refuses leaves the dependency in the inventory
// with no target instead of dropping it from the count.
func TestScanInventory_Boundary_RefusedPrereleaseStaysScanned(t *testing.T) {
	record := `{"Path":"example.com/dep","Version":"v1.0.0","Update":{"Version":"v1.1.0-rc.1"}}`
	got, err := decodeGoModules(record, ".", ScanOptions{})
	if err != nil || len(got) != 1 || got[0].TargetVersion != "v1.0.0" {
		t.Fatalf("stable-only entry = %+v, %v; want v1.0.0 kept without a target", got, err)
	}
	got, err = decodeGoModules(record, ".", ScanOptions{IncludePrerelease: true})
	if err != nil || len(got) != 1 || got[0].TargetVersion != "v1.1.0-rc.1" || got[0].Channel != ChannelRC {
		t.Fatalf("prerelease entry = %+v, %v; want the rc target", got, err)
	}
}

// Node sections are bounded: exactly the bound is scanned, one more is refused rather
// than truncated, and a package declared twice counts once.
func TestScanInventory_Boundary_NodeManifestEntries(t *testing.T) {
	write := func(t *testing.T, count int, extra string) string {
		t.Helper()
		deps := make([]string, 0, count)
		for i := range count {
			deps = append(deps, fmt.Sprintf("%q:\"1.0.0\"", fmt.Sprintf("dep-%04d", i)))
		}
		repo := t.TempDir()
		body := `{"dependencies":{` + strings.Join(deps, ",") + `}` + extra + `}`
		if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return repo
	}
	t.Setenv("PATH", t.TempDir())
	atBound, err := ScanNodeDependencies(t.Context(), write(t, maxManifestDependencies, ""), ScanOptions{})
	if err != nil || len(atBound) != maxManifestDependencies {
		t.Fatalf("at the bound: %d entries, %v", len(atBound), err)
	}
	if over, err := ScanNodeDependencies(t.Context(), write(t, maxManifestDependencies+1, ""), ScanOptions{}); err == nil {
		t.Fatalf("over the bound accepted %d entries", len(over))
	}
	twice, err := ScanNodeDependencies(t.Context(), write(t, 1, `,"devDependencies":{"dep-0000":"1.0.0"}`), ScanOptions{})
	if err != nil || len(twice) != 1 {
		t.Fatalf("package declared twice: %+v, %v", twice, err)
	}
}

// The offline package.json scan reduces a bare, caret or tilde range to its version and
// carries every other spec verbatim, so a comparator's version is never ranked as if the
// range resolved to it.
func TestScanInventory_Boundary_NodeRangeForms(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	repo := t.TempDir()
	body := `{"dependencies":{"a":"1.0.0","b":"^2.0.0","c":"~3.0.0","d":"=4.0.0","e":">=5.0.0","f":"<6.0.0","g":"<=7.0.0","h":">8.0.0","i":"workspace:*"}}`
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	inventory, err := ScanNodeDependencies(t.Context(), repo, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(inventory))
	for _, c := range inventory {
		if c.CurrentVersion != c.TargetVersion {
			t.Fatalf("offline entry %+v has an upgrade target", c)
		}
		got = append(got, c.Package+"="+c.CurrentVersion)
	}
	want := "a=1.0.0 b=2.0.0 c=3.0.0 d==4.0.0 e=>=5.0.0 f=<6.0.0 g=<=7.0.0 h=>8.0.0 i=workspace:*"
	if strings.Join(got, " ") != want {
		t.Fatalf("inventory = %s\nwant        %s", strings.Join(got, " "), want)
	}
}

func TestCalculateAuditScore_Boundaries(t *testing.T) {
	cases := []struct {
		total, pending, deps, unexamined, upToDate int
		score                                      float64
	}{
		{0, 0, 0, 0, 0, 100},
		{0, 0, 0, 1, 0, 0},
		{4, 1, 0, 0, 3, 75},
		{1, 1, 1, 0, 0, 0},
		{3, 0, 0, 1, 3, 75},
	}
	for _, tc := range cases {
		upToDate, score := calculateAuditScore(tc.total, tc.pending, tc.deps, tc.unexamined)
		if upToDate != tc.upToDate || score != tc.score {
			t.Errorf("calculateAuditScore(%d,%d,%d,%d) = %d, %.1f; want %d, %.1f",
				tc.total, tc.pending, tc.deps, tc.unexamined, upToDate, score, tc.upToDate, tc.score)
		}
	}
}
