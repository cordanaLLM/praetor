// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// writeTestLockAndCatalog writes a pinned lock and catalog entry for the given profile into the test repository.
func writeTestLockAndCatalog(t *testing.T, repo, profileID string) {
	t.Helper()
	source := fmt.Sprintf("id: %q\nname: %q\n", profileID, profileID)
	mustWrite(t, filepath.Join(repo, ".config", "archetypes", profileID+".yaml"), source)
	testsupport.WritePinnedLock(t, repo, profileID, source)
}

// assertExceptionFields checks that an exception carries the expected category, expires and entered state.
func assertExceptionFields(t *testing.T, label string, e config.Exception, wantCat, wantExpires string, wantEnteredSet bool) {
	t.Helper()
	if e.Category != wantCat {
		t.Errorf("%s category = %q, want %q", label, e.Category, wantCat)
	}
	if wantEnteredSet && e.Entered == "" {
		t.Errorf("%s entered is empty, want set", label)
	}
	if !wantEnteredSet && e.Entered != "" {
		t.Errorf("%s entered = %q, want empty", label, e.Entered)
	}
	if e.Expires != wantExpires {
		t.Errorf("%s expires = %q, want %q", label, e.Expires, wantExpires)
	}
}

// refreshManifest re-reads and decodes the manifest at the given path.
func refreshManifest(t *testing.T, manifestPath string) *config.Manifest {
	t.Helper()
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read refreshed manifest: %v", err)
	}
	m, err := config.DecodeManifest(data)
	if err != nil {
		t.Fatalf("decode refreshed manifest: %v", err)
	}
	return m
}

// 1a: pre-1711 fixture manifest with both example entries, no category: refresh output carries category D and entered on both, expires unchanged; audit of the fixture passes the exception rule.
func TestAdoptRefresh_CategorisesPre1711OwnedExceptions(t *testing.T) {
	repoPath := t.TempDir()
	manifestContent := `---
version: 1
repository:
  owner: acme
  name: pre-1711-refresh
profiles:
  - native-gpu-systems
exceptions:
  - rule: HISS-11
    path: .github/workflows/docker-publish.yml
    reason: "adoption found the release workflows short of the declared supply chain (SLSA Build Level 3 declared, Level 2 measured); raise them as docs/guides/releasing.md in Praetor describes, then remove this entry"
    expires: "2027-01-20"
  - rule: declared-linters
    tool: cppcheck
    reason: "adoption scaffolds no CI job for cppcheck declared by profile native-gpu-systems; run it in a workflow, or remove this entry"
    expires: "2027-01-20"
`
	manifestPath := filepath.Join(repoPath, manifestFile)
	mustWrite(t, manifestPath, manifestContent)
	writeTestLockAndCatalog(t, repoPath, "native-gpu-systems")

	// Pre-condition: audit of pre-1711 manifest fails because category is missing.
	preManifest, err := config.DecodeManifest([]byte(manifestContent))
	if err != nil {
		t.Fatalf("decode pre-manifest: %v", err)
	}
	fixedNow := categoriseFixture()
	if err := config.ValidateExceptions(preManifest.Exceptions, fixedNow); err == nil {
		t.Fatal("pre-1711 manifest should fail exception validation due to missing category")
	}

	s := &adoptSession{
		repoPath: repoPath,
		report:   &AdoptReport{},
		opts:     AdoptOptions{Path: repoPath},
	}
	missing := []config.DeclaredLinter{
		{Name: "cppcheck", Source: "profile native-gpu-systems"},
	}
	if err := s.syncDeclaredLintersExceptions(t.Context(), manifestPath, missing); err != nil {
		t.Fatalf("syncDeclaredLintersExceptions: %v", err)
	}

	afterManifest := refreshManifest(t, manifestPath)
	if len(afterManifest.Exceptions) != 2 {
		t.Fatalf("expected 2 exceptions, got %d", len(afterManifest.Exceptions))
	}
	for _, entry := range afterManifest.Exceptions {
		label := fmt.Sprintf("entry %s:%s", entry.Rule, entry.Target())
		assertExceptionFields(t, label, entry, "D", "2027-01-20", true)
	}
	if err := config.ValidateExceptions(afterManifest.Exceptions, fixedNow); err != nil {
		t.Fatalf("refreshed exceptions failed audit: %v", err)
	}
}

// 1b: hand-written entry without category (rule HISS-11, reason not from the renderer): refresh leaves it unchanged, audit still fails it.
func TestAdoptRefresh_PreservesHandwrittenUncategorisedException(t *testing.T) {
	repoPath := t.TempDir()
	manifestContent := `---
version: 1
repository:
  owner: acme
  name: handwritten-exception
profiles:
  - framework
exceptions:
  - rule: HISS-11
    path: .github/workflows/docker-publish.yml
    reason: "adoption found the release workflows short of the declared supply chain (SLSA Build Level 3 declared, Level 2 measured); raise them as docs/guides/releasing.md in Praetor describes, then remove this entry"
    expires: "2027-01-20"
  - rule: HISS-11
    path: .github/workflows/custom.yml
    reason: "manual exception for custom release workflow awaiting operator review"
    expires: "2027-01-20"
`
	manifestPath := filepath.Join(repoPath, manifestFile)
	mustWrite(t, manifestPath, manifestContent)
	writeTestLockAndCatalog(t, repoPath, "framework")

	s := &adoptSession{
		repoPath: repoPath,
		report:   &AdoptReport{},
		opts:     AdoptOptions{Path: repoPath},
	}
	if err := s.syncDeclaredLintersExceptions(t.Context(), manifestPath, nil); err != nil {
		t.Fatalf("syncDeclaredLintersExceptions: %v", err)
	}

	afterManifest := refreshManifest(t, manifestPath)
	if len(afterManifest.Exceptions) != 2 {
		t.Fatalf("expected 2 exceptions, got %d", len(afterManifest.Exceptions))
	}

	assertExceptionFields(t, "owned", afterManifest.Exceptions[0], "D", "2027-01-20", true)
	assertExceptionFields(t, "handwritten", afterManifest.Exceptions[1], "", "2027-01-20", false)

	fixedNow := categoriseFixture()
	err := config.ValidateExceptions(afterManifest.Exceptions, fixedNow)
	if err == nil || !strings.Contains(err.Error(), "category is required") {
		t.Fatalf("audit expected to fail on hand-written entry with 'category is required', got: %v", err)
	}
	if err := config.ValidateExceptions([]config.Exception{afterManifest.Exceptions[0]}, fixedNow); err != nil {
		t.Fatalf("owned entry alone failed audit: %v", err)
	}
}

// 1c: entry whose expires lies beyond entered plus the category D days: refresh sets category D but keeps the entry failing (do not move expires); test names that outcome.
func TestAdoptRefresh_FarExpiryCategorisedStillFailsAudit(t *testing.T) {
	repoPath := t.TempDir()
	manifestContent := `---
version: 1
repository:
  owner: acme
  name: far-expiry-exception
profiles:
  - framework
exceptions:
  - rule: HISS-11
    path: .github/workflows/docker-publish.yml
    reason: "adoption found the release workflows short of the declared supply chain (SLSA Build Level 3 declared, Level 2 measured); raise them as docs/guides/releasing.md in Praetor describes, then remove this entry"
    expires: "2028-01-20"
`
	manifestPath := filepath.Join(repoPath, manifestFile)
	mustWrite(t, manifestPath, manifestContent)
	writeTestLockAndCatalog(t, repoPath, "framework")

	s := &adoptSession{
		repoPath: repoPath,
		report:   &AdoptReport{},
		opts:     AdoptOptions{Path: repoPath},
	}
	if err := s.syncDeclaredLintersExceptions(t.Context(), manifestPath, nil); err != nil {
		t.Fatalf("syncDeclaredLintersExceptions: %v", err)
	}

	afterData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read refreshed manifest: %v", err)
	}
	afterManifest, err := config.DecodeManifest(afterData)
	if err != nil {
		t.Fatalf("decode refreshed manifest: %v", err)
	}

	if len(afterManifest.Exceptions) != 1 {
		t.Fatalf("expected 1 exception, got %d", len(afterManifest.Exceptions))
	}

	entry := afterManifest.Exceptions[0]
	if entry.Category != "D" {
		t.Errorf("entry %s:%s category = %q, want D", entry.Rule, entry.Target(), entry.Category)
	}
	if entry.Entered == "" {
		t.Errorf("entry %s:%s entered is empty, want set", entry.Rule, entry.Target())
	}
	if entry.Expires != "2028-01-20" {
		t.Errorf("entry %s:%s expires = %q, want 2028-01-20 (unchanged, not moved)", entry.Rule, entry.Target(), entry.Expires)
	}

	fixedNow := categoriseFixture()
	// Category D allows 120 days after entered. Expiry in 2028 exceeds 120 days from the
	// fixture date (2026-11-06). Audit must still fail on expiry bound even after category D is set.
	err = config.ValidateExceptions(afterManifest.Exceptions, fixedNow)
	if err == nil || !strings.Contains(err.Error(), "category D requires expires on or before") {
		t.Fatalf("audit expected to fail with category D expiry bound, got: %v", err)
	}
}

// categoriseFixture returns the reference timestamp shared by the unit tests.
func categoriseFixture() time.Time {
	return time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
}

// TestCategoriseOwnedExceptions_Empty verifies that nil input returns no exceptions and no change.
func TestCategoriseOwnedExceptions_Empty(t *testing.T) {
	got, changed := categoriseOwnedExceptions(nil, categoriseFixture())
	if len(got) != 0 || changed {
		t.Errorf("got len %d changed %v; want 0 false", len(got), changed)
	}
}

// TestCategoriseOwnedExceptions_AlreadyCategorised verifies that an exception with a category is left unchanged.
func TestCategoriseOwnedExceptions_AlreadyCategorised(t *testing.T) {
	in := config.Exception{
		Rule: config.ExceptionRuleSupplyChain, Category: "C",
		Reason: supplyChainExceptionReasonPrefix + "(gap)", Expires: "2026-11-16",
	}
	got, changed := categoriseOwnedExceptions([]config.Exception{in}, categoriseFixture())
	if changed || got[0].Category != "C" {
		t.Errorf("category %q changed %v; want C false", got[0].Category, changed)
	}
}

// TestCategoriseOwnedExceptions_PreservesExistingEntered verifies that an existing entered date is kept when a category is assigned.
func TestCategoriseOwnedExceptions_PreservesExistingEntered(t *testing.T) {
	in := config.Exception{
		Rule: config.ExceptionRuleSupplyChain, Reason: supplyChainExceptionReasonPrefix + "(gap)",
		Entered: "2026-09-01", Expires: "2026-12-30",
	}
	got, changed := categoriseOwnedExceptions([]config.Exception{in}, categoriseFixture())
	if !changed || got[0].Category != "D" || got[0].Entered != "2026-09-01" {
		t.Errorf("category %q entered %q changed %v", got[0].Category, got[0].Entered, changed)
	}
}

// TestCategoriseOwnedExceptions_UnrelatedRuleUnchanged verifies that a non-matching rule is not categorised.
func TestCategoriseOwnedExceptions_UnrelatedRuleUnchanged(t *testing.T) {
	in := config.Exception{Rule: "HISS-10", Reason: "unrelated", Expires: "2027-01-20"}
	got, changed := categoriseOwnedExceptions([]config.Exception{in}, categoriseFixture())
	if changed || got[0].Category != "" {
		t.Errorf("category %q changed %v; want empty false", got[0].Category, changed)
	}
}

// TestCategoriseOwnedExceptions_DeclaredLinterGetsD verifies that a declared-linter exception receives category D.
func TestCategoriseOwnedExceptions_DeclaredLinterGetsD(t *testing.T) {
	in := config.Exception{
		Rule: config.ExceptionRuleDeclaredLinters, Tool: "cppcheck",
		Reason:  declaredLinterExceptionReasonPrefix + "cppcheck declared by profile native-gpu-systems",
		Expires: "2027-01-20",
	}
	got, changed := categoriseOwnedExceptions([]config.Exception{in}, categoriseFixture())
	if !changed || got[0].Category != "D" || got[0].Entered != "2026-11-06" {
		t.Errorf("category %q entered %q changed %v", got[0].Category, got[0].Entered, changed)
	}
}
