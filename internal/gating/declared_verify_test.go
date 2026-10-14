// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// declaredVerifyRepo is a committed Python root (pyproject.toml, no go.mod, no Cargo.lock) whose
// Makefile declares verify-all with recipe; an empty recipe commits no Makefile.
func declaredVerifyRepo(t *testing.T, recipe string) string {
	t.Helper()
	dir := committedAdmitRepo(t, "pyproject.toml")
	if recipe != "" {
		writeFile(t, filepath.Join(dir, "Makefile"), ".PHONY: verify-all\nverify-all:\n\t"+recipe+"\n")
		treeGit(t, dir, "add", "--", "Makefile")
		treeGit(t, dir, "commit", "-q", "-m", "declare verify-all")
	}
	return dir
}

// recipeStatus matches GNU Make's failed-recipe line in any locale: "[Makefile:3: verify-all] Error 1".
var recipeStatus = regexp.MustCompile(`\[Makefile:\d+: verify-all\] \S+ 1\b`)

// requireMake skips where the stage's make is not on PATH (HISS-21): the acceptance runs below
// run the real command.
func requireMake(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skipf("make is not on PATH, so the declared verify-all cannot run here: %v", err)
	}
}

// stageNamed returns the stage the run recorded under name.
func stageNamed(t *testing.T, rep *PipelineReport, name string) StageResult {
	t.Helper()
	for _, s := range rep.Stages {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no %q stage in %+v", name, rep.Stages)
	return StageResult{}
}

// Negative (#1218 acceptance 1): a declared verify-all that fails refuses the push under
// --admit-unsupported, and the reason names the command and its exit code. GNU Make exits 2 when
// a recipe fails and reports the recipe's own status ("Error 1"), so both are named. Make
// translates the word "Error" (German hosts print "Fehler 1"), so the recipe status is matched
// without it: the target in brackets, then the status.
func TestRunGatedPipeline_Negative_DeclaredVerifyFailureRefuses(t *testing.T) {
	requireMake(t)
	rep := gatedRun(t, declaredVerifyRepo(t, "@exit 1"), RunOptions{AdmitUnsupported: true})
	got := rep.Stages[len(rep.Stages)-1]
	if rep.Status != StatusRejected || got.Name != stageDeclaredVerify || got.Status != StageFailed {
		t.Fatalf("status %s, last stage %s %s; want rejected at the declared verification: %+v", rep.Status, got.Name, got.Status, rep.Stages)
	}
	if !strings.Contains(got.Message, "make verify-all exited 2") {
		t.Errorf("reason %q does not name %q", got.Message, "make verify-all exited 2")
	}
	if !recipeStatus.MatchString(got.Message) {
		t.Errorf("reason %q does not name the recipe status (%s)", got.Message, recipeStatus)
	}
}

// Positive (#1218 acceptance 2): a declared verify-all that exits 0 is listed as run and passed,
// not as not applicable, the push is admitted, and the receipt stage says it ran but signs nothing.
func TestRunGatedPipeline_Positive_DeclaredVerifyPassAdmits(t *testing.T) {
	requireMake(t)
	rep := gatedRun(t, declaredVerifyRepo(t, "@exit 0"), RunOptions{AdmitUnsupported: true})
	got := stageNamed(t, rep, stageDeclaredVerify)
	if rep.Status != StatusAdmitted || got.Status != StagePassed || got.Message != "make verify-all exited 0" {
		t.Fatalf("status %s, stage %s %q; want admitted with the stage passed", rep.Status, got.Status, got.Message)
	}
	if receipt := receiptOf(t, rep); receipt.Status != StageNotApplicable || !strings.Contains(receipt.Message, "make verify-all ran and passed, but signs no receipt") {
		t.Fatalf("receipt stage %s %q; want not applicable, naming the passed run", receipt.Status, receipt.Message)
	}
}

// Boundary (#1218 acceptance 3): without a declared command the run is admitted as before, the
// stage is not applicable and says why, and the receipt stage names the unsupported language.
func TestRunGatedPipeline_Boundary_NoDeclaredVerifyKeepsTodaysAdmission(t *testing.T) {
	rep := gatedRun(t, declaredVerifyRepo(t, ""), RunOptions{AdmitUnsupported: true})
	got := stageNamed(t, rep, stageDeclaredVerify)
	if rep.Status != StatusAdmitted || !rep.AdmittedUnverified || got.Status != StageNotApplicable ||
		!strings.Contains(got.Message, "no verification command declared") {
		t.Fatalf("status %s, stage %s %q; want admitted with the stage not applicable", rep.Status, got.Status, got.Message)
	}
	receipt := receiptOf(t, rep)
	if !strings.Contains(receipt.Message, "python (pyproject.toml)") || strings.Contains(receipt.Message, "ran and passed") {
		t.Fatalf("receipt reason %q; want today's reason naming python", receipt.Message)
	}
}

// Boundary: the command runs in an isolated worktree of HEAD, so what it writes never reaches
// the working tree, and the run stays admitted on a clean tree.
func TestRunGatedPipeline_Boundary_DeclaredVerifyLeavesTheTreeAlone(t *testing.T) {
	requireMake(t)
	dir := declaredVerifyRepo(t, "@echo built> built.txt")
	rep := gatedRun(t, dir, RunOptions{AdmitUnsupported: true})
	if rep.Status != StatusAdmitted || stageNamed(t, rep, stageDeclaredVerify).Status != StagePassed {
		t.Fatalf("status %s; want admitted with the stage passed: %+v", rep.Status, rep.Stages)
	}
	if _, err := os.Stat(filepath.Join(dir, "built.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the command wrote into the working tree: %v", err)
	}
}

// declaredStageConfig is a stage configuration over a directory holding markers and, when
// makefile is set, a Makefile with that body. It is not a repository: it reaches only the checks
// before the worktree.
func declaredStageConfig(t *testing.T, makefile string, markers ...string) *stageConfig {
	t.Helper()
	dir := admitRepo(t, markers...)
	if makefile != "" {
		writeFile(t, filepath.Join(dir, "Makefile"), makefile)
	}
	cfg, _ := newTestConfig(t, dir, false)
	cfg.lookPath = func(string) (string, error) { return "make", nil }
	return cfg
}

const verifyAllMakefile = "verify-all:\n\t@exit 0\n"

func TestRunDeclaredVerifyStage_RunsNothingWhereItDoesNotApply(t *testing.T) {
	cases := []struct {
		name   string
		cfg    func(t *testing.T) *stageConfig
		status StageStatus
		reason string
	}{
		{"go.mod at the root", func(t *testing.T) *stageConfig {
			return declaredStageConfig(t, verifyAllMakefile, "go.mod")
		}, StageNotApplicable, "toolchain stages verify this repository"},
		{"Cargo.lock at the root", func(t *testing.T) *stageConfig {
			return declaredStageConfig(t, verifyAllMakefile, CargoLockFile)
		}, StageNotApplicable, "toolchain stages verify this repository"},
		{"no makefile", func(t *testing.T) *stageConfig {
			return declaredStageConfig(t, "", "pyproject.toml")
		}, StageNotApplicable, "no verification command declared"},
		{"makefile without the target", func(t *testing.T) *stageConfig {
			return declaredStageConfig(t, "test:\n\t@exit 0\n", "pyproject.toml")
		}, StageNotApplicable, "no verification command declared"},
		{"dry run", func(t *testing.T) *stageConfig {
			cfg := declaredStageConfig(t, verifyAllMakefile, "pyproject.toml")
			cfg.dryRun = true
			return cfg
		}, StageSkipped, "dry run: make verify-all not run"},
		{"make missing", func(t *testing.T) *stageConfig {
			cfg := declaredStageConfig(t, verifyAllMakefile, "pyproject.toml")
			cfg.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
			return cfg
		}, StageSkipped, "make is not on PATH"},
		{"inside a declared verification", func(t *testing.T) *stageConfig {
			t.Setenv(declaredVerifyGuardEnv, "1")
			return declaredStageConfig(t, verifyAllMakefile, "pyproject.toml")
		}, StageNotApplicable, "not run again"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg(t)
			if err := executeStage(t.Context(), stage{stageDeclaredVerify, runDeclaredVerifyStage}, cfg); err != nil {
				t.Fatalf("a stage that runs nothing stopped the pipeline: %v", err)
			}
			got := cfg.rep.Stages[len(cfg.rep.Stages)-1]
			if got.Status != tc.status || !strings.Contains(got.Message, tc.reason) || cfg.declaredVerified {
				t.Fatalf("stage %s %q (verified %v); want %s naming %q", got.Status, got.Message, cfg.declaredVerified, tc.status, tc.reason)
			}
		})
	}
}

func TestDeclaresVerifyTargetReadsTheMakefileMakeReads(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{"Makefile declares it", map[string]string{"Makefile": verifyAllMakefile}, true},
		{"lower-case makefile declares it", map[string]string{"makefile": verifyAllMakefile}, true},
		{"GNUmakefile is read first", map[string]string{"GNUmakefile": "test:\n", "Makefile": verifyAllMakefile}, false},
		{"target only in a comment", map[string]string{"Makefile": "# verify-all:\ntest:\n"}, false},
		{"no makefile", map[string]string{}, false},
		{"oversized makefile", map[string]string{"Makefile": verifyAllMakefile + strings.Repeat("#\n", maxMakefileBytes/2)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tc.files {
				writeFile(t, filepath.Join(dir, name), body)
			}
			if got := declaresVerifyTarget(dir); got != tc.want {
				t.Fatalf("declaresVerifyTarget = %v, want %v", got, tc.want)
			}
		})
	}
}

type exitCodeError struct{ code int }

func (e exitCodeError) Error() string { return "exit status" }
func (e exitCodeError) ExitCode() int { return e.code }

func TestDeclaredVerifyFailureNamesTheOutcome(t *testing.T) {
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	long := strings.Repeat("x", 3*maxDeclaredVerifyDiagnostic) + "last line"
	cases := []struct {
		name string
		ctx  context.Context
		err  error
		want []string
	}{
		{"exit code", context.Background(), exitCodeError{3}, []string{"declared verification make verify-all exited 3"}},
		{"bound fired", expired, exitCodeError{1}, []string{"make verify-all did not finish within 2m0s"}},
		{"no exit code", context.Background(), errors.New("exec: no such file"), []string{"make verify-all failed to run", "no such file"}},
		{"long output keeps its end", context.Background(), errors.New(long), []string{"...", "last line"}},
	}
	for _, tc := range cases {
		got := declaredVerifyFailure(tc.ctx, 2*time.Minute, tc.err).Error()
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: %q does not name %q", tc.name, got, want)
			}
		}
		if len(got) > maxDeclaredVerifyDiagnostic+200 {
			t.Errorf("%s: reason of %d bytes is not bounded", tc.name, len(got))
		}
	}
}
