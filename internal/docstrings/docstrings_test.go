// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docstrings_test

import (
	"context"
	"os"
	"path/filepath"
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
