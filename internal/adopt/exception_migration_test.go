// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Each manifest exception writer records the adoption's calendar day, including near midnight.
func TestAdoptionExceptionWritersEntered(t *testing.T) {
	today := time.Date(2026, 11, 5, 23, 30, 0, 0, time.FixedZone("west", -7*3600))
	entries := []config.Exception{
		newSupplyChainException(supplyChainGap{}, today),
		newDeclaredLinterException(config.DeclaredLinter{Name: "semgrep", Source: "security:high"}, today),
	}
	for _, entry := range entries {
		if entry.Category != "D" || entry.Entered != "2026-11-05" || entry.Expires != "2027-02-03" {
			t.Errorf("writer lifecycle: %+v", entry)
		}
	}
}

// exceptionMigrationSource returns a catalog lock source with a security:high profile for migration tests.
func exceptionMigrationSource(t *testing.T) string {
	t.Helper()
	body := "id: security:high\nname: Security\nsupply_chain:\n  slsa_level: 3\nlinters:\n  - gitleaks\n  - semgrep\n  - trivy\n"
	return newCatalogLockSource(t, &config.Manifest{Version: 1, Profiles: []string{"framework", "template-seed"},
		Facets: config.DefaultFacets()}, map[string]string{"security:high": body})
}

// First adoption exercises both writers through the manifest and declared-linter steps.
func TestAdoptExceptionLifecycleFirstAdoption(t *testing.T) {
	repo := newTestRepo(t, "exception-lifecycle")
	mustWrite(t, filepath.Join(repo, "main.go"), "package main\nfunc main() {}\n")
	adoptWithSource(t, repo, exceptionMigrationSource(t), false)
	manifest := decodeExceptionFixture(t, mustRead(t, filepath.Join(repo, manifestFile)))
	if len(manifest.Exceptions) != 4 {
		t.Fatalf("want supply-chain and three linter entries: %+v", manifest.Exceptions)
	}
	entered := config.ExceptionDay(time.Now()).Format(config.ExceptionDateLayout)
	for _, entry := range manifest.Exceptions {
		if entry.Category != "D" || entry.Entered != entered {
			t.Errorf("first adoption omitted lifecycle: %+v", entry)
		}
	}
}

// legacyAdoptionEntries returns YAML exception entries in the pre-category format for migration tests.
func legacyAdoptionEntries(expires string) string {
	entries := "  - rule: HISS-11\n    path: .github/workflows/release.yml\n    reason: adoption release gap\n    expires: " + expires + "\n"
	for _, tool := range []string{"gitleaks", "semgrep", "trivy"} {
		entries += "  - rule: declared-linters\n    tool: " + tool + "\n    reason: adoption CI gap\n    expires: " + expires + "\n"
	}
	return entries
}

// legacyAdoptionFixture creates a temporary repository with a legacy manifest containing the given entries.
func legacyAdoptionFixture(t *testing.T, entries string, blameTime time.Time) (string, string) {
	t.Helper()
	repo := newTestRepo(t, "exception-migration")
	mustWrite(t, filepath.Join(repo, "main.go"), "package main\nfunc main() {}\n")
	manifest := "---\nversion: 1\nrepository:\n  owner: acme\n  name: exception-migration\nprofiles:\n  - framework\nfacets:\n  - security:high\nexceptions:\n" + entries
	mustWrite(t, filepath.Join(repo, manifestFile), manifest)
	commitExceptionFixture(t, repo, blameTime)
	return repo, exceptionMigrationSource(t)
}

// Commit plumbing records blame evidence without invoking a repository hook.
func commitExceptionFixture(t *testing.T, repo string, blameTime time.Time) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := util.RunGit(ctx, repo, "add", "--", manifestFile); err != nil {
		t.Fatal(err)
	}
	tree, err := util.RunGit(ctx, repo, "write-tree")
	if err != nil {
		t.Fatal(err)
	}
	iso := blameTime.Format(time.RFC3339)
	commitCtx, err := util.WithCommandEnvironment(ctx, util.InheritedEnvironment([]string{
		"GIT_COMMITTER_DATE=" + iso, "GIT_AUTHOR_DATE=" + iso,
	}))
	if err != nil {
		t.Fatal(err)
	}
	commit, err := util.RunGit(commitCtx, repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit-tree", tree, "-m", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(ctx, repo, "update-ref", "HEAD", commit); err != nil {
		t.Fatal(err)
	}
}

// decodeExceptionFixture decodes a manifest from its YAML string representation and fails the test on error.
func decodeExceptionFixture(t *testing.T, data string) *config.Manifest {
	t.Helper()
	manifest, err := config.DecodeManifest([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

// migrationFixtureDates returns a blame time, its date string, and a
// category-A-fit expiry relative to the current clock so the migration fixture
// entries remain unexpired regardless of when the tests run.
func migrationFixtureDates() (blameTime time.Time, blameDate, expiry string) {
	blameTime = config.ExceptionDay(time.Now()).AddDate(0, 0, -16)
	blameDate = blameTime.Format(config.ExceptionDateLayout)
	expiry = blameTime.AddDate(0, 0, config.MaxExceptionDays).Format(config.ExceptionDateLayout)
	return
}

// Positive: the four legacy adoption entries use blame, keep expiry, and pass their audits.
func TestAdoptExceptionMigrationLegacy(t *testing.T) {
	blameTime, blameDate, expiry := migrationFixtureDates()
	repo, source := legacyAdoptionFixture(t, legacyAdoptionEntries(expiry), blameTime)
	report := adoptWithSource(t, repo, source, false)
	manifest := decodeExceptionFixture(t, mustRead(t, filepath.Join(repo, manifestFile)))
	if len(manifest.Exceptions) != 4 {
		t.Fatalf("lost legacy entries: %+v", manifest.Exceptions)
	}
	for _, entry := range manifest.Exceptions {
		if entry.Category != "A" || entry.Entered != blameDate || entry.Expires != expiry {
			t.Errorf("migration changed expiry or omitted blame: %+v", entry)
		}
		if !strings.Contains(fmt.Sprint(report.ActionDetails), "migrated exception "+entry.Rule+" "+entry.Target()) {
			t.Errorf("report omitted migrated entry: %+v", report.ActionDetails)
		}
	}
	assertMigratedExceptionAudits(t, repo, report, manifest.Exceptions, blameTime)
}

// Dry-run reports the same migration without publishing its proposed manifest.
func TestAdoptExceptionMigrationDryRun(t *testing.T) {
	blameTime, _, expiry := migrationFixtureDates()
	repo, source := legacyAdoptionFixture(t, legacyAdoptionEntries(expiry), blameTime)
	before := mustRead(t, filepath.Join(repo, manifestFile))
	report, err := Adopt(t.Context(), AdoptOptions{Path: repo, LockSourceRoot: source, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run migration: %v", err)
	}
	if !strings.Contains(fmt.Sprint(report.ActionDetails), "would migrate exception HISS-11 .github/workflows/release.yml") {
		t.Fatalf("dry-run omitted migration: %+v", report.ActionDetails)
	}
	if got := mustRead(t, filepath.Join(repo, manifestFile)); got != before {
		t.Fatal("dry-run published migrated manifest")
	}
}

// Migration planning never publishes a manifest before a later preflight refusal.
func TestAdoptExceptionMigrationPreflightRefusal(t *testing.T) {
	blameTime, _, expiry := migrationFixtureDates()
	repo, source := legacyAdoptionFixture(t, legacyAdoptionEntries(expiry), blameTime)
	before := mustRead(t, filepath.Join(repo, manifestFile))
	mustWrite(t, filepath.Join(repo, ".claude", "agents"), "not a directory\n")
	_, err := Adopt(t.Context(), AdoptOptions{Path: repo, LockSourceRoot: source})
	if err == nil {
		t.Fatal("invalid persona target passed preflight")
	}
	if got := mustRead(t, filepath.Join(repo, manifestFile)); got != before {
		t.Fatal("preflight refusal published migrated manifest")
	}
}

// Flavor planning keeps declared pins and rejects invalid or cancelled snapshots.
func TestAdoptionExceptionFlavorSnapshot(t *testing.T) {
	repo := t.TempDir()
	full := filepath.Join(repo, manifestFile)
	mustWrite(t, full, "version: 1\nunknown: true\n")
	ctx := config.WithManifestSnapshot(t.Context(), full, []byte("version: 1\nflavors:\n  - name: go-library\n"))
	targets, err := flavor.ResolveTargetsForProfileContext(ctx, repo, "framework")
	if err != nil || len(targets) != 1 || targets[0].Flavor != "go-library" {
		t.Fatalf("planned flavor pin ignored: %+v %v", targets, err)
	}
	if _, err := flavor.ResolveTargetsForProfileContext(t.Context(), repo, "framework"); err == nil {
		t.Fatal("invalid manifest accepted without snapshot")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := flavor.ResolveTargetsForProfileContext(ctx, repo, "framework"); !errors.Is(err, context.Canceled) {
		t.Fatalf("flavor cancellation lost: %v", err)
	}
}

// assertMigratedExceptionAudits validates that migrated exceptions pass audit and that the report is clean.
func assertMigratedExceptionAudits(t *testing.T, repo string, report *AdoptReport, entries []config.Exception, today time.Time) {
	t.Helper()
	if err := config.ValidateExceptions(entries, today); err != nil {
		t.Fatal(err)
	}
	out, err := AuditDeclaredLinters(t.Context(), DeclaredLintersOptions{RootDir: repo,
		Policy: report.EffectivePolicy, Exceptions: entries, Today: today})
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("declared-linters audit: %v\n%s", err, out)
	}
	out, err = AuditSupplyChain(t.Context(), SupplyChainOptions{Root: repo,
		Policy: &report.EffectivePolicy.Policy, Exceptions: entries, Today: today})
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("supply-chain audit: %v\n%s", err, out)
	}
}

// Negative: a hand-written entry outside adoption's rule kinds still blocks validation.
func TestAdoptExceptionMigrationOtherRuleRefused(t *testing.T) {
	blameTime, _, expiry := migrationFixtureDates()
	other := "  - rule: HISS-04\n    path: main.go\n    reason: hand-written complexity\n    expires: " + expiry + "\n"
	repo, source := legacyAdoptionFixture(t, legacyAdoptionEntries(expiry)+other, blameTime)
	before := mustRead(t, filepath.Join(repo, manifestFile))
	_, err := Adopt(t.Context(), AdoptOptions{Path: repo, LockSourceRoot: source})
	if err == nil || !strings.Contains(err.Error(), "exceptions[4] category is required") {
		t.Fatalf("uncategorised hand-written entry accepted: %v", err)
	}
	after := mustRead(t, filepath.Join(repo, manifestFile))
	if !strings.Contains(after, other) || after != before {
		t.Fatal("failed adoption changed hand-written input")
	}
	if err := config.ValidateExceptions(decodeExceptionFixture(t, after).Exceptions, time.Now()); err == nil {
		t.Fatal("audit accepted uncategorised input")
	}
}

// Complete lifecycle metadata remains byte-identical, even with hand-written comments.
func TestAdoptExceptionMigrationCompleteUnchanged(t *testing.T) {
	blameTime, blameDate, expiry := migrationFixtureDates()
	entries := strings.ReplaceAll(legacyAdoptionEntries(expiry), "    expires:",
		"    category: A # keep category\n    entered: "+blameDate+" # keep date\n    expires:")
	repo, source := legacyAdoptionFixture(t, entries, blameTime)
	adoptWithSource(t, repo, source, false)
	before := mustRead(t, filepath.Join(repo, manifestFile))
	adoptWithSource(t, repo, source, false)
	if after := mustRead(t, filepath.Join(repo, manifestFile)); after != before || !strings.Contains(after, entries) {
		t.Fatal("complete exception metadata was rewritten")
	}
}

// Boundary: no category can admit an expiry 121 days after the recorded entry date.
func TestAdoptExceptionMigrationUnresolved(t *testing.T) {
	blameTime, _, _ := migrationFixtureDates()
	unresolvedExpiry := blameTime.AddDate(0, 0, config.MaxExceptionDays+31).Format(config.ExceptionDateLayout)
	repo, source := legacyAdoptionFixture(t, legacyAdoptionEntries(unresolvedExpiry), blameTime)
	before := mustRead(t, filepath.Join(repo, manifestFile))
	report, err := Adopt(t.Context(), AdoptOptions{Path: repo, LockSourceRoot: source})
	if err == nil || !hasWarningContaining(report, "unresolved exception HISS-11 .github/workflows/release.yml") {
		t.Fatalf("unresolved migration not reported: err=%v report=%+v", err, report)
	}
	if after := mustRead(t, filepath.Join(repo, manifestFile)); after != before {
		t.Fatal("unresolved entries changed")
	}
}

// Partial metadata from an intermediate engine uses the same planner and retains existing values.
func TestAdoptExceptionMigrationPartial(t *testing.T) {
	blameTime, blameDate, expiry := migrationFixtureDates()
	for _, field := range []string{"category: D", "entered: " + blameDate} {
		t.Run(field, func(t *testing.T) {
			entries := strings.ReplaceAll(legacyAdoptionEntries(expiry), "    expires:", "    "+field+"\n    expires:")
			repo, source := legacyAdoptionFixture(t, entries, blameTime)
			adoptWithSource(t, repo, source, false)
			manifest := decodeExceptionFixture(t, mustRead(t, filepath.Join(repo, manifestFile)))
			for _, entry := range manifest.Exceptions {
				want := entry
				want.Entered = blameDate
				if field == "entered: "+blameDate {
					want.Category = "A"
				}
				if entry.Category == "" || !reflect.DeepEqual(entry, want) {
					t.Errorf("partial migration: %+v, want %+v", entry, want)
				}
			}
		})
	}
}
