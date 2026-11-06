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

func writeTestLockAndCatalog(t *testing.T, repo, profileID string) {
	t.Helper()
	source := fmt.Sprintf("id: %q\nname: %q\n", profileID, profileID)
	mustWrite(t, filepath.Join(repo, ".config", "archetypes", profileID+".yaml"), source)
	testsupport.WritePinnedLock(t, repo, profileID, source)
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
	today := time.Now()
	if err := config.ValidateExceptions(preManifest.Exceptions, today); err == nil {
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

	afterData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read refreshed manifest: %v", err)
	}
	afterManifest, err := config.DecodeManifest(afterData)
	if err != nil {
		t.Fatalf("decode refreshed manifest: %v", err)
	}

	if len(afterManifest.Exceptions) != 2 {
		t.Fatalf("expected 2 exceptions, got %d", len(afterManifest.Exceptions))
	}

	for _, entry := range afterManifest.Exceptions {
		if entry.Category != "D" {
			t.Errorf("entry %s:%s category = %q, want D", entry.Rule, entry.Target(), entry.Category)
		}
		if entry.Entered == "" {
			t.Errorf("entry %s:%s entered is empty, want set", entry.Rule, entry.Target())
		}
		if entry.Expires != "2027-01-20" {
			t.Errorf("entry %s:%s expires = %q, want 2027-01-20 (unchanged)", entry.Rule, entry.Target(), entry.Expires)
		}
	}

	if err := config.ValidateExceptions(afterManifest.Exceptions, today); err != nil {
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

	afterData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read refreshed manifest: %v", err)
	}
	afterManifest, err := config.DecodeManifest(afterData)
	if err != nil {
		t.Fatalf("decode refreshed manifest: %v", err)
	}

	if len(afterManifest.Exceptions) != 2 {
		t.Fatalf("expected 2 exceptions, got %d", len(afterManifest.Exceptions))
	}

	owned := afterManifest.Exceptions[0]
	if owned.Category != "D" {
		t.Errorf("owned entry %s:%s category = %q, want D", owned.Rule, owned.Target(), owned.Category)
	}
	if owned.Entered == "" {
		t.Errorf("owned entry %s:%s entered is empty, want set", owned.Rule, owned.Target())
	}
	if owned.Expires != "2027-01-20" {
		t.Errorf("owned entry %s:%s expires = %q, want 2027-01-20", owned.Rule, owned.Target(), owned.Expires)
	}

	handwritten := afterManifest.Exceptions[1]
	if handwritten.Category != "" {
		t.Errorf("hand-written entry %s:%s category = %q, want empty (untouched)", handwritten.Rule, handwritten.Target(), handwritten.Category)
	}
	if handwritten.Entered != "" {
		t.Errorf("hand-written entry %s:%s entered = %q, want empty (untouched)", handwritten.Rule, handwritten.Target(), handwritten.Entered)
	}
	if handwritten.Expires != "2027-01-20" {
		t.Errorf("hand-written entry %s:%s expires = %q, want 2027-01-20 (untouched)", handwritten.Rule, handwritten.Target(), handwritten.Expires)
	}

	today := time.Now()
	// Full manifest audit must still fail because the hand-written entry lacks category.
	err = config.ValidateExceptions(afterManifest.Exceptions, today)
	if err == nil || !strings.Contains(err.Error(), "category is required") {
		t.Fatalf("audit expected to fail on hand-written entry with 'category is required', got: %v", err)
	}

	// Owned entry alone must pass audit once categorised.
	if err := config.ValidateExceptions([]config.Exception{owned}, today); err != nil {
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

	today := time.Now()
	// Category D allows 120 days after entered. Expiry in 2028 exceeds 120 days from today.
	// Audit must still fail on expiry bound even after category D is set.
	err = config.ValidateExceptions(afterManifest.Exceptions, today)
	if err == nil || !strings.Contains(err.Error(), "category D requires expires on or before") {
		t.Fatalf("audit expected to fail with category D expiry bound, got: %v", err)
	}
}

// Unit test: direct coverage of categoriseOwnedExceptions edge cases.
func TestCategoriseOwnedExceptions_Unit(t *testing.T) {
	entered := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	expectedDate := "2026-11-06"

	// Empty slice returns unchanged, false.
	got, changed := categoriseOwnedExceptions(nil, entered)
	if len(got) != 0 || changed {
		t.Errorf("empty slice: got len %d, changed %v; want len 0, false", len(got), changed)
	}

	// Already categorised entry is not modified.
	alreadyCat := config.Exception{
		Rule:     config.ExceptionRuleSupplyChain,
		Category: "C",
		Reason:   supplyChainExceptionReasonPrefix + "(gap)",
		Expires:  "2026-11-16",
	}
	got, changed = categoriseOwnedExceptions([]config.Exception{alreadyCat}, entered)
	if changed || got[0].Category != "C" {
		t.Errorf("already categorised entry modified: got category %q, changed %v", got[0].Category, changed)
	}

	// Entry with existing entered date preserves that entered date.
	withEntered := config.Exception{
		Rule:    config.ExceptionRuleSupplyChain,
		Reason:  supplyChainExceptionReasonPrefix + "(gap)",
		Entered: "2026-09-01",
		Expires: "2026-12-30",
	}
	got, changed = categoriseOwnedExceptions([]config.Exception{withEntered}, entered)
	if !changed || got[0].Category != "D" || got[0].Entered != "2026-09-01" {
		t.Errorf("expected category D and preserved entered 2026-09-01, got category %q entered %q", got[0].Category, got[0].Entered)
	}

	// Unrelated rule is not modified.
	unrelated := config.Exception{
		Rule:    "HISS-10",
		Reason:  "unrelated",
		Expires: "2027-01-20",
	}
	got, changed = categoriseOwnedExceptions([]config.Exception{unrelated}, entered)
	if changed || got[0].Category != "" {
		t.Errorf("unrelated entry modified: got category %q, changed %v", got[0].Category, changed)
	}

	// Declared linters entry gets category D and entered.
	linter := config.Exception{
		Rule:    config.ExceptionRuleDeclaredLinters,
		Tool:    "cppcheck",
		Reason:  declaredLinterExceptionReasonPrefix + "cppcheck declared by profile native-gpu-systems",
		Expires: "2027-01-20",
	}
	got, changed = categoriseOwnedExceptions([]config.Exception{linter}, entered)
	if !changed || got[0].Category != "D" || got[0].Entered != expectedDate {
		t.Errorf("linter entry: got category %q, entered %q, changed %v", got[0].Category, got[0].Entered, changed)
	}
}
