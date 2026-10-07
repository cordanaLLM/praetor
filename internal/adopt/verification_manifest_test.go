// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// The audit runs in the hooks and CI jobs adoption writes with no --verification-max-* flag, so a
// bound raised only on the command line never reached it, and a repository past the default
// failed its Paperclip gate with no remedy those runs could take (#321). The manifest's
// verification section records the bound for every run; a flag still overrides it for one run.

// manifestHint is the remedy an exceeded entry bound names for every run.
const manifestHint = "or declare verification.max_entries in .standards.yaml so every run"

// declaredGoRepo is largeGoRepo whose .standards.yaml declares verification.max_entries bound,
// filled to exactly entries walk entries: go.mod, src/ and .standards.yaml, plus the files in src/.
func declaredGoRepo(t *testing.T, root string, entries, bound int) string {
	t.Helper()
	mustWrite(t, filepath.Join(root, manifestFile), fmt.Sprintf("version: 1\nverification:\n  max_entries: %d\n", bound))
	return largeGoRepo(t, root, entries-3)
}

// TestResolveVerificationLimits_Positive: a declared bound raises the default, a flag overrides the
// bound it names and keeps the others the manifest declares, and an undeclared bound keeps its
// default.
func TestResolveVerificationLimits_Positive(t *testing.T) {
	defaults := DefaultVerificationLimits()
	declared := &config.VerificationPolicy{MaxEntries: 131072, MaxFiles: 256}
	got := ResolveVerificationLimits(declared, nil)
	want := defaults
	want.MaxEntries, want.MaxFiles = 131072, 256
	if got == nil || *got != want {
		t.Fatalf("declared only = %+v; want %+v", got, want)
	}
	got = ResolveVerificationLimits(declared, &VerificationLimits{MaxDepth: 40, MaxFiles: 300})
	want.MaxDepth, want.MaxFiles = 40, 300
	if got == nil || *got != want {
		t.Fatalf("declared under flags = %+v; want %+v", got, want)
	}
	if _, err := NormalizeVerificationLimits(got); err != nil {
		t.Fatalf("a resolved in-range bound is refused: %v", err)
	}
}

// TestResolveVerificationLimits_Negative: a negative flag survives the resolution, so the walk's
// validation refuses it naming its flag instead of falling back to the declared bound.
func TestResolveVerificationLimits_Negative(t *testing.T) {
	got := ResolveVerificationLimits(&config.VerificationPolicy{MaxEntries: 131072}, &VerificationLimits{MaxEntries: -1})
	if got == nil || got.MaxEntries != -1 {
		t.Fatalf("negative flag over a declared bound = %+v; want it kept for refusal", got)
	}
	if _, err := NormalizeVerificationLimits(got); err == nil || !strings.Contains(err.Error(), "max_entries (--verification-max-entries) must be 1..200000") {
		t.Fatalf("negative flag = %v; want a refusal naming the flag", err)
	}
}

// TestResolveVerificationLimits_Boundary: nothing declared or raised keeps the defaults (nil), an
// empty section resolves to the defaults, and limits that set every bound, as a dogfood suite's do,
// override every declared one.
func TestResolveVerificationLimits_Boundary(t *testing.T) {
	if got := ResolveVerificationLimits(nil, nil); got != nil {
		t.Fatalf("nothing declared or raised = %+v; want nil", got)
	}
	if got := ResolveVerificationLimits(&config.VerificationPolicy{}, nil); got == nil || *got != DefaultVerificationLimits() {
		t.Fatalf("empty section = %+v; want the defaults", got)
	}
	full := VerificationLimits{MaxEntries: 10, MaxFiles: 11, MaxDepth: 12, MaxFileBytes: 13, MaxTotalBytes: 14}
	if got := ResolveVerificationLimits(&config.VerificationPolicy{MaxEntries: 1000, MaxFiles: 100, MaxDepth: 50}, &full); got == nil || *got != full {
		t.Fatalf("full limits over a declared section = %+v; want %+v", got, full)
	}
}

// TestDeclaredVerification_Positive_EveryWalkReadsTheManifest: with no flag, the bound the
// manifest declares reaches the harness facts walk the audit runs and the adoption walk, whose
// plan carries it on to the editor scan.
func TestDeclaredVerification_Positive_EveryWalkReadsTheManifest(t *testing.T) {
	root := declaredGoRepo(t, t.TempDir(), fixtureEntries+1, fixtureEntries+8)
	if got, _, err := RepositoryHISSFacts(t.Context(), root, nil); err != nil || got.Languages != hisscatalog.LanguageGo {
		t.Fatalf("RepositoryHISSFacts under the declared bound = %+v, %v; want Go", got, err)
	}
	repo := declaredGoRepo(t, newTestRepo(t, "large-widget"), 2*fixtureEntries, 8*fixtureEntries)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, DryRun: true})
	if err != nil {
		t.Fatalf("adoption under the declared bound failed: %v", err)
	}
	if rep.Verification == nil || rep.Verification.Limits == nil || rep.Verification.Limits.MaxEntries != 8*fixtureEntries {
		t.Fatalf("the verification plan did not carry the declared bound: %+v", rep.Verification)
	}
	if !contains(rep.CreatedFiles, ".vscode/settings.json") {
		t.Fatalf("the editor step did not run under the declared bound: created %v", rep.CreatedFiles)
	}
}

// TestDeclaredVerification_Negative_ExceededBoundNamesTheKey: past the declared bound the walk
// stops and names both remedies, a flag raises it for its own run, and a declared value past
// its ceiling is refused naming the key before any walk.
func TestDeclaredVerification_Negative_ExceededBoundNamesTheKey(t *testing.T) {
	root := declaredGoRepo(t, t.TempDir(), fixtureEntries, fixtureEntries-1)
	_, _, err := RepositoryHISSFacts(t.Context(), root, nil)
	if err == nil || !strings.Contains(err.Error(), entriesHint) || !strings.Contains(err.Error(), manifestHint) {
		t.Fatalf("walk past the declared bound = %v; want it to name %q and %q", err, entriesHint, manifestHint)
	}
	if _, _, err := RepositoryHISSFacts(t.Context(), root, &VerificationLimits{MaxEntries: fixtureEntries}); err != nil {
		t.Fatalf("a flag raising the declared bound is ignored: %v", err)
	}
	repo := declaredGoRepo(t, newTestRepo(t, "bounded-widget"), 2*fixtureEntries, fixtureEntries)
	if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, DryRun: true}); err == nil || !strings.Contains(err.Error(), manifestHint) {
		t.Fatalf("adoption past the declared bound = %v; want it to name %q", err, manifestHint)
	}
	over := t.TempDir()
	mustWrite(t, filepath.Join(over, manifestFile), "version: 1\nverification:\n  max_entries: 200001\n")
	if _, _, err := RepositoryHISSFacts(t.Context(), over, nil); err == nil || !strings.Contains(err.Error(), "verification.max_entries must be an integer from 1 to 200000") {
		t.Fatalf("over-ceiling declaration = %v; want a refusal naming the key", err)
	}
}

// TestDeclaredVerification_Boundary_BoundIsExact: exactly the declared number of entries passes.
func TestDeclaredVerification_Boundary_BoundIsExact(t *testing.T) {
	root := declaredGoRepo(t, t.TempDir(), fixtureEntries, fixtureEntries)
	if _, _, err := RepositoryHISSFacts(t.Context(), root, nil); err != nil {
		t.Fatalf("exactly %d declared entries refused: %v", fixtureEntries, err)
	}
}
