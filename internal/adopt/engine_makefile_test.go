// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: an own Makefile with the main-era appended block and the docs block:
// after adopt, `make praetor-engine-path` resolves to the pinned launcher path.
// A rerun changes nothing and warns nothing (#906).
// A rerun changes nothing and warns nothing (#906).
func TestEngineMakefile_Positive_OwnMakefileMainEraAppendedBlockResolvesToPinnedLauncher(t *testing.T) {
	root, wantPath := setupPinnedEngineRepo(t, "engine-mk-own-makefile")
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}

	writeMainEraAppendedMakefile(t, root, plan)

	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	report1, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}

	verifyMakePraetorEnginePath(t, root, wantPath)

	report2, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	assertNoMakefileWarningsOnRerun(t, report1, report2)
}

func setupPinnedEngineRepo(t *testing.T, name string) (string, string) {
	t.Helper()
	root := newTestRepo(t, name)
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	mustWrite(t, filepath.Join(root, ".github", "workflows", "gate.yml"), "env:\n  PRAETOR_REF: "+enginePin+"\n")

	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	pinDir := filepath.Join(cacheDir, "praetor", "engine", enginePin)
	if err := os.MkdirAll(pinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStub(t, pinDir, "standardsctl", "echo \"pinned-standardsctl $*\"\n")
	return root, filepath.Join(pinDir, "standardsctl")
}

func writeMainEraAppendedMakefile(t *testing.T, root string, plan *VerificationPlan) {
	t.Helper()
	ownMakefile := "all: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
	appended, err := appendVerificationTargets(ownMakefile, plan)
	if err != nil {
		t.Fatal(err)
	}
	withDocs, err := mergeDocumentationMakefile(appended, false)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "Makefile"), withDocs)
}

func verifyMakePraetorEnginePath(t *testing.T, root, wantPath string) {
	t.Helper()
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is absent; skipping make execution (HISS-21)")
	}
	out, err := util.RunCommand(t.Context(), root, makePath, "--no-print-directory", "praetor-engine-path")
	if err != nil {
		t.Fatalf("make praetor-engine-path failed: %v, output: %q", err, out)
	}
	if strings.TrimSpace(out) != wantPath {
		t.Fatalf("praetor-engine-path = %q, want %q", strings.TrimSpace(out), wantPath)
	}
}

func assertNoMakefileWarningsOnRerun(t *testing.T, report1, report2 *AdoptReport) {
	t.Helper()
	for _, w := range report2.Warnings {
		if isMakefileWarning(w) {
			t.Fatalf("rerun emitted unexpected Makefile warning: %s", w)
		}
	}
	entry2 := findLastAction(report2.ActionDetails, makefileName)
	if entry2 == nil || entry2.Action == actionAppend || entry2.Action == actionReplace {
		t.Fatalf("run 2 reported action %v, want reconcile or no-op", entry2)
	}
	if report2.Verification.Status != report1.Verification.Status {
		t.Fatalf("verification status changed on rerun: %v -> %v", report1.Verification.Status, report2.Verification.Status)
	}
}

// Positive: a greenfield Makefile includes engine.mk (#906).
func TestEngineMakefile_Positive_GreenfieldMakefileIncludesEngineMk(t *testing.T) {
	root := newTestRepo(t, "engine-mk-greenfield")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	assertNoIssues(t, report)

	makefile := mustRead(t, filepath.Join(root, "Makefile"))
	if !strings.Contains(makefile, "-include .config/praetor/engine.mk") {
		t.Fatalf("greenfield Makefile does not include engine.mk:\n%s", makefile)
	}
	engineMk := mustRead(t, filepath.Join(root, filepath.FromSlash(".config/praetor/engine.mk")))
	if !strings.Contains(engineMk, "PRAETORCTL ?=") || !strings.Contains(engineMk, "praetor-engine-path:") {
		t.Fatalf(".config/praetor/engine.mk is missing expected targets:\n%s", engineMk)
	}
}

// Positive: an explicit := override is kept and reported (#906).
func TestEngineMakefile_Positive_ExplicitAssignmentOverrideKeptAndReported(t *testing.T) {
	root := newTestRepo(t, "engine-mk-override")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	ownMakefile := "# Project Makefile\nPRAETORCTL := /custom/bin/my-praetor\nall: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
	mustWrite(t, filepath.Join(root, "Makefile"), ownMakefile)

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, "Makefile"))
	if !strings.Contains(got, "PRAETORCTL := /custom/bin/my-praetor") {
		t.Fatalf("explicit := override was not kept:\n%s", got)
	}
	if !slices.ContainsFunc(report.Warnings, func(w string) bool {
		return strings.Contains(w, "Makefile line 2") && strings.Contains(w, "PRAETORCTL := /custom/bin/my-praetor") && strings.Contains(w, "overrides PRAETORCTL")
	}) {
		t.Fatalf("expected warning naming line 2 override, got: %v", report.Warnings)
	}

	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is absent; skipping make execution (HISS-21)")
	}
	out, err := util.RunCommand(t.Context(), root, makePath, "--no-print-directory", "praetor-engine-path")
	if err != nil {
		t.Fatalf("make praetor-engine-path failed: %v, output: %q", err, out)
	}
	if strings.TrimSpace(out) != "/custom/bin/my-praetor" {
		t.Fatalf("praetor-engine-path = %q, want /custom/bin/my-praetor", strings.TrimSpace(out))
	}
}

// Boundary: a hand-edited engine.mk is kept (#906).
func TestEngineMakefile_Boundary_HandEditedEngineMkKept(t *testing.T) {
	root := newTestRepo(t, "engine-mk-hand-edited")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")

	// First adopt creates engine.mk
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}

	engineMkPath := filepath.Join(root, filepath.FromSlash(".config/praetor/engine.mk"))
	customEngineMk := "# Hand-edited custom engine.mk\nPRAETORCTL ?= custom-praetor\n\n.PHONY: praetor-engine-path\npraetor-engine-path:\n\t@echo $(PRAETORCTL)\n"
	mustWrite(t, engineMkPath, customEngineMk)

	// Second adopt (with Force: true to prove it is kept like other un-locked scaffolds)
	opts.Force = true
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, engineMkPath); got != customEngineMk {
		t.Fatalf("hand-edited engine.mk was overwritten:\n%s", got)
	}
	if !slices.ContainsFunc(report.Warnings, func(w string) bool {
		return strings.Contains(w, ".config/praetor/engine.mk")
	}) {
		t.Fatalf("expected warning that engine.mk was preserved, got: %v", report.Warnings)
	}
}

// Boundary: the placeholder case behaves as on main (#594).
func TestEngineMakefile_Boundary_PlaceholderCaseBehavesAsOnMain(t *testing.T) {
	root := newTestRepo(t, "engine-mk-placeholder")
	ownMakefile := "all: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
	placeholderPlan := &VerificationPlan{Status: verificationUnavailable}
	appended, err := appendVerificationTargets(ownMakefile, placeholderPlan)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "Makefile"), appended)
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Verification.Status != verificationUnavailable {
		t.Fatalf("status = %v, want %v", report.Verification.Status, verificationUnavailable)
	}
	wantReason := "The Makefile still holds the failing placeholder recipe adoption writes; replace it with the project's build and test commands."
	if !slices.Contains(report.Verification.Reasons, wantReason) {
		t.Fatalf("expected reasons to contain %q, got: %v", wantReason, report.Verification.Reasons)
	}
}

// Boundary: CRLF checkout of current lefthook.yml without engine.sh is idempotent and emits zero warnings (MINOR 3, MINOR 8).
func TestEngineMakefile_Boundary_CRLFCheckoutRerunIdempotent(t *testing.T) {
	root := newTestRepo(t, "engine-mk-crlf-rerun")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	ownMakefile := "all: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
	mustWrite(t, filepath.Join(root, "Makefile"), ownMakefile)

	// Write current lefthook.yml in CRLF format
	currentLefthook := buildLefthookYAML()
	crlfLefthook := strings.ReplaceAll(currentLefthook, "\n", "\r\n")
	mustWrite(t, filepath.Join(root, "lefthook.yml"), crlfLefthook)

	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	// Run 1: initial adopt
	report1, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range report1.Warnings {
		if isMakefileWarning(w) {
			t.Fatalf("run 1 emitted unexpected Makefile warning: %s", w)
		}
	}

	// Run 2: re-run must be completely idempotent and warn nothing about Makefile
	report2, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range report2.Warnings {
		if isMakefileWarning(w) {
			t.Fatalf("run 2 emitted unexpected Makefile warning: %s", w)
		}
	}
}

// The digest set records the current engine.mk (HISS-20), so a later change still refreshes it.
func TestPriorEngineMakefileDigests_Boundary_CurrentTextRecorded(t *testing.T) {
	digest, _, err := util.CanonicalTextDigest([]byte(engineMakefileContent))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("engineMakefileContent digest: %s", digest)
	if _, ok := priorEngineMakefileDigests[digest]; !ok {
		t.Fatalf("the current engine.mk (%s) is not recorded in priorEngineMakefileDigests", digest)
	}
}

func isMakefileWarning(w string) bool {
	return strings.HasPrefix(w, "Makefile") || strings.Contains(w, "verification block") || strings.Contains(w, "overrides PRAETORCTL")
}

func findLastAction(details []ActionDetail, path string) *ActionDetail {
	var found *ActionDetail
	for i := range details {
		if details[i].Path == path {
			found = &details[i]
		}
	}
	return found
}
