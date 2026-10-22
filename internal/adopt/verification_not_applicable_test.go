// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Negative: a root without build markers must NOT write the failing placeholder recipe (exit 1),
// and must NOT report verification as unavailable. This test proves the defect before the fix.
func TestAdoptRootWithoutBuildMarkerRefusesPlaceholderForm(t *testing.T) {
	root := newTestRepo(t, "no-build-marker-negative")
	rep := adoptPlain(t, root, false)
	if rep.Verification.Status == verificationUnavailable {
		t.Fatalf("negative: verification plan must refuse unavailable status for root without build marker, got: %s", rep.Verification.Status)
	}
	makefile := mustRead(t, filepath.Join(root, makefileName))
	if strings.Contains(makefile, unavailableRecipeLine) || strings.Contains(makefile, unavailableVerificationRecipe) {
		t.Fatalf("negative: Makefile must refuse failing placeholder recipe:\n%s", makefile)
	}
	agents := mustRead(t, filepath.Join(root, agentsFile))
	if strings.Contains(agents, "exit 1") {
		t.Fatalf("negative: AGENTS.md must refuse exit 1 in verification section:\n%s", agents)
	}
}

// Positive: a root without build markers declares build and test targets not applicable,
// verify-all runs the governance checks and exits 0, and AGENTS.md names the not-applicable targets.
func TestAdoptRootWithoutBuildMarkerDeclaresTargetsNotApplicable(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Fatalf("make is required for scaffold execution: %v", err)
	}
	root := newTestRepo(t, "no-build-marker-positive")
	rep := adoptPlain(t, root, false)
	wantReason := "no build system; verify-all checks agent context, caveman sources and audit."
	assertNotApplicableReport(t, rep, wantReason)
	assertNotApplicableMakefile(t, root, wantReason)
	assertNotApplicableAgents(t, root, wantReason)
	assertNotApplicableExecution(t, root, makePath)
}

func assertNotApplicableReport(t *testing.T, rep *AdoptReport, wantReason string) {
	t.Helper()
	if rep.Verification.Status != verificationNotApplicable {
		t.Fatalf("verification status = %s, want %s", rep.Verification.Status, verificationNotApplicable)
	}
	if len(rep.Verification.Reasons) == 0 || !strings.Contains(rep.Verification.Reasons[0], "no build system") {
		t.Fatalf("verification reasons = %v, want mentioning no build system", rep.Verification.Reasons)
	}
	pillar := verificationGatePillar(t, rep)
	if pillar.Status != PillarReady {
		t.Fatalf("Verification Gate pillar = %s, want ready", pillar.Status)
	}
	if slices.Contains(rep.PendingPillars(), "Verification Gate") {
		t.Fatalf("Verification Gate must not be pending: %v", rep.PendingPillars())
	}
	if slices.ContainsFunc(rep.Warnings, func(w string) bool { return strings.Contains(w, "Project verification unavailable") }) {
		t.Fatalf("report must not warn verification unavailable: %v", rep.Warnings)
	}
	if !slices.ContainsFunc(rep.Warnings, func(w string) bool { return strings.Contains(w, "Project verification not-applicable") }) {
		t.Fatalf("report must state verification not-applicable: %v", rep.Warnings)
	}
}

func assertNotApplicableMakefile(t *testing.T, root, wantReason string) {
	t.Helper()
	makefile := mustRead(t, filepath.Join(root, makefileName))
	for _, target := range []string{"compile-context-verify", "caveman-sources", "audit", "test"} {
		if !strings.Contains(makefile, "verify-all:") || !strings.Contains(makefile, target) {
			t.Fatalf("Makefile verify-all must include %s:\n%s", target, makefile)
		}
	}
	if !strings.Contains(makefile, "build: not applicable: "+wantReason) {
		t.Fatalf("Makefile lacks not-applicable build recipe:\n%s", makefile)
	}
	if !strings.Contains(makefile, "test: not applicable: "+wantReason) {
		t.Fatalf("Makefile lacks not-applicable test recipe:\n%s", makefile)
	}
	if strings.Contains(makefile, "exit 1") {
		t.Fatalf("Makefile must not contain exit 1:\n%s", makefile)
	}
}

func assertNotApplicableAgents(t *testing.T, root, wantReason string) {
	t.Helper()
	agents := mustRead(t, filepath.Join(root, agentsFile))
	if strings.Contains(agents, "exit 1") {
		t.Fatalf("AGENTS.md must not contain exit 1:\n%s", agents)
	}
	if !strings.Contains(agents, "# Build and test targets not applicable: "+wantReason) {
		t.Fatalf("AGENTS.md must name not-applicable targets:\n%s", agents)
	}
}

func assertNotApplicableExecution(t *testing.T, root, makePath string) {
	t.Helper()
	stubs := t.TempDir()
	for _, cli := range []string{util.PraetorCLI, util.LegacyCLI} {
		writeStub(t, stubs, cli, "printf '%s\\n' \"$*\" >> calls\n")
	}
	// The adopted Makefile resolves the engine with "sh .config/lefthook/engine.sh", so the
	// launcher's tools must stay reachable once hermeticPath narrows PATH to git's directory,
	// which need not hold them (Praetor's own devcontainer keeps git in /usr/local/bin).
	hermeticPath(t, stubs+string(os.PathListSeparator)+hookToolbox(t))
	out, err := util.RunCommand(context.Background(), root, makePath, "--no-print-directory", "-j1", "verify-all")
	if err != nil {
		t.Fatalf("make verify-all failed: %v\noutput:\n%s", err, out)
	}
	calls := mustRead(t, filepath.Join(root, "calls"))
	if calls != "compile-context --verify\ncaveman check --configured-sources\naudit\n" {
		t.Fatalf("make verify-all calls = %q", calls)
	}
	if !strings.Contains(out, "build: not applicable") || !strings.Contains(out, "test: not applicable") {
		t.Fatalf("make verify-all output did not print not-applicable lines:\n%s", out)
	}
}

// Boundary: a root with a build marker (go.mod, package.json, meson.build) or an existing verify-all
// keeps today's behaviour (declared, unavailable, or preserved).
func TestAdoptRootWithBuildMarkerPreservesUnavailableOrDeclared(t *testing.T) {
	// Go module -> declared
	goRoot := newTestRepo(t, "go-boundary")
	mustWrite(t, filepath.Join(goRoot, "go.mod"), "module fixture\n")
	goRep := adoptPlain(t, goRoot, false)
	if goRep.Verification.Status != verificationDeclared {
		t.Fatalf("go verification status = %s, want declared", goRep.Verification.Status)
	}

	// Meson build without native steps -> unavailable (keeps today's behavior)
	mesonRoot := newTestRepo(t, "meson-boundary")
	mustWrite(t, filepath.Join(mesonRoot, "meson.build"), "project('fixture', 'c')\n")
	mesonRep := adoptPlain(t, mesonRoot, false)
	if mesonRep.Verification.Status != verificationUnavailable {
		t.Fatalf("meson verification status = %s, want unavailable", mesonRep.Verification.Status)
	}

	// Existing custom verify-all -> preserved (keeps today's behavior)
	customRoot := newTestRepo(t, "custom-boundary")
	mustWrite(t, filepath.Join(customRoot, makefileName), ".PHONY: verify-all\nverify-all:\n\t@echo custom-gate\n")
	customRep := adoptPlain(t, customRoot, false)
	if customRep.Verification.Status != verificationPreserved {
		t.Fatalf("custom verification status = %s, want preserved", customRep.Verification.Status)
	}
}

// Planted negative & 3D boundary: a root with source files (scripts/x.py, tools/y.sh, or nested
// deep under a directory) but no build marker must be unavailable naming the detected language,
// never not-applicable.
func TestAdoptRootWithSourceFilesWithoutBuildMarkerUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name     string
		file     string
		wantLang string
	}{
		{"python-script", "scripts/x.py", "python"},
		{"shell-script", "tools/y.sh", "shell"},
		{"nested-source-boundary", "nested/deep/inside/path/helper.py", "python"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newTestRepo(t, tc.name)
			mustWrite(t, filepath.Join(root, tc.file), "print('hello')\n")
			rep := adoptPlain(t, root, false)
			if rep.Verification.Status != verificationUnavailable {
				t.Fatalf("root with %s: verification status = %s, want %s", tc.file, rep.Verification.Status, verificationUnavailable)
			}
			if len(rep.Verification.Reasons) == 0 || !strings.Contains(rep.Verification.Reasons[0], tc.wantLang) {
				t.Fatalf("verification reasons = %v, want naming %s", rep.Verification.Reasons, tc.wantLang)
			}
		})
	}
}

// 3D Positive: a docs-only root (README.md, LICENSE, profile/README.md, .github/ISSUE_TEMPLATE/x.yml)
// declares verification not applicable.
func TestAdoptDocsOnlyRootDeclaresNotApplicable(t *testing.T) {
	root := newTestRepo(t, "docs-only-root")
	mustWrite(t, filepath.Join(root, "README.md"), "# Organization\n")
	mustWrite(t, filepath.Join(root, "LICENSE"), "EUPL-1.2\n")
	mustWrite(t, filepath.Join(root, "profile", "README.md"), "# Profile\n")
	mustWrite(t, filepath.Join(root, ".github", "ISSUE_TEMPLATE", "x.yml"), "name: Bug\n")
	rep := adoptPlain(t, root, false)
	if rep.Verification.Status != verificationNotApplicable {
		t.Fatalf("docs-only root verification status = %s, want %s", rep.Verification.Status, verificationNotApplicable)
	}
	if len(rep.Verification.Reasons) == 0 || !strings.Contains(rep.Verification.Reasons[0], "no build system") {
		t.Fatalf("verification reasons = %v, want mentioning no build system", rep.Verification.Reasons)
	}
}

// 3D Boundary: when discovery reaches the workspace scan bound during language scan without runtimes,
// the plan fails closed to unavailable.
func TestAdoptRootWalkBoundExceededUnavailable(t *testing.T) {
	root := newTestRepo(t, "walk-bound")
	mustWrite(t, filepath.Join(root, "docs", "guide.md"), "# Guide\n")
	mustWrite(t, filepath.Join(root, "docs", "faq.md"), "# FAQ\n")
	mustWrite(t, filepath.Join(root, "docs", "intro.md"), "# Intro\n")
	limits := DefaultVerificationLimits()
	limits.MaxFiles = 1
	plan, err := resolveVerificationPlanWithLimits(t.Context(), root, &limits)
	if err != nil {
		t.Fatalf("resolveVerificationPlanWithLimits failed: %v", err)
	}
	if plan.Status != verificationUnavailable {
		t.Fatalf("plan status = %s, want unavailable", plan.Status)
	}
	if len(plan.Reasons) == 0 || !strings.Contains(plan.Reasons[0], "workspace scan bound") {
		t.Fatalf("plan reasons = %v, want mentioning workspace scan bound", plan.Reasons)
	}
}
