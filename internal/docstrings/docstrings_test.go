// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docstrings_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/docstrings"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// newDocstringTestRepo initializes a hermetic Git repository with an origin/main baseline.
func newDocstringTestRepo(t *testing.T, baseFiles map[string]string) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "praetor-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "praetor-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")
	dir := t.TempDir()
	for rel, body := range baseFiles {
		writeTestFile(t, dir, rel, body)
	}
	runGitCmd(t, dir, "init", "-q", "-b", "main")
	runGitCmd(t, dir, "add", ".")
	runGitCmd(t, dir, "commit", "-q", "-m", "initial")
	runGitCmd(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	return dir
}

// writeTestFile writes content to dir/rel.
func writeTestFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runGitCmd runs git in dir with a hermetic environment.
func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	envCtx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(envCtx, 30*time.Second)
	defer cancel()
	if out, err := util.RunGit(ctx, dir, args...); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// runGitOut runs git in dir with a hermetic environment and returns trimmed stdout.
func runGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	envCtx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(envCtx, 30*time.Second)
	defer cancel()
	out, err := util.RunGit(ctx, dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

// assertDocstringReport checks that a docstring coverage report matches the expected pass state and counts.
func assertDocstringReport(t *testing.T, label string, rep *docstrings.Report, err error, passed bool, counted, covered int) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	if rep.Passed != passed || rep.Counted != counted || rep.Covered != covered {
		t.Fatalf("%s: expected passed=%v %d/%d, got %+v", label, passed, covered, counted, rep)
	}
}

// TestCheck_Positive_PlantedCases verifies that a touched undocumented function fails, the same
// with a doc comment passes, an untouched undocumented function is not counted, and Python, shell,
// and attribute-decorated functions are detected.
func TestCheck_Positive_PlantedCases(t *testing.T) {
	baseGo := "package pkg\n\nfunc Untouched() {\n\t_ = 1\n}\n"
	dir := newDocstringTestRepo(t, map[string]string{"pkg/a.go": baseGo})

	// 1. Touched function without a doc comment fails, untouched undocumented function not counted.
	writeTestFile(t, dir, "pkg/a.go", baseGo+"\nfunc Touched() {\n\t_ = 2\n}\n")
	rep, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	assertDocstringReport(t, "Check undocumented", rep, err, false, 1, 0)
	if len(rep.Uncovered) != 1 || rep.Uncovered[0].Name != "Touched" {
		t.Fatalf("expected uncovered Touched, got %+v", rep.Uncovered)
	}

	// 2. The same with a doc comment passes.
	writeTestFile(t, dir, "pkg/a.go", baseGo+"\n// Touched performs the work.\nfunc Touched() {\n\t_ = 2\n}\n")
	rep, err = docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	assertDocstringReport(t, "Check documented", rep, err, true, 1, 1)

	// 3. Python docstring, Rust with attribute, and Shell # comment count as covered.
	writeTestFile(t, dir, "app/run.py", "def compute(x):\n    \"\"\"Return x plus one.\"\"\"\n    return x + 1\n")
	writeTestFile(t, dir, "src/lib.rs", "/// Computes the value.\n#[inline]\npub fn compute() {\n    let _ = 1;\n}\n")
	writeTestFile(t, dir, "bin/job.sh", "#!/bin/sh\n# Run the deployment job.\nrun_job() {\n  echo ok\n}\n")
	runGitCmd(t, dir, "add", ".")
	rep, err = docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	assertDocstringReport(t, "Check multi-language", rep, err, true, 4, 4)
}

// TestCheck_Boundary_ExactThresholdPasses verifies that coverage exactly equal to the threshold passes
// and one below fails, and reads .coderabbit.yaml when present.
func TestCheck_Boundary_ExactThresholdPasses(t *testing.T) {
	dir := newDocstringTestRepo(t, map[string]string{"pkg/a.go": "package pkg\n"})
	writeTestFile(t, dir, ".coderabbit.yaml", "reviews:\n  pre_merge_checks:\n    docstrings:\n      mode: error\n      threshold: 80\n")
	writeTestFile(t, dir, "pkg/a.go", `package pkg

// One is documented.
func One() {}

// Two is documented.
func Two() {}

// Three is documented.
func Three() {}

// Four is documented.
func Four() {}

func Five() {}
`)
	rep, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	if err != nil {
		t.Fatalf("Check 80%% boundary: %v", err)
	}
	if !rep.Passed || rep.Counted != 5 || rep.Covered != 4 || rep.Coverage != 80.0 {
		t.Fatalf("expected exact 80%% (4/5) to pass, got %+v", rep)
	}

	override81 := 81
	repFail, err := docstrings.Check(t.Context(), docstrings.Options{
		RepoDir:           dir,
		BaseRef:           "origin/main",
		ThresholdOverride: &override81,
	})
	if err != nil {
		t.Fatalf("Check 81%% override: %v", err)
	}
	if repFail.Passed {
		t.Fatalf("expected 80%% against 81%% threshold to fail, got %+v", repFail)
	}
}

// TestCheck_Negative_FailClosedOnInvalidConfigOrRef verifies that malformed .coderabbit.yaml or
// unresolvable git refs return an error.
func TestCheck_Negative_FailClosedOnInvalidConfigOrRef(t *testing.T) {
	dir := newDocstringTestRepo(t, map[string]string{"pkg/a.go": "package pkg\n"})
	writeTestFile(t, dir, ".coderabbit.yaml", "reviews:\n  pre_merge_checks:\n    docstrings:\n      mode: invalid\n")
	if _, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"}); err == nil {
		t.Fatal("expected invalid .coderabbit.yaml mode to fail closed")
	}

	writeTestFile(t, dir, ".coderabbit.yaml", "reviews: [unterminated\n")
	if _, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"}); err == nil {
		t.Fatal("expected malformed .coderabbit.yaml to fail closed")
	}

	if err := os.Remove(filepath.Join(dir, ".coderabbit.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "nonexistent-ref"}); err == nil {
		t.Fatal("expected nonexistent base ref to fail closed")
	}
}

// TestCheck_Positive_ModesErrorWarningOff verifies error, warning, and off modes in .coderabbit.yaml.
func TestCheck_Positive_ModesErrorWarningOff(t *testing.T) {
	dir := newDocstringTestRepo(t, map[string]string{"pkg/a.go": "package pkg\n"})
	writeTestFile(t, dir, "pkg/a.go", "package pkg\n\nfunc Undocumented() {\n\t_ = 1\n}\n")

	writeTestFile(t, dir, ".coderabbit.yaml", "reviews:\n  pre_merge_checks:\n    docstrings:\n      mode: error\n      threshold: 80\n")
	repErr, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	assertDocstringReport(t, "mode error", repErr, err, false, 1, 0)
	if repErr.Mode != "error" || len(repErr.Uncovered) != 1 {
		t.Fatalf("expected error mode with 1 uncovered function, got %+v", repErr)
	}

	writeTestFile(t, dir, ".coderabbit.yaml", "reviews:\n  pre_merge_checks:\n    docstrings:\n      mode: warning\n      threshold: 80\n")
	repWarn, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	assertDocstringReport(t, "mode warning", repWarn, err, true, 1, 0)
	if repWarn.Mode != "warning" || len(repWarn.Uncovered) != 1 || repWarn.Coverage != 0.0 {
		t.Fatalf("expected warning mode to report uncovered function and pass, got %+v", repWarn)
	}

	writeTestFile(t, dir, ".coderabbit.yaml", "reviews:\n  pre_merge_checks:\n    docstrings:\n      mode: off\n      threshold: 80\n")
	repOff, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	assertDocstringReport(t, "mode off", repOff, err, true, 0, 0)
	if repOff.Mode != "off" {
		t.Fatalf("expected off mode, got %+v", repOff)
	}
}

// TestCheck_Boundary_SubmoduleGitlinkAndSymlinkSkipped verifies that a submodule bump and a changed symlink
// are skipped without failing the check in both worktree and --head modes.
func TestCheck_Boundary_SubmoduleGitlinkAndSymlinkSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs developer mode on Windows")
	}
	dir := newDocstringTestRepo(t, map[string]string{"pkg/a.go": "package pkg\n"})
	headOID := runGitOut(t, dir, "rev-parse", "HEAD")

	writeTestFile(t, dir, "pkg/a.go", "package pkg\n\n// Covered is documented.\nfunc Covered() {\n\t_ = 1\n}\n")
	if err := os.MkdirAll(filepath.Join(dir, "vendor", "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.go", filepath.Join(dir, "pkg", "link.go")); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, dir, "add", "pkg/a.go", "pkg/link.go")
	runGitCmd(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+headOID+",vendor/sub")
	runGitCmd(t, dir, "commit", "-q", "-m", "add submodule gitlink and symlink")

	repWT, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	assertDocstringReport(t, "worktree with gitlink and symlink", repWT, err, true, 1, 1)

	repHead, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main", HeadRef: "HEAD"})
	assertDocstringReport(t, "head with gitlink and symlink", repHead, err, true, 1, 1)
}

// TestCheck_Negative_ChangedGoFileParseErrorFails verifies that a Go syntax error in a changed file fails the check.
func TestCheck_Negative_ChangedGoFileParseErrorFails(t *testing.T) {
	dir := newDocstringTestRepo(t, map[string]string{"pkg/broken.go": "package pkg\n"})
	writeTestFile(t, dir, "pkg/broken.go", "package pkg\nfunc Broken(\n")
	_, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	if err == nil || !strings.Contains(err.Error(), "pkg/broken.go") {
		t.Fatalf("expected parse error naming pkg/broken.go, got %v", err)
	}
}

// TestPrePushAndDefaultBranch_3D verifies 40- and 64-hex pre-push stdin parsing, delete skipping,
// base resolution, and omitted BaseRef resolution via forge.RepositoryDefaultBranch.
func TestPrePushAndDefaultBranch_3D(t *testing.T) {
	oid40 := strings.Repeat("a", 40)
	oid64 := strings.Repeat("b", 64)
	zero40 := strings.Repeat("0", 40)
	stdin := "refs/heads/f1 " + oid40 + " refs/heads/f1 " + zero40 + "\n" +
		"refs/heads/del " + zero40 + " refs/heads/del " + oid40 + "\n" +
		"refs/heads/f2 " + oid64 + " refs/heads/f2 " + strings.Repeat("c", 64) + "\n"
	updates, err := docstrings.ParsePrePushStdin(strings.NewReader(stdin))
	if err != nil || len(updates) != 2 || updates[0].LocalOID != oid40 || updates[1].LocalOID != oid64 {
		t.Fatalf("ParsePrePushStdin valid 40/64-hex: got %+v, err=%v", updates, err)
	}
	for _, bad := range []string{"only-three a b\n", "r1 not-hex r2 " + oid40 + "\n"} {
		if _, err := docstrings.ParsePrePushStdin(strings.NewReader(bad)); err == nil {
			t.Fatalf("expected malformed stdin %q to fail", bad)
		}
	}

	dir := newDocstringTestRepo(t, map[string]string{"pkg/a.go": "package pkg\n"})
	runGitCmd(t, dir, "branch", "-m", "main", "trunk")
	runGitCmd(t, dir, "update-ref", "refs/remotes/origin/trunk", "HEAD")
	runGitCmd(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	runGitCmd(t, dir, "update-ref", "-d", "refs/remotes/origin/main")

	writeTestFile(t, dir, "pkg/a.go", "package pkg\n\n// Ok is documented.\nfunc Ok() {}\n")
	rep, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir})
	assertDocstringReport(t, "omitted BaseRef uses origin/trunk", rep, err, true, 1, 1)
}

// TestCheck_ModeOffWithoutOriginMain_ShortCircuits verifies that mode off short-circuits
// before base ref resolution or diff calculation in a repo lacking origin/main.
func TestCheck_ModeOffWithoutOriginMain_ShortCircuits(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "praetor-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "praetor-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")
	dir := t.TempDir()
	writeTestFile(t, dir, ".coderabbit.yaml", "reviews:\n  pre_merge_checks:\n    docstrings:\n      mode: off\n      threshold: 80\n")
	writeTestFile(t, dir, "pkg/a.go", "package pkg\n\nfunc Unchecked() {}\n")
	runGitCmd(t, dir, "init", "-q", "-b", "main")
	runGitCmd(t, dir, "add", ".")
	runGitCmd(t, dir, "commit", "-q", "-m", "initial")
	// There is intentionally no origin remote and no origin/main ref.
	rep, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir})
	if err != nil {
		t.Fatalf("expected mode off to exit 0 without origin/main, got error: %v", err)
	}
	assertDocstringReport(t, "mode off without origin/main", rep, err, true, 0, 0)
	if rep.Mode != "off" {
		t.Fatalf("expected mode off, got %+v", rep)
	}
}

// TestCheck_UnsupportedFileTypeNotRead verifies that changed files of unsupported types
// (such as Markdown documentation or JSON data) are not read, while extensionless shell scripts are.
func TestCheck_UnsupportedFileTypeNotRead(t *testing.T) {
	baseGo := "package pkg\n\n// Alpha is documented.\nfunc Alpha() {}\n"
	dir := newDocstringTestRepo(t, map[string]string{
		"pkg/a.go":        baseGo,
		"docs/manual.md":  "# Manual\n",
		"data/items.json": "[]\n",
		"bin/deploy":      "#!/bin/sh\n# Deploy helper.\ndeploy() {\n  echo ok\n}\n",
	})
	// Touch all four files in the worktree.
	writeTestFile(t, dir, "pkg/a.go", baseGo+"\n// Beta is documented.\nfunc Beta() {}\n")
	writeTestFile(t, dir, "docs/manual.md", "# Manual Updated\n")
	writeTestFile(t, dir, "data/items.json", "[\"item\"]\n")
	writeTestFile(t, dir, "bin/deploy", "#!/bin/sh\n# Deploy helper.\ndeploy() {\n  echo ok\n}\n# Extra task.\nstep() {\n  echo step\n}\n")

	var readPaths []string
	restore := docstrings.SetTargetFileReaderForTest(func(ctx context.Context, repoDir, headRef, rel string) ([]byte, bool, error) {
		readPaths = append(readPaths, rel)
		if strings.HasSuffix(rel, ".md") || strings.HasSuffix(rel, ".json") {
			return nil, false, errors.New("unsupported file must not be read: " + rel)
		}
		return docstrings.ReadTargetFileDefault(ctx, repoDir, headRef, rel)
	})
	defer restore()

	rep, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	if err != nil {
		t.Fatalf("expected unsupported file not to be read, but Check failed: %v", err)
	}
	// pkg/a.go (Beta, 1) + bin/deploy (step, 1) = 2 counted, both documented.
	assertDocstringReport(t, "unsupported files not read", rep, err, true, 2, 2)

	for _, p := range readPaths {
		if strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".json") {
			t.Fatalf("targetFileReader read unsupported file %q", p)
		}
	}
	if !slices.Contains(readPaths, "pkg/a.go") {
		t.Fatalf("expected pkg/a.go to be read, got reads: %v", readPaths)
	}
	if !slices.Contains(readPaths, "bin/deploy") {
		t.Fatalf("expected extensionless script bin/deploy to be read, got reads: %v", readPaths)
	}
}
