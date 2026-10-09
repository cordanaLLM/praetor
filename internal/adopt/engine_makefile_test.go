// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"errors"
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

// Negative: when git-hooks is declined, engine.mk is neither created nor included,
// and make compile-context runs the PATH stub without failing (MAJOR 1).
func TestEngineMakefile_Negative_DeclinedGitHooksRunsStubFromPath(t *testing.T) {
	root := newTestRepo(t, "declined-hooks-stub")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	mustWrite(t, filepath.Join(root, ".standards.yaml"), "version: 1\nadoption:\n  decline:\n    - git-hooks\n")

	stubDir := t.TempDir()
	writeStub(t, stubDir, "praetorctl", "echo \"path-praetorctl $*\"\n")
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	assertNoIssues(t, report)

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(engineMakefile))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("engine.mk was installed when git-hooks was declined: %v", err)
	}
	makefile := mustRead(t, filepath.Join(root, "Makefile"))
	if strings.Contains(makefile, engineMakefileIncludeLine) {
		t.Fatalf("Makefile included engine.mk when git-hooks was declined:\n%s", makefile)
	}

	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is absent; skipping make execution (HISS-21)")
	}
	out, err := util.RunCommand(t.Context(), root, makePath, "--no-print-directory", "compile-context")
	if err != nil {
		t.Fatalf("make compile-context failed: %v, output: %q", err, out)
	}
	if !strings.Contains(out, "path-praetorctl compile-context") {
		t.Fatalf("expected PATH stub output, got: %q", out)
	}
}

// Negative: when a custom lefthook.yml is kept, engine.mk is neither created nor included,
// and make compile-context runs the PATH stub without failing (MAJOR 1).
func TestEngineMakefile_Negative_KeptCustomLefthookRunsStubFromPath(t *testing.T) {
	root := newTestRepo(t, "kept-custom-lefthook-stub")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	mustWrite(t, filepath.Join(root, "lefthook.yml"), "pre-commit:\n  commands:\n    custom:\n      run: echo custom\n")

	stubDir := t.TempDir()
	writeStub(t, stubDir, "praetorctl", "echo \"path-praetorctl $*\"\n")
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(report.Warnings, func(w string) bool {
		return strings.Contains(w, "existing lefthook.yml differs")
	}) {
		t.Fatalf("expected warning that custom lefthook.yml is kept, got: %v", report.Warnings)
	}

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(engineMakefile))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("engine.mk was installed when custom lefthook was kept: %v", err)
	}
	makefile := mustRead(t, filepath.Join(root, "Makefile"))
	if strings.Contains(makefile, engineMakefileIncludeLine) {
		t.Fatalf("Makefile included engine.mk when custom lefthook was kept:\n%s", makefile)
	}

	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is absent; skipping make execution (HISS-21)")
	}
	out, err := util.RunCommand(t.Context(), root, makePath, "--no-print-directory", "compile-context")
	if err != nil {
		t.Fatalf("make compile-context failed: %v, output: %q", err, out)
	}
	if !strings.Contains(out, "path-praetorctl compile-context") {
		t.Fatalf("expected PATH stub output, got: %q", out)
	}
}

// Negative: a Makefile with inconsistent line endings is refused rather than silently rewritten (MINOR 3).
func TestEngineMakefile_Negative_InconsistentLineEndingsRefused(t *testing.T) {
	root := newTestRepo(t, "inconsistent-endings")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	mixedMakefile := "# head\r\nall: build\nbuild:\n\t@cargo build\r\n"
	mustWrite(t, filepath.Join(root, "Makefile"), mixedMakefile)

	_, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err == nil || !strings.Contains(err.Error(), "makefile line endings are inconsistent") {
		t.Fatalf("expected inconsistent line endings error, got: %v", err)
	}
}

// Positive: assignment shapes ?=, !=, +=, and define PRAETORCTL are detected and reported (MINOR 4).
func TestEngineMakefile_Positive_AllAssignmentOverrideShapesDetectedAndReported(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantSubstr string
	}{
		{
			name:       "conditional",
			line:       "PRAETORCTL ?= /opt/team/praetorctl",
			wantSubstr: "defines PRAETORCTL with ?=; shadowed by .config/praetor/engine.mk",
		},
		{
			name:       "shell assignment",
			line:       "PRAETORCTL != which praetorctl",
			wantSubstr: "overrides PRAETORCTL; adopter override is respected",
		},
		{
			name:       "append",
			line:       "PRAETORCTL += --flag",
			wantSubstr: "overrides PRAETORCTL; adopter override is respected",
		},
		{
			name:       "define block",
			line:       "define PRAETORCTL",
			wantSubstr: "overrides PRAETORCTL; adopter override is respected",
		},
		{
			name:       "override prefix",
			line:       "override PRAETORCTL := /opt/team/praetorctl",
			wantSubstr: "overrides PRAETORCTL; adopter override is respected",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := newTestRepo(t, "override-"+tc.name)
			mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
			mustWrite(t, filepath.Join(root, ".standards.yaml"), "version: 1\nfacets: []\n")
			content := "# Project Makefile\n" + tc.line + "\nall: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
			if tc.name == "define block" {
				content = "# Project Makefile\ndefine PRAETORCTL\n/opt/team/praetorctl\nendef\nall: build\nbuild:\n\t@cargo build\ntest:\n\t@cargo test\n"
			}
			mustWrite(t, filepath.Join(root, "Makefile"), content)

			report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(report.Warnings, func(w string) bool {
				return strings.Contains(w, tc.wantSubstr)
			}) {
				t.Fatalf("expected warning containing %q, got: %v", tc.wantSubstr, report.Warnings)
			}
		})
	}
}

// Negative: a non-pin PRAETOR_REF fails closed with exit code 2 and refuses (MINOR 5).
func TestEngineMakefile_Negative_NonPinRefFailsClosed(t *testing.T) {
	root := newTestRepo(t, "non-pin-ref-fails")
	mustWrite(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = 'fixture'\nversion = '0.1.0'\n")
	mustWrite(t, filepath.Join(root, ".github", "workflows", "gate.yml"), "env:\n  PRAETOR_REF: main\n")

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	assertNoIssues(t, report)

	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is absent; skipping make execution (HISS-21)")
	}
	out, err := util.RunCommand(t.Context(), root, makePath, "--no-print-directory", "praetor-engine-path")
	if err == nil {
		t.Fatalf("make praetor-engine-path succeeded unexpectedly: output: %q", out)
	}
	if !strings.Contains(err.Error(), "exit status 2") {
		t.Fatalf("expected exit status 2, got: %v", err)
	}
}

// Boundary: if .config/lefthook/engine.sh is later removed, make falls back to PATH (MAJOR 1).
func TestEngineMakefile_Boundary_LauncherRemovedFallsBackToPath(t *testing.T) {
	root, wantPath := setupPinnedEngineRepo(t, "launcher-removed")
	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	assertNoIssues(t, report)

	verifyMakePraetorEnginePath(t, root, wantPath)

	// Remove launcher script
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(engineLauncherFile))); err != nil {
		t.Fatal(err)
	}

	stubDir := t.TempDir()
	writeStub(t, stubDir, "praetorctl", "echo \"path-praetorctl $*\"\n")
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is absent; skipping make execution (HISS-21)")
	}
	out, err := util.RunCommand(t.Context(), root, makePath, "--no-print-directory", "praetor-engine-path")
	if err != nil {
		t.Fatalf("make praetor-engine-path failed after launcher removal: %v, output: %q", err, out)
	}
	if strings.TrimSpace(out) != filepath.Join(stubDir, "praetorctl") {
		t.Fatalf("praetor-engine-path = %q, want %q", strings.TrimSpace(out), filepath.Join(stubDir, "praetorctl"))
	}
}

// Positive: a generated Makefile runs its first real target when make runs with no goal (#906).
func TestEngineMakefile_Positive_GeneratedMakefileRunsFirstTargetWithoutGoal(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is absent; skipping make execution (HISS-21)")
	}
	root, _ := setupPinnedEngineRepo(t, "default-goal-generated")
	stubs := t.TempDir()
	writeStub(t, stubs, "cargo", "echo GENERATED-CARGO-BUILD-RAN\n")
	t.Setenv("PATH", stubs+string(os.PathListSeparator)+os.Getenv("PATH"))

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	assertNoIssues(t, report)

	out, err := util.RunCommand(t.Context(), root, makePath, "--no-print-directory")
	if err != nil {
		t.Fatalf("plain make failed: %v, output: %q", err, out)
	}
	if !strings.Contains(out, "GENERATED-CARGO-BUILD-RAN") {
		t.Fatalf("plain make did not run first real target; got output: %q", out)
	}
}

// Positive: an adopter Makefile with all: build runs the build when make runs with no goal (#906).
func TestEngineMakefile_Positive_AdopterMakefileRunsBuildWithoutGoal(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is absent; skipping make execution (HISS-21)")
	}
	root, _ := setupPinnedEngineRepo(t, "default-goal-adopter")
	ownMakefile := "# Project\nall: build\nbuild:\n\t@echo OWN-BUILD-RAN\n"
	mustWrite(t, filepath.Join(root, "Makefile"), ownMakefile)

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	assertNoIssues(t, report)

	out, err := util.RunCommand(t.Context(), root, makePath, "--no-print-directory")
	if err != nil {
		t.Fatalf("plain make failed: %v, output: %q", err, out)
	}
	if !strings.Contains(out, "OWN-BUILD-RAN") {
		t.Fatalf("plain make did not run build target; got output: %q", out)
	}
}

// Positive: make praetor-engine-path prints the launcher path (#906).
func TestEngineMakefile_Positive_ExplicitPraetorEnginePathPrintsLauncherPath(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is absent; skipping make execution (HISS-21)")
	}
	root, wantPath := setupPinnedEngineRepo(t, "default-goal-explicit")
	ownMakefile := "# Project\nall: build\nbuild:\n\t@echo OWN-BUILD-RAN\n"
	mustWrite(t, filepath.Join(root, "Makefile"), ownMakefile)

	report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatal(err)
	}
	assertNoIssues(t, report)

	out, err := util.RunCommand(t.Context(), root, makePath, "--no-print-directory", "praetor-engine-path")
	if err != nil {
		t.Fatalf("make praetor-engine-path failed: %v, output: %q", err, out)
	}
	if strings.TrimSpace(out) != wantPath {
		t.Fatalf("praetor-engine-path = %q, want %q", strings.TrimSpace(out), wantPath)
	}
}
