// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bump

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testCatalog is a fixed catalog so direction tests do not move when FleetCatalog is refreshed.
var testCatalog = map[string]CatalogEntry{
	"example.org/lib":  {Version: "v1.6.0", Verified: "2026-09-26"},
	"example.org/sync": {Version: "v0.9.0", Verified: "2026-09-26"},
	"node-lib":         {Version: "^7.0.2", Verified: "2026-09-26"},
}

func discoveredDep(pkg, current, manifest string) UpgradeCandidate {
	return UpgradeCandidate{Package: pkg, CurrentVersion: current, TargetVersion: current, Channel: ChannelStable, ManifestType: manifest, ModuleDir: "."}
}

func TestReconcileWithCatalogBehindIsUpgrade(t *testing.T) {
	report := reconcileWithCatalog([]UpgradeCandidate{
		discoveredDep("example.org/lib", "v1.3.0", "go.mod"),
		discoveredDep("node-lib", "5.9.3", "package.json"),
	}, testCatalog)
	if len(report.Upgrades) != 2 || len(report.Ahead) != 0 || len(report.Unranked) != 0 {
		t.Fatalf("behind dependencies not reported as upgrades: %+v", report)
	}
	if got := report.Upgrades[0]; got.TargetVersion != "v1.6.0" || got.CurrentVersion != "v1.3.0" || got.ManifestType != "go.mod" {
		t.Fatalf("go upgrade = %+v", got)
	}
	if got := report.Upgrades[1]; got.TargetVersion != "^7.0.2" || got.CurrentVersion != "5.9.3" {
		t.Fatalf("node upgrade = %+v", got)
	}
}

// TestReconcileWithCatalogAheadIsNeverAnUpgrade covers BUG-161: a repository newer than its
// catalog pin was offered the pin as an upgrade, and unify --apply wrote the downgrade. The
// v0.10.0/v0.9.0 pair also orders the wrong way as strings.
func TestReconcileWithCatalogAheadIsNeverAnUpgrade(t *testing.T) {
	report := reconcileWithCatalog([]UpgradeCandidate{
		discoveredDep("example.org/lib", "v1.7.0", "go.mod"),
		discoveredDep("example.org/sync", "v0.10.0", "go.mod"),
		discoveredDep("node-lib", "^8.0.0", "package.json"),
	}, testCatalog)
	if len(report.Upgrades) != 0 {
		t.Fatalf("repository ahead of the catalog got downgrade candidates: %+v", report.Upgrades)
	}
	if len(report.Ahead) != 3 {
		t.Fatalf("ahead dependencies not reported: %+v", report)
	}
	if got := report.Ahead[1]; got.Package != "example.org/sync" || got.CurrentVersion != "v0.10.0" || got.CatalogVersion != "v0.9.0" {
		t.Fatalf("ahead entry = %+v", got)
	}
}

func TestReconcileWithCatalogBoundaries(t *testing.T) {
	cases := []struct {
		name, pkg, current        string
		upgrades, ahead, unranked int
	}{
		{"equal caret against caret pin", "node-lib", "^7.0.2", 0, 0, 0},
		{"equal tilde against caret pin", "node-lib", "~7.0.2", 0, 0, 0},
		{"equal resolved against caret pin", "node-lib", "7.0.2", 0, 0, 0},
		{"build metadata ignored", "example.org/lib", "v1.6.0+meta", 0, 0, 0},
		{"prerelease of the pin is older", "example.org/lib", "v1.6.0-rc.1", 1, 0, 0},
		{"pseudo-version before the pin", "example.org/lib", "v0.0.0-20240101000000-abcdefabcdef", 1, 0, 0},
		{"one patch ahead", "example.org/lib", "v1.6.1", 0, 1, 0},
		{"workspace protocol", "node-lib", "workspace:*", 0, 0, 1},
		{"compound range", "node-lib", ">=7.0.0", 0, 0, 1},
		{"upper cap below the pin", "node-lib", "<7.0.0", 0, 0, 1},
		{"upper cap above the pin", "node-lib", "<9.0.0", 0, 0, 1},
		{"inclusive upper cap", "node-lib", "<=5.0.0", 0, 0, 1},
		{"strict lower bound", "node-lib", ">5.0.0", 0, 0, 1},
		{"exact comparator", "node-lib", "=5.0.0", 0, 0, 1},
		{"bare version behind the pin", "node-lib", "5.0.0", 1, 0, 0},
		{"surrounding space", "node-lib", " ^5.0.0", 0, 0, 1},
		{"dist-tag", "node-lib", "latest", 0, 0, 1},
		{"doubled operator", "node-lib", "^^7.0.2", 0, 0, 1},
		{"empty version", "node-lib", "", 0, 0, 1},
		{"package not in catalog", "example.org/other", "v0.0.1", 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := reconcileWithCatalog([]UpgradeCandidate{discoveredDep(tc.pkg, tc.current, "package.json")}, testCatalog)
			if len(report.Upgrades) != tc.upgrades || len(report.Ahead) != tc.ahead || len(report.Unranked) != tc.unranked {
				t.Fatalf("%s %q: got upgrades=%d ahead=%d unranked=%d, want %d/%d/%d", tc.pkg, tc.current,
					len(report.Upgrades), len(report.Ahead), len(report.Unranked), tc.upgrades, tc.ahead, tc.unranked)
			}
		})
	}
}

func TestReconcileWithCatalogUnparseablePinIsUnranked(t *testing.T) {
	catalog := map[string]CatalogEntry{"node-lib": {Version: "latest", Verified: "2026-09-26"}}
	report := reconcileWithCatalog([]UpgradeCandidate{discoveredDep("node-lib", "1.0.0", "package.json")}, catalog)
	if len(report.Upgrades) != 0 || len(report.Unranked) != 1 || report.Unranked[0].CatalogVersion != "latest" {
		t.Fatalf("non-SemVer pin ranked: %+v", report)
	}
	empty := reconcileWithCatalog(nil, catalog)
	if empty.Upgrades == nil || empty.Ahead == nil || empty.Unranked == nil {
		t.Fatalf("empty report carries nil slices: %+v", empty)
	}
}

// writeCatalogRepo writes a Go module and a Node package with one dependency behind, one ahead
// of and one equal to FleetCatalog, and hides go and pnpm so the static manifest scanners run.
func writeCatalogRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	repo := t.TempDir()
	files := map[string]string{
		"go.mod":       "module example.test/catalog\n\ngo 1.24\n\nrequire (\n\tgithub.com/google/uuid v1.3.0\n\tgolang.org/x/sync v0.99.0\n)\n",
		"package.json": `{"dependencies":{"typescript":"^5.0.0","eslint":"^99.0.0","prettier":"` + FleetCatalog["prettier"].Version + `"}}` + "\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestReconcileCatalogReportScansRepository(t *testing.T) {
	repo := writeCatalogRepo(t)
	report, err := ReconcileCatalogReport(t.Context(), repo)
	if err != nil {
		t.Fatalf("ReconcileCatalogReport: %v", err)
	}
	upgrades := map[string]string{}
	for _, c := range report.Upgrades {
		upgrades[c.Package] = c.TargetVersion
	}
	if len(upgrades) != 2 || upgrades["github.com/google/uuid"] != FleetCatalog["github.com/google/uuid"].Version || upgrades["typescript"] != FleetCatalog["typescript"].Version {
		t.Fatalf("upgrades = %v", upgrades)
	}
	ahead := map[string]bool{}
	for _, d := range report.Ahead {
		ahead[d.Package] = true
	}
	if len(ahead) != 2 || !ahead["golang.org/x/sync"] || !ahead["eslint"] {
		t.Fatalf("ahead = %+v", report.Ahead)
	}
	candidates, err := ReconcileCatalog(t.Context(), repo)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("ReconcileCatalog returned %+v, %v; want the two upgrades only", candidates, err)
	}
}

func TestReconcileCatalogReportPropagatesCancellation(t *testing.T) {
	repo := writeCatalogRepo(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if report, err := ReconcileCatalogReport(ctx, repo); err == nil || report != nil {
		t.Fatalf("cancelled reconcile = %+v, %v", report, err)
	}
	if candidates, err := ReconcileCatalog(ctx, repo); err == nil || candidates != nil {
		t.Fatalf("cancelled ReconcileCatalog = %+v, %v", candidates, err)
	}
}

func TestReconcileCatalogReportRejectsNilContext(t *testing.T) {
	if report, err := ReconcileCatalogReport(nilTestContext(), t.TempDir()); err == nil || report != nil {
		t.Fatalf("nil-context reconcile = %+v, %v; want an error and no report", report, err)
	}
	if candidates, err := ReconcileCatalog(nilTestContext(), t.TempDir()); err == nil || candidates != nil {
		t.Fatalf("nil-context ReconcileCatalog = %+v, %v; want an error and no candidates", candidates, err)
	}
}

// rankedVersion admits exactly the bare, caret and tilde forms of splitRangeOperator; every
// other comparator splitRangeOperator reads is refused, as is what it refuses itself.
func TestRankedVersion_Boundaries(t *testing.T) {
	for spec, want := range map[string]string{"1.2.3": "1.2.3", "^1.2.3": "1.2.3", "~1.2.3": "1.2.3", "v1.6.0": "v1.6.0", "^2.0.0-rc.1": "2.0.0-rc.1"} {
		if got, ok := rankedVersion(spec); !ok || got != want {
			t.Errorf("rankedVersion(%q) = %q, %v; want %q", spec, got, ok, want)
		}
	}
	for _, spec := range []string{">=1.2.3", "<=1.2.3", ">1.2.3", "<2.0.0", "=1.2.3", "", "latest", "1.x", "^^1.2.3", " ^1.2.3", "workspace:^1.2.3"} {
		if got, ok := rankedVersion(spec); ok {
			t.Errorf("rankedVersion(%q) = %q, true; want refused", spec, got)
		}
	}
}

func TestVerifiedWithin(t *testing.T) {
	now := time.Date(2026, 9, 26, 15, 4, 5, 0, time.UTC)
	bound := 90 * 24 * time.Hour
	cases := []struct {
		name, verified string
		ok             bool
	}{
		{"verified today", "2026-09-26", true},
		{"exactly at the bound", "2026-06-28", true},
		{"one day past the bound", "2026-06-27", false},
		{"in the future", "2026-09-27", false},
		{"not a date", "last week", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := verifiedWithin(tc.verified, now, bound); (err == nil) != tc.ok {
				t.Fatalf("verifiedWithin(%q) = %v, want ok=%v", tc.verified, err, tc.ok)
			}
		})
	}
}

// TestFleetCatalogVerified is the dated assertion from issue #337: it fails once any pin was
// last checked against upstream more than maxCatalogAge ago. Refresh the stale entries against
// proxy.golang.org / the npm registry and move their Verified date; never widen the bound to
// make it pass.
func TestFleetCatalogVerified(t *testing.T) {
	for pkg, entry := range FleetCatalog {
		if err := verifiedWithin(entry.Verified, time.Now(), maxCatalogAge); err != nil {
			t.Errorf("FleetCatalog[%q]: %v", pkg, err)
		}
		if _, ok := catalogVersion(entry.Version); !ok {
			t.Errorf("FleetCatalog[%q] pin %q is not SemVer, so no dependency can be ranked against it", pkg, entry.Version)
		}
	}
}
