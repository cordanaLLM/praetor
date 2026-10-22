// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func skipIfWindowsSymlinksRefused(t *testing.T, rep *PipelineReport) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	for _, s := range rep.Stages {
		if strings.Contains(s.Message, "developer mode or administrator rights on Windows") {
			t.Skipf("skipping on Windows: symlinks refused (%s)", s.Message)
		}
	}
}

func createIgnoredDependencyRepo(t *testing.T, recipe string) string {
	t.Helper()
	dir := committedAdmitRepo(t, "pyproject.toml")
	writeFile(t, filepath.Join(dir, ".gitignore"), "node_modules/\n.venv/\nvenv/\nbuild/\n")
	treeGit(t, dir, "add", "--", ".gitignore")
	treeGit(t, dir, "commit", "-q", "-m", "ignore dependency folders")

	if recipe != "" {
		writeFile(t, filepath.Join(dir, "Makefile"), ".PHONY: verify-all\nverify-all:\n\t"+recipe+"\n")
		treeGit(t, dir, "add", "--", "Makefile")
		treeGit(t, dir, "commit", "-q", "-m", "declare verify-all")
	}
	return dir
}

// Positive: a verify-all that needs node_modules/x passes with the folder present in the checkout and git-ignored.
func TestRunGatedPipeline_Positive_DeclaredVerifyWithIgnoredDependencyFolder(t *testing.T) {
	requireMake(t)
	dir := createIgnoredDependencyRepo(t, "test -f node_modules/x/ok")

	depFile := filepath.Join(dir, "node_modules", "x", "ok")
	writeFile(t, depFile, "dependency content\n")

	rep := gatedRun(t, dir, RunOptions{AdmitUnsupported: true})
	skipIfWindowsSymlinksRefused(t, rep)
	got := stageNamed(t, rep, stageDeclaredVerify)
	if rep.Status != StatusAdmitted || got.Status != StagePassed || got.Message != "make verify-all exited 0" {
		t.Fatalf("status %s, stage %s %q; want admitted with stage passed", rep.Status, got.Status, got.Message)
	}

	// Verify the checkout's dependency folder and file are preserved and never deleted.
	if _, err := os.Stat(depFile); err != nil {
		t.Fatalf("checkout dependency folder was deleted or damaged: %v", err)
	}
}

// Negative: the same repository without the folder still fails the stage (absent dependencies outside the gate still fail).
func TestRunGatedPipeline_Negative_DeclaredVerifyFailsWithoutDependencyFolder(t *testing.T) {
	requireMake(t)
	dir := createIgnoredDependencyRepo(t, "test -f node_modules/x/ok")

	rep := gatedRun(t, dir, RunOptions{AdmitUnsupported: true})
	got := rep.Stages[len(rep.Stages)-1]
	if rep.Status != StatusRejected || got.Name != stageDeclaredVerify || got.Status != StageFailed {
		t.Fatalf("status %s, last stage %s %s; want rejected at declared verification: %+v", rep.Status, got.Name, got.Status, rep.Stages)
	}
	if !recipeStatus.MatchString(got.Message) {
		t.Errorf("reason %q does not name recipe status", got.Message)
	}
}

// Negative: a verify-all that writes into node_modules fails the stage and names the folder.
func TestRunGatedPipeline_Negative_DeclaredVerifyFailsWhenWritingIntoDependencyFolder(t *testing.T) {
	requireMake(t)
	dir := createIgnoredDependencyRepo(t, "@echo modified > node_modules/x/ok")

	depFile := filepath.Join(dir, "node_modules", "x", "ok")
	writeFile(t, depFile, "clean\n")

	rep := gatedRun(t, dir, RunOptions{AdmitUnsupported: true})
	skipIfWindowsSymlinksRefused(t, rep)
	got := rep.Stages[len(rep.Stages)-1]
	if rep.Status != StatusRejected || got.Name != stageDeclaredVerify || got.Status != StageFailed {
		t.Fatalf("status %s, last stage %s %s; want rejected at declared verification: %+v", rep.Status, got.Name, got.Status, rep.Stages)
	}
	want := "declared verification wrote into the checkout's dependencies in node_modules"
	if !strings.Contains(got.Message, want) {
		t.Errorf("reason %q does not contain %q", got.Message, want)
	}
}

// Negative: a real test failure (verify-all exits 1) with dependencies present is still refused.
func TestRunGatedPipeline_Negative_DeclaredVerifyRealTestFailureWithDependenciesPresent(t *testing.T) {
	requireMake(t)
	dir := createIgnoredDependencyRepo(t, "@test -f node_modules/x/ok || exit 73; echo planted-test-failure >&2; exit 1")

	depFile := filepath.Join(dir, "node_modules", "x", "ok")
	writeFile(t, depFile, "present\n")

	rep := gatedRun(t, dir, RunOptions{AdmitUnsupported: true})
	skipIfWindowsSymlinksRefused(t, rep)
	got := rep.Stages[len(rep.Stages)-1]
	if rep.Status != StatusRejected || got.Name != stageDeclaredVerify || got.Status != StageFailed {
		t.Fatalf("status %s, last stage %s %s; want rejected at declared verification: %+v", rep.Status, got.Name, got.Status, rep.Stages)
	}
	if !recipeStatus.MatchString(got.Message) {
		t.Errorf("reason %q does not name recipe status", got.Message)
	}
	if !strings.Contains(got.Message, "planted-test-failure") {
		t.Errorf("reason %q does not prove dependencies were read before the test failed", got.Message)
	}
}

// Negative: dependency reuse belongs to the stage; an ordinary checkout still needs its dependencies.
func TestDeclaredVerify_Negative_AbsentDependenciesOutsideGate(t *testing.T) {
	requireMake(t)
	t.Setenv(DeclaredVerifyGuardEnv, "")
	dir := createIgnoredDependencyRepo(t, "test -f node_modules/x/ok")
	_, err := util.RunCommand(t.Context(), dir, "make", declaredVerifyTarget)
	if err == nil || !recipeStatus.MatchString(err.Error()) {
		t.Fatalf("make verify-all without dependencies: %v; want a real recipe failure", err)
	}
}

// Boundary: a folder name not on the allow-list (for example build/) is not linked.
func TestRunGatedPipeline_Boundary_FolderNotOnAllowListIsNotLinked(t *testing.T) {
	requireMake(t)
	dir := createIgnoredDependencyRepo(t, "test -f build/x/ok")

	buildFile := filepath.Join(dir, "build", "x", "ok")
	writeFile(t, buildFile, "present\n")

	rep := gatedRun(t, dir, RunOptions{AdmitUnsupported: true})
	got := rep.Stages[len(rep.Stages)-1]
	if rep.Status != StatusRejected || got.Name != stageDeclaredVerify || got.Status != StageFailed {
		t.Fatalf("status %s, last stage %s %s; want rejected because un-allowlisted build/ is not linked: %+v", rep.Status, got.Name, got.Status, rep.Stages)
	}
	if !recipeStatus.MatchString(got.Message) {
		t.Errorf("reason %q does not name recipe status", got.Message)
	}
}

// Negative (plurano/horistes, 2026-10-18): a recipe that creates its virtualenv with python -m venv passes with a
// git-ignored .venv in the checkout. A linked .venv made venv refuse the path ("Unable to create directory"), since
// venv will not build over a symbolic link; a virtualenv also embeds absolute paths, so the stage builds its own.
func TestRunGatedPipeline_Negative_VirtualenvRecipeWithIgnoredVenvInCheckout(t *testing.T) {
	requireMake(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH: the recipe under test is python3 -m venv")
	}
	if _, err := util.RunCommand(t.Context(), "", python, "-m", "venv", "--help"); err != nil {
		t.Skipf("python3 has no venv module, so the recipe under test cannot run: %v", err)
	}
	// --without-pip: the refusal under test is venv's own, so the recipe needs no ensurepip. The
	// recipe runs under sh, which reads a Windows path's backslashes as escapes: forward slashes.
	dir := createIgnoredDependencyRepo(t, filepath.ToSlash(python)+" -m venv --without-pip .venv && test ! -L .venv")
	writeFile(t, filepath.Join(dir, ".venv", "pyvenv.cfg"), "home = /checkout-only\n")

	rep := gatedRun(t, dir, RunOptions{AdmitUnsupported: true})
	got := stageNamed(t, rep, stageDeclaredVerify)
	if rep.Status != StatusAdmitted || got.Status != StagePassed {
		t.Fatalf("status %s, stage %s %q; want admitted: the stage builds its own virtualenv", rep.Status, got.Status, got.Message)
	}
	if data, err := os.ReadFile(filepath.Join(dir, ".venv", "pyvenv.cfg")); err != nil || string(data) != "home = /checkout-only\n" {
		t.Fatalf("the checkout's .venv changed: %q, %v", data, err)
	}
}

// Boundary: the 65th dependency folder fails closed naming the bound.
func TestRunGatedPipeline_Boundary_65thDependencyFolderFailsClosed(t *testing.T) {
	requireMake(t)
	dir := createIgnoredDependencyRepo(t, "@exit 0")

	// Create 65 folders whose parent exists in HEAD and whose base is on the allow-list.
	var addPaths []string
	for i := 0; i < 65; i++ {
		pkg := fmt.Sprintf("pkg%02d", i)
		writeFile(t, filepath.Join(dir, pkg, "keep.txt"), "tracked\n")
		addPaths = append(addPaths, filepath.Join(pkg, "keep.txt"))
	}
	treeGit(t, dir, append([]string{"add", "--"}, addPaths...)...)
	treeGit(t, dir, "commit", "-q", "-m", "commit 65 package directories")

	for i := 0; i < 65; i++ {
		pkg := fmt.Sprintf("pkg%02d", i)
		writeFile(t, filepath.Join(dir, pkg, "node_modules", "dep.txt"), "dep\n")
	}

	rep := gatedRun(t, dir, RunOptions{AdmitUnsupported: true})
	got := rep.Stages[len(rep.Stages)-1]
	if rep.Status != StatusRejected || got.Name != stageDeclaredVerify || got.Status != StageFailed {
		t.Fatalf("status %s, last stage %s %s; want rejected on dependency folder bound: %+v", rep.Status, got.Name, got.Status, rep.Stages)
	}
	wantBound := fmt.Sprintf("exceeded bound of %d", maxDependencyFolders)
	if !strings.Contains(got.Message, wantBound) {
		t.Errorf("reason %q does not contain %q", got.Message, wantBound)
	}
}

// Positive: a repository with a tracked tree deeper than 6 levels passes declared verification;
// search does not descend below depth 6, but links node_modules at the root.
func TestRunGatedPipeline_Positive_DeepTrackedTreeDoesNotFailStage(t *testing.T) {
	requireMake(t)
	dir := createIgnoredDependencyRepo(t, "test -f node_modules/x/ok")

	depFile := filepath.Join(dir, "node_modules", "x", "ok")
	writeFile(t, depFile, "dependency content\n")

	// Create and commit a tracked file 8 levels deep: a/b/c/d/e/f/g/h.txt.
	deepFile := filepath.Join(dir, "a", "b", "c", "d", "e", "f", "g", "h.txt")
	writeFile(t, deepFile, "deep content\n")
	treeGit(t, dir, "add", "--", filepath.Join("a", "b", "c", "d", "e", "f", "g", "h.txt"))
	treeGit(t, dir, "commit", "-q", "-m", "commit deep tracked file")

	rep := gatedRun(t, dir, RunOptions{AdmitUnsupported: true})
	skipIfWindowsSymlinksRefused(t, rep)

	got := stageNamed(t, rep, stageDeclaredVerify)
	if rep.Status != StatusAdmitted || got.Status != StagePassed || got.Message != "make verify-all exited 0" {
		t.Fatalf("status %s, stage %s %q; want admitted with stage passed", rep.Status, got.Status, got.Message)
	}

	if _, err := os.Stat(depFile); err != nil {
		t.Fatalf("checkout dependency folder was deleted or damaged: %v", err)
	}
}

// Boundary: search exceeding folder visit bound fails closed naming the bound.
func TestRunGatedPipeline_Boundary_SearchFolderVisitBoundFailsClosed(t *testing.T) {
	requireMake(t)
	dir := createIgnoredDependencyRepo(t, "@exit 0")

	// Create multiple tracked subdirectories so queue has remaining items when bound is exceeded.
	for i := 0; i < 5; i++ {
		pkg := fmt.Sprintf("subpkg%02d", i)
		writeFile(t, filepath.Join(dir, pkg, "keep.txt"), "tracked\n")
		treeGit(t, dir, "add", "--", filepath.Join(pkg, "keep.txt"))
	}
	treeGit(t, dir, "commit", "-q", "-m", "commit subpackages")

	oldIterations := maxSearchIterations
	maxSearchIterations = 2
	defer func() { maxSearchIterations = oldIterations }()

	rep := gatedRun(t, dir, RunOptions{AdmitUnsupported: true})
	got := rep.Stages[len(rep.Stages)-1]
	if rep.Status != StatusRejected || got.Name != stageDeclaredVerify || got.Status != StageFailed {
		t.Fatalf("status %s, last stage %s %s; want rejected on search visit bound: %+v", rep.Status, got.Name, got.Status, rep.Stages)
	}

	wantBound := fmt.Sprintf("exceeded bound of %d folder visits", 2)
	if !strings.Contains(got.Message, wantBound) {
		t.Errorf("reason %q does not contain %q", got.Message, wantBound)
	}
}

// Negative: symlink permission failure (Windows without developer mode) fails the stage naming the cause and two ways out.
func TestRunGatedPipeline_Negative_SymlinkPermissionErrorNamesCauseAndRemediation(t *testing.T) {
	requireMake(t)
	dir := createIgnoredDependencyRepo(t, "@exit 0")
	writeFile(t, filepath.Join(dir, "node_modules", "dep.txt"), "dep\n")

	oldSymlink := symlinkFunc
	symlinkFunc = func(oldname, newname string) error {
		return &os.LinkError{Op: "symlink", Old: oldname, New: newname, Err: os.ErrPermission}
	}
	defer func() { symlinkFunc = oldSymlink }()

	rep := gatedRun(t, dir, RunOptions{AdmitUnsupported: true})
	got := rep.Stages[len(rep.Stages)-1]
	if rep.Status != StatusRejected || got.Name != stageDeclaredVerify || got.Status != StageFailed {
		t.Fatalf("status %s, last stage %s %s; want rejected at declared verification: %+v", rep.Status, got.Name, got.Status, rep.Stages)
	}

	wantSubs := []string{
		"developer mode or administrator rights on Windows",
		"enable developer mode",
		"run make verify-all in the checkout",
		"permission denied",
	}
	for _, sub := range wantSubs {
		if !strings.Contains(got.Message, sub) {
			t.Errorf("stage message %q does not contain %q", got.Message, sub)
		}
	}
}
