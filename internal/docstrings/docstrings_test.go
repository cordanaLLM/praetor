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
	"github.com/cordanaLLM/praetor/internal/hiss"
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
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- fixture directories need owner traversal.
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
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- fixture directories need owner traversal.
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
	updates, err := docstrings.ParsePrePushStdin(t.Context(), strings.NewReader(stdin))
	if err != nil || len(updates) != 2 || updates[0].LocalOID != oid40 || updates[1].LocalOID != oid64 {
		t.Fatalf("ParsePrePushStdin valid 40/64-hex: got %+v, err=%v", updates, err)
	}
	for _, bad := range []string{"only-three a b\n", "r1 not-hex r2 " + oid40 + "\n"} {
		if _, err := docstrings.ParsePrePushStdin(t.Context(), strings.NewReader(bad)); err == nil {
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

// TestResolvePrePushBase_RewrittenBranch_3D verifies that a fast-forward push diffs against the
// old remote head, a force push of rewritten history diffs against the merge base with the
// default branch (never the stale head, whose diff holds every commit the rebase picked up),
// and a new branch (zero remote OID) or an unknown remote head diffs against the merge base.
func TestResolvePrePushBase_RewrittenBranch_3D(t *testing.T) {
	dir := newDocstringTestRepo(t, map[string]string{"pkg/a.go": "package pkg\n"})
	mainOID := runGitOut(t, dir, "rev-parse", "HEAD")
	writeTestFile(t, dir, "pkg/b.go", "package pkg\n")
	runGitCmd(t, dir, "add", ".")
	runGitCmd(t, dir, "commit", "-q", "-m", "old branch head")
	oldHead := runGitOut(t, dir, "rev-parse", "HEAD")
	writeTestFile(t, dir, "pkg/c.go", "package pkg\n")
	runGitCmd(t, dir, "add", ".")
	runGitCmd(t, dir, "commit", "-q", "-m", "fast-forward child")
	childOID := runGitOut(t, dir, "rev-parse", "HEAD")
	runGitCmd(t, dir, "reset", "-q", "--hard", mainOID)
	writeTestFile(t, dir, "pkg/b.go", "package pkg\n\n// B is rewritten.\nfunc B() {}\n")
	runGitCmd(t, dir, "add", ".")
	runGitCmd(t, dir, "commit", "-q", "-m", "rewritten branch head")
	rewritten := runGitOut(t, dir, "rev-parse", "HEAD")

	cases := []struct{ name, local, remote, want string }{
		{"fast-forward uses the remote head", childOID, oldHead, oldHead},
		{"rewritten history uses the merge base", rewritten, oldHead, mainOID},
		{"new branch uses the merge base", rewritten, strings.Repeat("0", 40), mainOID},
		{"unknown remote head uses the merge base", rewritten, strings.Repeat("d", 40), mainOID},
	}
	for _, tc := range cases {
		got, skip := docstrings.ResolvePrePushBase(t.Context(), dir, "origin", tc.local, tc.remote)
		if skip != "" || got != tc.want {
			t.Fatalf("%s: got base %q skip %q, want %q", tc.name, got, skip, tc.want)
		}
	}
}

// assertDefaultBaseRef checks that DefaultBaseRef resolves wantRef and wantSource without error.
func assertDefaultBaseRef(t *testing.T, label, dir, wantRef, wantSource string) {
	t.Helper()
	ref, source, err := docstrings.DefaultBaseRef(t.Context(), dir, "origin")
	if err != nil || ref != wantRef || source != wantSource {
		t.Fatalf("%s: got ref=%q source=%q err=%v, want %s %s", label, ref, source, err, wantRef, wantSource)
	}
}

// TestDefaultBaseRef_RemoteSymrefOverStaleLocal_3D verifies that DefaultBaseRef and ResolvePrePushBase
// prefer the remote's own symbolic HEAD over a stale local origin/HEAD or .standards.yaml, and fall
// back to the local default branch with source "local" when the remote cannot answer.
func TestDefaultBaseRef_RemoteSymrefOverStaleLocal_3D(t *testing.T) {
	dir := newDocstringTestRepo(t, map[string]string{"pkg/a.go": "package pkg\n"})
	mainOID := runGitOut(t, dir, "rev-parse", "HEAD")

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	runGitCmd(t, dir, "init", "--bare", "-q", "-b", "main", remoteDir)
	runGitCmd(t, dir, "remote", "add", "origin", remoteDir)
	runGitCmd(t, dir, "push", "-q", "origin", "main")

	writeTestFile(t, dir, "pkg/a.go", "package pkg\n\nfunc StaleBypass() {}\n")
	runGitCmd(t, dir, "add", ".")
	runGitCmd(t, dir, "commit", "-q", "-m", "undocumented feature commit")
	featOID := runGitOut(t, dir, "rev-parse", "HEAD")
	runGitCmd(t, dir, "update-ref", "refs/remotes/origin/feat", featOID)
	runGitCmd(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/feat")

	assertDefaultBaseRef(t, "reachable remote", dir, "origin/main", "remote")
	base, skip := docstrings.ResolvePrePushBase(t.Context(), dir, "origin", featOID, strings.Repeat("0", 40))
	if skip != "" || base != mainOID {
		t.Fatalf("ResolvePrePushBase with stale local origin/HEAD: got base=%q skip=%q, want %q", base, skip, mainOID)
	}
	rep, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, HeadRef: featOID})
	assertDocstringReport(t, "Check against remote default over stale local HEAD", rep, err, false, 1, 0)

	runGitCmd(t, remoteDir, "symbolic-ref", "HEAD", "refs/heads/nonexistent-head")
	runGitCmd(t, remoteDir, "update-ref", "--no-deref", "HEAD", mainOID)
	assertDefaultBaseRef(t, "detached remote HEAD", dir, "origin/feat", "local")

	runGitCmd(t, dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "unreachable.git"))
	assertDefaultBaseRef(t, "unreachable remote", dir, "origin/feat", "local")

	padLine := strings.Repeat("0", 40) + "\trefs/heads/pad\n"
	if got := docstrings.ParseRemoteDefaultBranchForTest(strings.Repeat(padLine, 63) + "ref: refs/heads/main\tHEAD\n"); got != "main" {
		t.Fatalf("ParseRemoteDefaultBranch at line 64 boundary: got %q, want main", got)
	}
	if got := docstrings.ParseRemoteDefaultBranchForTest(strings.Repeat(padLine, 64) + "ref: refs/heads/main\tHEAD\n"); got != "" {
		t.Fatalf("ParseRemoteDefaultBranch past line 64 boundary: got %q, want empty", got)
	}
}

type stallingReader struct {
	unblock <-chan struct{}
}

// Read blocks until unblock is closed and then returns EOF.
func (s stallingReader) Read(_ []byte) (int, error) {
	<-s.unblock
	return 0, os.ErrClosed
}

// TestParsePrePushStdin_Deadline_3D verifies that ParsePrePushStdin stops on ctx.Done() when the
// reader stalls before EOF, accepts valid input within the deadline, and enforces the byte boundary.
func TestParsePrePushStdin_Deadline_3D(t *testing.T) {
	unblock := make(chan struct{})
	t.Cleanup(func() { close(unblock) })
	shortCtx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	_, err := docstrings.ParsePrePushStdin(shortCtx, stallingReader{unblock: unblock})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("ParsePrePushStdin with stalled reader: got err=%v, want timeout wrapping DeadlineExceeded", err)
	}
	if _, err := docstrings.ParsePrePushStdin(nil, strings.NewReader("")); err == nil { //nolint:staticcheck // SA1012: intentionally testing nil context refusal
		t.Fatal("expected nil context to fail closed")
	}
	if _, err := docstrings.ParsePrePushStdin(t.Context(), nil); err == nil {
		t.Fatal("expected nil reader to fail closed")
	}
	exactMax := strings.Repeat("\n", 1<<20)
	if updates, err := docstrings.ParsePrePushStdin(t.Context(), strings.NewReader(exactMax)); err != nil || len(updates) != 0 {
		t.Fatalf("ParsePrePushStdin exact 1 MiB boundary: got len=%d err=%v", len(updates), err)
	}
	if _, err := docstrings.ParsePrePushStdin(t.Context(), strings.NewReader(exactMax+"\n")); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("ParsePrePushStdin 1 MiB + 1 byte boundary: got err=%v, want exceeds error", err)
	}
}

type docstringFixtureCase struct {
	name          string
	fixture       string
	targetPath    string
	passed        bool
	counted       int
	covered       int
	uncoveredName string
	uncoveredLine int
}

// readDocstringFixture reads data bytes and verifies the source suffix matches the reader's target language.
func readDocstringFixture(t *testing.T, fixture, targetPath string) string {
	t.Helper()
	sourceName := strings.TrimSuffix(fixture, ".txt")
	if util.SourceLanguage(sourceName) != util.SourceLanguage(targetPath) {
		t.Fatalf("fixture %s and target %s select different languages", fixture, targetPath)
	}
	root, err := os.OpenRoot("testdata")
	if err != nil {
		t.Fatalf("open fixture root: %v", err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close fixture root: %v", err)
		}
	})
	raw, err := root.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	return string(raw)
}

// TestCheck_FixtureDataNotSource verifies that no corpus file has a suffix the docstring reader inspects.
func TestCheck_FixtureDataNotSource(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected a nonempty fixture corpus")
	}
	dir := newDocstringTestRepo(t, map[string]string{"pkg/seed.go": "package pkg\n"})
	for i := 0; i < len(entries); i++ {
		name := entries[i].Name()
		raw := readDocstringFixture(t, name, strings.TrimSuffix(name, ".txt"))
		writeTestFile(t, dir, filepath.Join("testdata", name), raw)
	}
	writeTestFile(t, dir, "pkg/covered.go", "package pkg\n\n// Covered provides a counted control.\nfunc Covered() {}\n")
	runGitCmd(t, dir, "add", ".")
	restore := docstrings.SetTargetFileReaderForTest(func(ctx context.Context, repoDir, headRef, rel string) ([]byte, bool, error) {
		if strings.HasPrefix(rel, "testdata/") {
			return nil, false, errors.New("fixture carries a source extension: " + rel)
		}
		return docstrings.ReadTargetFileDefault(ctx, repoDir, headRef, rel)
	})
	defer restore()
	rep, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	assertDocstringReport(t, "fixture data excluded; documented control counted", rep, err, true, 1, 1)
}

// runDocstringFixtureCase evaluates one fixture file in a fresh git repository and checks the report.
func runDocstringFixtureCase(t *testing.T, tc docstringFixtureCase) {
	t.Helper()
	raw := readDocstringFixture(t, tc.fixture, tc.targetPath)
	dir := newDocstringTestRepo(t, map[string]string{"pkg/seed.go": "package pkg\n"})
	writeTestFile(t, dir, tc.targetPath, raw)
	runGitCmd(t, dir, "add", ".")
	rep, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	assertDocstringReport(t, tc.name, rep, err, tc.passed, tc.counted, tc.covered)
	if tc.uncoveredName != "" {
		if len(rep.Uncovered) == 0 || rep.Uncovered[0].Name != tc.uncoveredName || rep.Uncovered[0].Line != tc.uncoveredLine {
			t.Fatalf("%s: want first uncovered %s:%d, got %+v", tc.name, tc.uncoveredName, tc.uncoveredLine, rep.Uncovered)
		}
	}
}

// TestCheck_RustMultilineSignaturesAndAttributes_3D verifies positive, negative, and boundary
// docstring detection for Rust wrapped signatures, where clauses, and multi-line attributes.
func TestCheck_RustMultilineSignaturesAndAttributes_3D(t *testing.T) {
	cases := []docstringFixtureCase{
		{"a_two_functions", "rust_a_two_functions.rs.txt", "src/lib.rs", true, 2, 2, "", 0},
		{"b_multiline_attr_oneline_fn", "rust_b_multiline_attr_oneline_fn.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"c_multiline_attr_wrapped_fn", "rust_c_multiline_attr_wrapped_fn.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"d_wrapped_where_clause", "rust_d_wrapped_where_clause.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"e1_wrapped_no_comment", "rust_e1_wrapped_no_comment.rs.txt", "src/lib.rs", false, 1, 0, "wrapped_undoc", 1},
		{"e2_wrapped_blank_line_separated", "rust_e2_wrapped_blank_line_separated.rs.txt", "src/lib.rs", false, 2, 1, "wrapped_after_blank", 7},
		{"e3_wrapped_param_comment_only", "rust_e3_wrapped_param_comment_only.rs.txt", "src/lib.rs", false, 1, 0, "wrapped_param_comment", 1},
		{"f_unclosed_attr_bound", "rust_f_unclosed_attr_bound.rs.txt", "src/lib.rs", false, 2, 0, "unclosed_attr", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDocstringFixtureCase(t, tc)
		})
	}
}

// TestCheck_WrappedSignaturesAcrossLanguages_3D verifies wrapped function signatures across Go,
// Python (with a multi-line decorator), TypeScript, and C (with the brace on its own line).
func TestCheck_WrappedSignaturesAcrossLanguages_3D(t *testing.T) {
	cases := []docstringFixtureCase{
		{"go_positive", "go_wrapped_pos.go.txt", "pkg/wrapped.go", true, 1, 1, "", 0},
		{"go_negative", "go_wrapped_neg.go.txt", "pkg/wrapped.go", false, 1, 0, "CombineUndoc", 3},
		{"python_positive", "python_wrapped_pos.py.txt", "app/wrapped.py", true, 1, 1, "", 0},
		{"python_negative", "python_wrapped_neg.py.txt", "app/wrapped.py", false, 1, 0, "combine_undoc", 5},
		{"typescript_positive", "ts_wrapped_pos.ts.txt", "web/wrapped.ts", true, 1, 1, "", 0},
		{"typescript_negative", "ts_wrapped_neg.ts.txt", "web/wrapped.ts", false, 1, 0, "combineUndoc", 1},
		{"c_positive", "c_wrapped_pos.c.txt", "src/wrapped.c", true, 1, 1, "", 0},
		{"c_negative", "c_wrapped_neg.c.txt", "src/wrapped.c", false, 1, 0, "combine_undoc", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDocstringFixtureCase(t, tc)
		})
	}
}

// TestCheck_Boundary_MaxAttributeLines_3D verifies the exact 32-line scalar bound on attribute
// skipping: a 32-line balanced attribute passes, a 33-line attribute fails as undocumented, and
// quoted brackets or trailing // comments inside attributes do not desynchronise the delimiter walk.
func TestCheck_Boundary_MaxAttributeLines_3D(t *testing.T) {
	dir := newDocstringTestRepo(t, map[string]string{"pkg/seed.go": "package pkg\n"})

	// Boundary positive: 32 attribute lines total (#[cfg_attr( + 30 inner lines + )]).
	exact32 := "/// Documented at the exact 32-line attribute bound.\n#[cfg_attr(\n" +
		strings.Repeat("    feature = \"a)]\", // trailing )]\n", 30) +
		")]\npub fn at_bound(\n    a: u32,\n) -> u32 {\n    a\n}\n"
	writeTestFile(t, dir, "src/lib.rs", exact32)
	runGitCmd(t, dir, "add", ".")
	repAt, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	assertDocstringReport(t, "exact 32-line attribute", repAt, err, true, 1, 1)

	// Boundary negative: 33 attribute lines total (#[cfg_attr( + 31 inner lines + )]).
	over33 := "/// Documented above a 33-line attribute exceeding the bound.\n#[cfg_attr(\n" +
		strings.Repeat("    feature = \"a\",\n", 31) +
		")]\npub fn over_bound(\n    a: u32,\n) -> u32 {\n    a\n}\n"
	writeTestFile(t, dir, "src/lib.rs", over33)
	repOver, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	assertDocstringReport(t, "33-line attribute exceeds bound", repOver, err, false, 1, 0)

	// Boundary positive: 16 complete annotations on one line.
	exact16 := "/// Documented at the exact 16-annotation line bound.\n" +
		strings.Repeat("#[inline] ", 16) + "\npub fn line_at_bound() {}\n"
	writeTestFile(t, dir, "src/lib.rs", exact16)
	rep16, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	assertDocstringReport(t, "exact 16 annotations on one line", rep16, err, true, 1, 1)

	// Boundary negative: 17 complete annotations on one line exceeds maxLineAttributes.
	over17 := "/// Documented above 17 annotations exceeding the per-line bound.\n" +
		strings.Repeat("#[inline] ", 17) + "\npub fn line_over_bound() {}\n"
	writeTestFile(t, dir, "src/lib.rs", over17)
	rep17, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
	assertDocstringReport(t, "17 annotations on one line exceeds bound", rep17, err, false, 1, 0)
}

// TestCheck_AttributeLiteralRegressions_3D covers quoted delimiters, malformed decorators,
// and lifetime apostrophes without changing the existing fixture corpus expectations.
func TestCheck_AttributeLiteralRegressions_3D(t *testing.T) {
	cases := []docstringFixtureCase{
		{"double_bracket", "ts_double_bracket.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"multiple_comments", "rust_attribute_multi_comment.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"single_bracket", "ts_single_bracket.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"single_url", "ts_single_url.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"single_multiline", "ts_single_multiline.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"unclosed_quote", "ts_unclosed_quote.ts.txt", "web/run.ts", false, 1, 0, "run", 4},
		{"unclosed_decorator", "ts_unclosed_decorator.ts.txt", "web/run.ts", false, 1, 0, "run", 4},
		{"python_single", "python_single_decorator.py.txt", "app/run.py", true, 1, 1, "", 0},
		{"python_triple", "python_triple_decorator.py.txt", "app/run.py", true, 1, 1, "", 0},
		{"rust_string_lifetime", "rust_string_lifetime.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"rust_char", "rust_char_attribute.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"rust_lifetime_argument", "rust_lifetime_attribute.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"previous_item", "rust_previous_inline.rs.txt", "src/lib.rs", false, 2, 1, "next", 3},
		{"trailing_comment", "rust_attribute_comment.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"inline_doc_comment", "rust_attribute_doc_comment.rs.txt", "src/lib.rs", false, 1, 0, "run", 2},
		{"inline_self", "rust_inline_self.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"rust_two_attributes", "rust_two_attributes.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"cpp_two_attributes", "cpp_two_attributes.cpp.txt", "src/lib.cpp", true, 1, 1, "", 0},
		{"ts_two_decorators", "ts_two_decorators.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"rust_three_attributes", "rust_three_attributes.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"rust_attribute_block_between", "rust_attribute_block_between.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"rust_two_attributes_previous_inline", "rust_two_attributes_previous_inline.rs.txt", "src/lib.rs", false, 2, 1, "next", 3},
		{"rust_attribute_trailing_code", "rust_attribute_trailing_code.rs.txt", "src/lib.rs", false, 1, 0, "run", 3},
		{"ts_template_bracket", "ts_template_bracket.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"ts_regex_paren", "ts_regex_paren.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"ts_template_substitution", "ts_template_substitution.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"ts_template_line_comment", "ts_template_line_comment.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"ts_template_block_comment", "ts_template_block_comment.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"ts_regex_quote", "ts_regex_quote.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"ts_template_multiline", "ts_template_multiline.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"ts_unclosed_template", "ts_unclosed_template.ts.txt", "web/run.ts", false, 1, 0, "run", 4},
		{"ts_unclosed_regex_class", "ts_unclosed_regex_class.ts.txt", "web/run.ts", false, 1, 0, "run", 4},
		{"rust_multiline_attr_line_comment", "rust_multiline_attr_line_comment.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"rust_multiline_attr_block_comment", "rust_multiline_attr_block_comment.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"ts_multiline_decorator_comment", "ts_multiline_decorator_comment.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"python_multiline_decorator_comment", "python_multiline_decorator_comment.py.txt", "app/run.py", true, 1, 1, "", 0},
		{"cpp_multiline_attribute_comment", "cpp_multiline_attribute_comment.cpp.txt", "src/lib.cpp", true, 1, 1, "", 0},
		{"rust_blank_above_attribute", "rust_blank_above_attribute.rs.txt", "src/lib.rs", false, 1, 0, "run", 7},
		{"rust_comment_after_attribute", "rust_comment_after_attribute.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"rust_unclosed_attr_comment", "rust_unclosed_attr_comment.rs.txt", "src/lib.rs", false, 1, 0, "run", 6},
		{"rust_multiline_attr_blank_line", "rust_multiline_attr_blank_line.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
	}
	cases = append(cases, lexicalScannerFixtureCases()...)
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) { runDocstringFixtureCase(t, tc) })
	}
}

// lexicalScannerFixtureCases returns the positive, negative, boundary, and undocumented-twin
// fixtures for forward lexical scanning across Rust, TypeScript, JavaScript, and C++.
func lexicalScannerFixtureCases() []docstringFixtureCase {
	return []docstringFixtureCase{
		{"rust_raw_string_attribute", "rust_raw_string_attribute.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"rust_raw_string_attribute_undoc", "rust_raw_string_attribute_undoc.rs.txt", "src/lib.rs", false, 1, 0, "run", 2},
		{"rust_unclosed_raw_string", "rust_unclosed_raw_string.rs.txt", "src/lib.rs", false, 1, 0, "run", 3},
		{"rust_unclosed_raw_string_undoc", "rust_unclosed_raw_string_undoc.rs.txt", "src/lib.rs", false, 1, 0, "run", 2},
		{"ts_template_inner_decorator", "ts_template_inner_decorator.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"ts_template_inner_decorator_undoc", "ts_template_inner_decorator_undoc.ts.txt", "web/run.ts", false, 1, 0, "run", 5},
		{"cpp_block_comment_inner_attribute", "cpp_block_comment_inner_attribute.cpp.txt", "src/lib.cpp", true, 1, 1, "", 0},
		{"cpp_block_comment_inner_attribute_undoc", "cpp_block_comment_inner_attribute_undoc.cpp.txt", "src/lib.cpp", false, 1, 0, "run", 7},
		{"rust_multiline_ordinary_string", "rust_multiline_ordinary_string.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"rust_multiline_ordinary_string_undoc", "rust_multiline_ordinary_string_undoc.rs.txt", "src/lib.rs", false, 1, 0, "run", 3},
		{"rust_unfinished_multiline_string", "rust_unfinished_multiline_string.rs.txt", "src/lib.rs", false, 1, 0, "run", 4},
		{"rust_unfinished_multiline_string_undoc", "rust_unfinished_multiline_string_undoc.rs.txt", "src/lib.rs", false, 1, 0, "run", 3},
		{"js_multiline_ordinary_string", "js_multiline_ordinary_string.js.txt", "web/run.js", false, 1, 0, "run", 5},
		{"js_multiline_ordinary_string_undoc", "js_multiline_ordinary_string_undoc.js.txt", "web/run.js", false, 1, 0, "run", 4},
		{"rust_multiline_attr_trailing_block_comment", "rust_multiline_attr_trailing_block_comment.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"rust_multiline_attr_trailing_block_comment_undoc", "rust_multiline_attr_trailing_block_comment_undoc.rs.txt", "src/lib.rs", false, 1, 0, "run", 5},
		{"rust_block_comment_before_attribute", "rust_block_comment_before_attribute.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"rust_block_comment_before_attribute_undoc", "rust_block_comment_before_attribute_undoc.rs.txt", "src/lib.rs", false, 1, 0, "run", 5},
		{"rust_nested_block_comment_attribute", "rust_nested_block_comment_attribute.rs.txt", "src/lib.rs", true, 1, 1, "", 0},
		{"rust_nested_block_comment_attribute_undoc", "rust_nested_block_comment_attribute_undoc.rs.txt", "src/lib.rs", false, 1, 0, "run", 6},
		{"cpp_raw_string_attribute", "cpp_raw_string_attribute.cpp.txt", "src/lib.cpp", true, 1, 1, "", 0},
		{"cpp_raw_string_attribute_undoc", "cpp_raw_string_attribute_undoc.cpp.txt", "src/lib.cpp", false, 1, 0, "run", 2},
		{"ts_template_nested_backtick", "ts_template_nested_backtick.ts.txt", "web/run.ts", true, 1, 1, "", 0},
		{"ts_template_nested_backtick_undoc", "ts_template_nested_backtick_undoc.ts.txt", "web/run.ts", false, 1, 0, "run", 3},
	}
}

// TestHasDocstring_AttributeItems_3D checks inline preceding items directly, including
// headers the C and TypeScript span scanners do not name as preceding functions.
func TestHasDocstring_AttributeItems_3D(t *testing.T) {
	cases := []struct {
		fixture, target string
		previous, next  int
	}{
		{"rust_previous_inline.rs.txt", "src/lib.rs", 2, 3},
		{"rust_two_attributes_previous_inline.rs.txt", "src/lib.rs", 2, 3},
		{"c_previous_inline.c.txt", "src/lib.c", 2, 3},
		{"ts_previous_inline.ts.txt", "web/run.ts", 3, 4},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.fixture, func(t *testing.T) {
			raw := readDocstringFixture(t, tc.fixture, tc.target)
			lines := strings.Split(raw, "\n")
			if !docstrings.HasDocstring(tc.target, []byte(raw), lines, hiss.FunctionSpan{Start: tc.previous, End: tc.previous}) {
				t.Fatal("previous item lost its own documentation")
			}
			if docstrings.HasDocstring(tc.target, []byte(raw), lines, hiss.FunctionSpan{Start: tc.next, End: tc.next + 2}) {
				t.Fatal("next item inherited previous documentation")
			}
		})
	}
}

// TestStripLiterals_3D checks the shared HISS literal and comment stripper's positive,
// malformed/unclosed, and multi-line boundary cases across supported languages.
func TestStripLiterals_3D(t *testing.T) {
	cases := []struct {
		name, language string
		lines, want    []string
		complete       bool
	}{
		{"typescript_single", "typescript", []string{"@tag(']')"}, []string{"@tag()"}, true},
		{"typescript_url", "typescript", []string{"@tag('https://x')"}, []string{"@tag()"}, true},
		{"typescript_template", "typescript", []string{"@tag(`]`)"}, []string{"@tag()"}, true},
		{"typescript_substitution", "typescript", []string{"@tag(`${a[0]}`)"}, []string{"@tag(a[0])"}, true},
		{"typescript_regex", "typescript", []string{"@tag(/[)]/)"}, []string{"@tag()"}, true},
		{"typescript_regex_quote", "typescript", []string{"@tag(/'/)"}, []string{"@tag()"}, true},
		{"typescript_multiline_template", "typescript", []string{"@tag(`a ]", "b`)"}, []string{"@tag(", ")"}, true},
		{"typescript_unclosed_quote", "typescript", []string{"@tag('unclosed)"}, []string{"@tag(unclosed)"}, false},
		{"typescript_unclosed_template", "typescript", []string{"@tag(`unclosed)"}, []string{"@tag("}, false},
		{"python_triple", "python", []string{"@tag('''a]", "#b)''')"}, []string{"@tag(", ")"}, true},
		{"python_unclosed_triple", "python", []string{"@tag('''x)"}, []string{"@tag("}, false},
		{"rust_unicode_separators", "rust", []string{"#[tag('\\u{5_d}')]"}, []string{"#[tag()]"}, true},

		{"rust_c_string", "rust", []string{`#[tag(c"]")]`}, []string{"#[tag()]"}, true},
		{"rust_raw_c_string", "rust", []string{`#[tag(cr#"]"#)]`}, []string{"#[tag()]"}, true},
		{"go_unicode_rune", "go", []string{`var r = '\u005d'`}, []string{"var r = "}, true},
		{"rust_character", "rust", []string{"#[tag(']')]"}, []string{"#[tag()]"}, true},
		{"rust_byte_character", "rust", []string{"#[tag(b']')]"}, []string{"#[tag()]"}, true},
		{"rust_byte_string", "rust", []string{"#[tag(b\"]\")]"}, []string{"#[tag()]"}, true},
		{"rust_raw_string", "rust", []string{"#[tag(r#\"use \"new_api]\"#)]"}, []string{"#[tag()]"}, true},
		{"rust_raw_byte_string", "rust", []string{"#[tag(br##\"a]\"#b\"##)]"}, []string{"#[tag()]"}, true},
		{"rust_unclosed_raw_string", "rust", []string{"#[tag(r#\"unclosed\")]"}, []string{"#[tag("}, false},
		{"rust_multiline_string", "rust", []string{"#[tag(\"use", "run_v2\")]"}, []string{"#[tag(", ")]"}, true},
		{"rust_nested_block_comment", "rust", []string{"#[tag(/* a /* )] */ b */)]"}, []string{"#[tag()]"}, true},
		{"rust_unclosed_nested_comment", "rust", []string{"#[tag(/* a /* b */)]"}, []string{"#[tag("}, false},
		{"cpp_raw_string", "cpp", []string{"[[deprecated(R\"msg(a )]] b)msg\")]]"}, []string{"[[deprecated()]]"}, true},
		{"cpp_unclosed_raw_string", "cpp", []string{"[[deprecated(R\"msg(a )]] b)\")]]"}, []string{"[[deprecated("}, false},
		{"typescript_nested_backtick", "typescript", []string{"@tag(`outer ${`inner )`}`)"}, []string{"@tag()"}, true},
		{"rust_lifetime", "rust", []string{"#[tag('a)]"}, []string{"#[tag('a)]"}, true},
		{"rust_comments", "rust", []string{"#[inline] /* x */ #[must_use] // y"}, []string{"#[inline]  #[must_use] "}, true},
		{"rust_unclosed_block_comment", "rust", []string{"#[inline] /* unclosed"}, []string{"#[inline] "}, false},
		{"empty_lines", "c", nil, []string{}, true},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			got, complete := hiss.StripLiterals(tc.lines, tc.language)
			if complete != tc.complete || !slices.Equal(got, tc.want) {
				t.Fatalf("got=%q complete=%v; want=%q complete=%v", got, complete, tc.want, tc.complete)
			}
		})
	}
}

// expectedFixtureSpans returns the pinned function spans for every fixture in testdata.
func expectedFixtureSpans() map[string][]hiss.FunctionSpan {
	run3 := []hiss.FunctionSpan{{Name: "run", Start: 3, End: 3}}
	run5 := []hiss.FunctionSpan{{Name: "run", Start: 5, End: 5}}
	run6 := []hiss.FunctionSpan{{Name: "run", Start: 6, End: 6}}
	run7 := []hiss.FunctionSpan{{Name: "run", Start: 7, End: 7}}
	prevNext := []hiss.FunctionSpan{{Name: "previous", Start: 2, End: 2}, {Name: "next", Start: 3, End: 5}}
	spans := map[string][]hiss.FunctionSpan{
		"c_previous_inline.c.txt":                     {{Name: "__attribute__", Start: 2, End: 2}, {Name: "next", Start: 3, End: 5}},
		"c_wrapped_neg.c.txt":                         {{Name: "combine_undoc", Start: 1, End: 6}},
		"c_wrapped_pos.c.txt":                         {{Name: "combine", Start: 2, End: 7}},
		"cpp_multiline_attribute_comment.cpp.txt":     run5,
		"cpp_two_attributes.cpp.txt":                  run3,
		"go_wrapped_neg.go.txt":                       {{Name: "CombineUndoc", Start: 3, End: 8}},
		"go_wrapped_pos.go.txt":                       {{Name: "Combine", Start: 4, End: 9}},
		"python_multiline_decorator_comment.py.txt":   {{Name: "run", Start: 6, End: 8}},
		"python_single_decorator.py.txt":              {{Name: "run", Start: 2, End: 4}},
		"python_triple_decorator.py.txt":              {{Name: "run", Start: 5, End: 7}},
		"python_wrapped_neg.py.txt":                   {{Name: "combine_undoc", Start: 5, End: 9}},
		"python_wrapped_pos.py.txt":                   {{Name: "combine", Start: 5, End: 10}},
		"rust_a_two_functions.rs.txt":                 {{Name: "one", Start: 2, End: 4}, {Name: "wrapped", Start: 7, End: 12}},
		"rust_attribute_block_between.rs.txt":         run3,
		"rust_attribute_comment.rs.txt":               run3,
		"rust_attribute_doc_comment.rs.txt":           {{Name: "run", Start: 2, End: 2}},
		"rust_attribute_multi_comment.rs.txt":         run3,
		"rust_attribute_trailing_code.rs.txt":         run3,
		"rust_b_multiline_attr_oneline_fn.rs.txt":     {{Name: "one_line", Start: 6, End: 8}},
		"rust_blank_above_attribute.rs.txt":           run7,
		"rust_c_multiline_attr_wrapped_fn.rs.txt":     {{Name: "wrapped", Start: 6, End: 11}},
		"rust_char_attribute.rs.txt":                  run3,
		"rust_comment_after_attribute.rs.txt":         run6,
		"rust_d_wrapped_where_clause.rs.txt":          {{Name: "clone_where", Start: 2, End: 9}},
		"rust_e1_wrapped_no_comment.rs.txt":           {{Name: "wrapped_undoc", Start: 1, End: 6}},
		"rust_e2_wrapped_blank_line_separated.rs.txt": {{Name: "previous_item", Start: 2, End: 4}, {Name: "wrapped_after_blank", Start: 7, End: 12}},
		"rust_e3_wrapped_param_comment_only.rs.txt":   {{Name: "wrapped_param_comment", Start: 1, End: 7}},
		"rust_f_unclosed_attr_bound.rs.txt":           {{Name: "unclosed_attr", Start: 3, End: 5}, {Name: "over_bound_attr", Start: 43, End: 45}},
		"rust_inline_self.rs.txt":                     {{Name: "run", Start: 2, End: 2}},
		"rust_lifetime_attribute.rs.txt":              run5,
		"rust_multiline_attr_blank_line.rs.txt":       run7,
		"rust_multiline_attr_block_comment.rs.txt":    run7,
		"rust_multiline_attr_line_comment.rs.txt":     run7,
		"rust_previous_inline.rs.txt":                 prevNext,
		"rust_string_lifetime.rs.txt":                 run3,
		"rust_three_attributes.rs.txt":                run3,
		"rust_two_attributes.rs.txt":                  run3,
		"rust_two_attributes_previous_inline.rs.txt":  prevNext,
		"rust_unclosed_attr_comment.rs.txt":           run6,
	}
	addTypeScriptFixtureSpans(spans)
	addLexicalScannerFixtureSpans(spans)
	addReferenceFixtureSpans(spans)
	return spans
}

// addLexicalScannerFixtureSpans populates the pinned function spans for the lexical scanner fixtures.
func addLexicalScannerFixtureSpans(spans map[string][]hiss.FunctionSpan) {
	run2 := []hiss.FunctionSpan{{Name: "run", Start: 2, End: 2}}
	run3 := []hiss.FunctionSpan{{Name: "run", Start: 3, End: 3}}
	run4 := []hiss.FunctionSpan{{Name: "run", Start: 4, End: 4}}
	run5 := []hiss.FunctionSpan{{Name: "run", Start: 5, End: 5}}
	run6 := []hiss.FunctionSpan{{Name: "run", Start: 6, End: 6}}
	run7 := []hiss.FunctionSpan{{Name: "run", Start: 7, End: 7}}
	run8 := []hiss.FunctionSpan{{Name: "run", Start: 8, End: 8}}
	spans["cpp_block_comment_inner_attribute.cpp.txt"] = run8
	spans["cpp_block_comment_inner_attribute_undoc.cpp.txt"] = run7
	spans["cpp_raw_string_attribute.cpp.txt"] = run3
	spans["cpp_raw_string_attribute_undoc.cpp.txt"] = run2
	spans["js_multiline_ordinary_string.js.txt"] = run5
	spans["js_multiline_ordinary_string_undoc.js.txt"] = run4
	spans["rust_block_comment_before_attribute.rs.txt"] = run7
	spans["rust_block_comment_before_attribute_undoc.rs.txt"] = run5
	spans["rust_multiline_attr_trailing_block_comment.rs.txt"] = run6
	spans["rust_multiline_attr_trailing_block_comment_undoc.rs.txt"] = run5
	spans["rust_multiline_ordinary_string.rs.txt"] = run4
	spans["rust_multiline_ordinary_string_undoc.rs.txt"] = run3
	spans["rust_nested_block_comment_attribute.rs.txt"] = run7
	spans["rust_nested_block_comment_attribute_undoc.rs.txt"] = run6
	spans["rust_raw_string_attribute.rs.txt"] = run3
	spans["rust_raw_string_attribute_undoc.rs.txt"] = run2
	spans["rust_unclosed_raw_string.rs.txt"] = run3
	spans["rust_unclosed_raw_string_undoc.rs.txt"] = run2
	spans["rust_unfinished_multiline_string.rs.txt"] = run4
	spans["rust_unfinished_multiline_string_undoc.rs.txt"] = run3
	spans["ts_template_inner_decorator.ts.txt"] = run6
	spans["ts_template_inner_decorator_undoc.ts.txt"] = run5
	spans["ts_template_nested_backtick.ts.txt"] = run4
	spans["ts_template_nested_backtick_undoc.ts.txt"] = run3
}

// addTypeScriptFixtureSpans populates the pinned function spans for TypeScript fixtures.
func addTypeScriptFixtureSpans(spans map[string][]hiss.FunctionSpan) {
	run4 := []hiss.FunctionSpan{{Name: "run", Start: 4, End: 4}}
	spans["ts_double_bracket.ts.txt"] = run4
	spans["ts_multiline_decorator_comment.ts.txt"] = []hiss.FunctionSpan{{Name: "run", Start: 8, End: 8}}
	spans["ts_previous_inline.ts.txt"] = []hiss.FunctionSpan{{Name: "next", Start: 4, End: 6}}
	spans["ts_regex_paren.ts.txt"] = run4
	spans["ts_regex_quote.ts.txt"] = run4
	spans["ts_single_bracket.ts.txt"] = run4
	spans["ts_single_multiline.ts.txt"] = []hiss.FunctionSpan{{Name: "run", Start: 6, End: 6}}
	spans["ts_single_url.ts.txt"] = run4
	spans["ts_template_block_comment.ts.txt"] = run4
	spans["ts_template_bracket.ts.txt"] = run4
	spans["ts_template_line_comment.ts.txt"] = run4
	spans["ts_template_multiline.ts.txt"] = []hiss.FunctionSpan{{Name: "run", Start: 5, End: 5}}
	spans["ts_template_substitution.ts.txt"] = run4
	spans["ts_two_decorators.ts.txt"] = run4
	spans["ts_unclosed_decorator.ts.txt"] = run4
	spans["ts_unclosed_quote.ts.txt"] = run4
	spans["ts_unclosed_regex_class.ts.txt"] = run4
	spans["ts_unclosed_template.ts.txt"] = run4
	spans["ts_wrapped_neg.ts.txt"] = []hiss.FunctionSpan{{Name: "combineUndoc", Start: 1, End: 6}}
	spans["ts_wrapped_pos.ts.txt"] = []hiss.FunctionSpan{{Name: "combine", Start: 2, End: 7}}
}

// TestFixtureScannerSpansAndFindings_3D verifies that every existing and new fixture in testdata
// produces identical HISS function spans and zero HISS findings before and after exporting StripLiterals.
func TestFixtureScannerSpansAndFindings_3D(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	wantMap := expectedFixtureSpans()
	if len(entries) != len(wantMap) {
		t.Fatalf("fixture count %d != pinned span count %d", len(entries), len(wantMap))
	}
	dir := t.TempDir()
	for i := 0; i < len(entries); i++ {
		name := entries[i].Name()
		want, exists := wantMap[name]
		if !exists {
			t.Fatalf("fixture %s has no pinned span expectation", name)
		}
		target := strings.TrimSuffix(name, ".txt")
		raw := readDocstringFixture(t, name, target)
		spans, err := hiss.ExtractFileFunctions(target, []byte(raw))
		if err != nil || !slices.Equal(spans, want) {
			t.Fatalf("%s: got spans=%+v err=%v, want %+v", name, spans, err, want)
		}
		writeTestFile(t, dir, target, raw)
	}
	rep, err := hiss.Scan(t.Context(), dir, hiss.ScanOptions{})
	if err != nil || rep.TotalInfractions != 0 {
		t.Fatalf("unexpected HISS findings across fixtures: err=%v violations=%+v", err, rep.Violations)
	}
}

// TestScanLines_3D verifies per-line start and end lexical classes, block-comment nesting depths,
// literal carry state, and unclosed literal markers across Rust, TypeScript, and C++.
func TestScanLines_3D(t *testing.T) {
	rustLines, ok := hiss.ScanLines([]string{
		"/// Doc comment.",
		"#[deprecated(note = \"use",
		"run_v2\")] /* note */",
		"",
	}, "rust")
	if !ok || len(rustLines) != 4 {
		t.Fatalf("ScanLines rust: ok=%v len=%d", ok, len(rustLines))
	}
	assertLexLine(t, "line 0", rustLines[0], hiss.LexDocComment, hiss.LexDocComment, false, true)
	assertLexLine(t, "line 1", rustLines[1], hiss.LexCode, hiss.LexLiteralString, true, false)
	assertLexLine(t, "line 2", rustLines[2], hiss.LexLiteralString, hiss.LexBlockComment, false, false)
	assertLexLine(t, "line 3", rustLines[3], hiss.LexBlank, hiss.LexBlank, false, false)

	unclosed, ok := hiss.ScanLines([]string{"#[tag(r#\"unclosed\")]"}, "rust")
	if ok || len(unclosed) != 1 {
		t.Fatalf("ScanLines unclosed raw string: ok=%v got=%+v", ok, unclosed)
	}
	assertLexLine(t, "unclosed raw", unclosed[0], hiss.LexCode, hiss.LexLiteralRawString, true, false)
}

// assertLexLine checks one LexLine's start/end classes, literal carry flag, and commentOnly state.
func assertLexLine(t *testing.T, label string, got hiss.LexLine, start, end hiss.LexClass, endLit, commentOnly bool) {
	t.Helper()
	if got.StartClass != start || got.EndClass != end || got.EndInLiteral != endLit || got.CommentOnly != commentOnly {
		t.Fatalf("%s: got %+v, want start=%v end=%v endLit=%v commentOnly=%v", label, got, start, end, endLit, commentOnly)
	}
}

// referenceFixture pins a lexical fixture's function location and documentation verdict.
type referenceFixture struct {
	name                                  string
	line                                  int
	covered, complete, visible, extracted bool
}

// referenceFixtures returns documented controls and their undocumented twins for every syntax family.
func referenceFixtures() []referenceFixture {
	var out []referenceFixture
	out = append(out, goReferenceFixtures()...)
	out = append(out, cReferenceFixtures()...)
	out = append(out, cppReferenceFixtures()...)
	out = append(out, rustReferenceFixtures()...)
	out = append(out, cudaReferenceFixtures()...)
	out = append(out, objectiveCppReferenceFixtures()...)
	out = append(out, javaReferenceFixtures()...)
	out = append(out, kotlinReferenceFixtures()...)
	out = append(out, swiftReferenceFixtures()...)
	out = append(out, pythonReferenceFixtures()...)
	out = append(out, javascriptReferenceFixtures()...)
	out = append(out, typescriptReferenceFixtures()...)
	out = append(out, svelteReferenceFixtures()...)
	return out
}

// goReferenceFixtures pins the go lexical reference controls.
func goReferenceFixtures() []referenceFixture {
	return []referenceFixture{
		{"lex_go_raw.go.txt", 4, true, true, true, true},
		{"lex_go_raw_undoc.go.txt", 3, false, true, true, true},
		{"lex_go_interpreted.go.txt", 4, true, true, true, true},
		{"lex_go_interpreted_undoc.go.txt", 3, false, true, true, true},
		{"lex_go_raw_multiline.go.txt", 6, true, true, true, true},
		{"lex_go_raw_multiline_undoc.go.txt", 5, false, true, true, true},
		{"lex_go_runes.go.txt", 9, true, true, true, true},
		{"lex_go_runes_undoc.go.txt", 8, false, true, true, true},
		{"lex_go_comments.go.txt", 4, true, true, true, true},
		{"lex_go_comments_undoc.go.txt", 3, false, true, true, true},
	}
}

// cReferenceFixtures pins the c lexical reference controls.
func cReferenceFixtures() []referenceFixture {
	return []referenceFixture{
		{"lex_c_macro_string.c.txt", 4, true, true, true, true},
		{"lex_c_macro_string_undoc.c.txt", 3, false, true, true, true},
		{"lex_c_strings.c.txt", 3, true, true, true, true},
		{"lex_c_strings_undoc.c.txt", 2, false, true, true, true},
		{"lex_c_characters.c.txt", 3, true, true, true, true},
		{"lex_c_characters_undoc.c.txt", 2, false, true, true, true},
		{"lex_c_comments.c.txt", 3, true, true, true, true},
		{"lex_c_comments_undoc.c.txt", 2, false, true, true, true},
		{"lex_c_line_splice.c.txt", 4, false, false, false, true},
		{"lex_c_line_splice_undoc.c.txt", 3, false, false, false, true},
		{"lex_c_split_comment.c.txt", 4, false, false, false, true},
		{"lex_c_split_comment_undoc.c.txt", 3, false, false, false, true},
		{"lex_c_trigraph.c.txt", 3, false, false, false, true},
		{"lex_c_trigraph_undoc.c.txt", 2, false, false, false, true},
	}
}

// cppReferenceFixtures pins the cpp lexical reference controls.
func cppReferenceFixtures() []referenceFixture {
	return []referenceFixture{
		{"lex_cpp_quote_delimiter.cpp.txt", 3, true, true, true, true},
		{"lex_cpp_quote_delimiter_undoc.cpp.txt", 2, false, true, true, true},
		{"lex_cpp_strings.cpp.txt", 3, true, true, true, true},
		{"lex_cpp_strings_undoc.cpp.txt", 2, false, true, true, true},
		{"lex_cpp_characters.cpp.txt", 3, true, true, true, true},
		{"lex_cpp_characters_undoc.cpp.txt", 2, false, true, true, true},
		{"lex_cpp_comments.cpp.txt", 3, true, true, true, true},
		{"lex_cpp_comments_undoc.cpp.txt", 2, false, true, true, true},
		{"lex_cpp_line_splice.cpp.txt", 4, false, false, false, true},
		{"lex_cpp_line_splice_undoc.cpp.txt", 3, false, false, false, true},
		{"lex_cpp_split_comment.cpp.txt", 4, false, false, false, true},
		{"lex_cpp_split_comment_undoc.cpp.txt", 3, false, false, false, true},
		{"lex_cpp_raw_strings.cpp.txt", 3, true, true, true, true},
		{"lex_cpp_raw_strings_undoc.cpp.txt", 2, false, true, true, true},
		{"lex_cpp_raw_multiline.cpp.txt", 5, true, true, true, true},
		{"lex_cpp_raw_multiline_undoc.cpp.txt", 4, false, true, true, true},
		{"lex_cpp_raw_delimiter_boundary.cpp.txt", 3, true, true, true, true},
		{"lex_cpp_raw_delimiter_boundary_undoc.cpp.txt", 2, false, true, true, true},
	}
}

// rustReferenceFixtures joins bounded reference fixture groups.
func rustReferenceFixtures() []referenceFixture {
	var out []referenceFixture
	out = append(out, rustReferenceFixtures0()...)
	out = append(out, rustReferenceFixtures1()...)
	return out
}

// rustReferenceFixtures0 pins the rust lexical reference controls.
func rustReferenceFixtures0() []referenceFixture {
	return []referenceFixture{
		{"lex_rust_seventeen_lines.rs.txt", 19, true, true, true, true},
		{"lex_rust_seventeen_lines_undoc.rs.txt", 18, false, true, true, true},
		{"lex_rust_sixteen_one_line.rs.txt", 3, true, true, true, true},
		{"lex_rust_sixteen_one_line_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_seventeen_one_line.rs.txt", 3, false, true, true, true},
		{"lex_rust_seventeen_one_line_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_attribute_item.rs.txt", 3, false, true, true, true},
		{"lex_rust_attribute_item_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_thirty_two_lines.rs.txt", 34, true, true, true, true},
		{"lex_rust_thirty_two_lines_undoc.rs.txt", 33, false, true, true, true},
		{"lex_rust_thirty_three_lines.rs.txt", 35, false, true, true, true},
		{"lex_rust_thirty_three_lines_undoc.rs.txt", 34, false, true, true, true},
		{"lex_rust_ordinary.rs.txt", 3, true, true, true, true},
		{"lex_rust_ordinary_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_byte.rs.txt", 3, true, true, true, true},
		{"lex_rust_byte_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_raw.rs.txt", 3, true, true, true, true},
		{"lex_rust_raw_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_raw_hash.rs.txt", 3, true, true, true, true},
		{"lex_rust_raw_hash_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_raw_byte.rs.txt", 3, true, true, true, true},
		{"lex_rust_raw_byte_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_c_string.rs.txt", 3, true, true, true, true},
		{"lex_rust_c_string_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_raw_c.rs.txt", 3, true, true, true, true},
		{"lex_rust_raw_c_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_char.rs.txt", 3, true, true, true, true},
		{"lex_rust_char_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_char_unicode.rs.txt", 3, true, true, true, true},
		{"lex_rust_char_unicode_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_char_utf8.rs.txt", 3, true, true, true, true},
		{"lex_rust_char_utf8_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_byte_char.rs.txt", 3, true, true, true, true},
		{"lex_rust_byte_char_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_multiline.rs.txt", 5, true, true, true, true},
		{"lex_rust_multiline_undoc.rs.txt", 4, false, true, true, true},
		{"lex_rust_multiline_byte_c.rs.txt", 5, true, true, true, true},
		{"lex_rust_multiline_byte_c_undoc.rs.txt", 4, false, true, true, true},
		{"lex_rust_raw_boundary.rs.txt", 3, true, true, true, true},
		{"lex_rust_raw_boundary_undoc.rs.txt", 2, false, true, true, true},
	}
}

// rustReferenceFixtures1 pins the rust lexical reference controls.
func rustReferenceFixtures1() []referenceFixture {
	return []referenceFixture{
		{"lex_rust_comments.rs.txt", 3, true, true, true, true},
		{"lex_rust_comments_undoc.rs.txt", 2, false, true, true, true},
		{"lex_rust_doc_comments.rs.txt", 3, true, true, true, true},
		{"lex_rust_doc_comments_undoc.rs.txt", 2, false, true, true, true},
	}
}

// cudaReferenceFixtures pins the cuda lexical reference controls.
func cudaReferenceFixtures() []referenceFixture {
	return []referenceFixture{
		{"lex_cuda_strings.cu.txt", 3, true, true, true, true},
		{"lex_cuda_strings_undoc.cu.txt", 2, false, true, true, true},
		{"lex_cuda_characters.cu.txt", 3, true, true, true, true},
		{"lex_cuda_characters_undoc.cu.txt", 2, false, true, true, true},
		{"lex_cuda_comments.cu.txt", 3, true, true, true, true},
		{"lex_cuda_comments_undoc.cu.txt", 2, false, true, true, true},
		{"lex_cuda_line_splice.cu.txt", 4, false, false, false, true},
		{"lex_cuda_line_splice_undoc.cu.txt", 3, false, false, false, true},
		{"lex_cuda_split_comment.cu.txt", 4, false, false, false, true},
		{"lex_cuda_split_comment_undoc.cu.txt", 3, false, false, false, true},
		{"lex_cuda_raw_strings.cu.txt", 3, true, true, true, true},
		{"lex_cuda_raw_strings_undoc.cu.txt", 2, false, true, true, true},
		{"lex_cuda_raw_multiline.cu.txt", 5, true, true, true, true},
		{"lex_cuda_raw_multiline_undoc.cu.txt", 4, false, true, true, true},
		{"lex_cuda_raw_delimiter_boundary.cu.txt", 3, true, true, true, true},
		{"lex_cuda_raw_delimiter_boundary_undoc.cu.txt", 2, false, true, true, true},
	}
}

// objectiveCppReferenceFixtures pins the objective_cpp lexical reference controls.
func objectiveCppReferenceFixtures() []referenceFixture {
	return []referenceFixture{
		{"lex_objective_cpp_strings.mm.txt", 3, true, true, true, true},
		{"lex_objective_cpp_strings_undoc.mm.txt", 2, false, true, true, true},
		{"lex_objective_cpp_characters.mm.txt", 3, true, true, true, true},
		{"lex_objective_cpp_characters_undoc.mm.txt", 2, false, true, true, true},
		{"lex_objective_cpp_comments.mm.txt", 3, true, true, true, true},
		{"lex_objective_cpp_comments_undoc.mm.txt", 2, false, true, true, true},
		{"lex_objective_cpp_line_splice.mm.txt", 4, false, false, false, true},
		{"lex_objective_cpp_line_splice_undoc.mm.txt", 3, false, false, false, true},
		{"lex_objective_cpp_split_comment.mm.txt", 4, false, false, false, true},
		{"lex_objective_cpp_split_comment_undoc.mm.txt", 3, false, false, false, true},
		{"lex_objective_cpp_raw_strings.mm.txt", 3, true, true, true, true},
		{"lex_objective_cpp_raw_strings_undoc.mm.txt", 2, false, true, true, true},
		{"lex_objective_cpp_raw_multiline.mm.txt", 5, true, true, true, true},
		{"lex_objective_cpp_raw_multiline_undoc.mm.txt", 4, false, true, true, true},
		{"lex_objective_cpp_raw_delimiter_boundary.mm.txt", 3, true, true, true, true},
		{"lex_objective_cpp_raw_delimiter_boundary_undoc.mm.txt", 2, false, true, true, true},
		{"lex_objective_cpp_objc_string.mm.txt", 3, true, true, true, true},
		{"lex_objective_cpp_objc_string_undoc.mm.txt", 2, false, true, true, true},
	}
}

// javaReferenceFixtures pins the java lexical reference controls.
func javaReferenceFixtures() []referenceFixture {
	return []referenceFixture{
		{"lex_java_strings_characters.java.txt", 3, true, true, true, true},
		{"lex_java_strings_characters_undoc.java.txt", 2, false, true, true, true},
		{"lex_java_text_block.java.txt", 7, true, true, true, true},
		{"lex_java_text_block_undoc.java.txt", 6, false, true, true, true},
		{"lex_java_comments.java.txt", 3, true, true, true, true},
		{"lex_java_comments_undoc.java.txt", 2, false, true, true, true},
		{"lex_java_unicode_translation.java.txt", 3, false, false, false, true},
		{"lex_java_unicode_translation_undoc.java.txt", 2, false, false, false, true},
	}
}

// kotlinReferenceFixtures pins the kotlin lexical reference controls.
func kotlinReferenceFixtures() []referenceFixture {
	return []referenceFixture{
		{"lex_kotlin_strings_characters.kt.txt", 3, true, true, true, true},
		{"lex_kotlin_strings_characters_undoc.kt.txt", 2, false, true, true, true},
		{"lex_kotlin_raw.kt.txt", 5, true, true, true, true},
		{"lex_kotlin_raw_undoc.kt.txt", 4, false, true, true, true},
		{"lex_kotlin_interpolation.kt.txt", 3, true, true, true, true},
		{"lex_kotlin_interpolation_undoc.kt.txt", 2, false, true, true, true},
		{"lex_kotlin_comments.kt.txt", 3, true, true, true, true},
		{"lex_kotlin_comments_undoc.kt.txt", 2, false, true, true, true},
		{"lex_kotlin_raw_quote_run.kt.txt", 3, true, true, true, true},
		{"lex_kotlin_raw_quote_run_undoc.kt.txt", 2, false, true, true, true},
	}
}

// swiftReferenceFixtures pins the swift lexical reference controls.
func swiftReferenceFixtures() []referenceFixture {
	return []referenceFixture{
		{"lex_swift_string.swift.txt", 3, true, true, true, true},
		{"lex_swift_string_undoc.swift.txt", 2, false, true, true, true},
		{"lex_swift_multiline.swift.txt", 7, true, true, true, true},
		{"lex_swift_multiline_undoc.swift.txt", 6, false, true, true, true},
		{"lex_swift_extended.swift.txt", 3, true, true, true, true},
		{"lex_swift_extended_undoc.swift.txt", 2, false, true, true, true},
		{"lex_swift_extended_multiline.swift.txt", 7, true, true, true, true},
		{"lex_swift_extended_multiline_undoc.swift.txt", 6, false, true, true, true},
		{"lex_swift_interpolation.swift.txt", 3, true, true, true, true},
		{"lex_swift_interpolation_undoc.swift.txt", 2, false, true, true, true},
		{"lex_swift_comments.swift.txt", 3, true, true, true, true},
		{"lex_swift_comments_undoc.swift.txt", 2, false, true, true, true},
		{"lex_swift_regex.swift.txt", 3, false, false, false, false},
		{"lex_swift_regex_undoc.swift.txt", 2, false, false, false, false},
		{"lex_swift_extended_regex.swift.txt", 5, false, false, false, false},
		{"lex_swift_extended_regex_undoc.swift.txt", 4, false, false, false, false},
	}
}

// pythonReferenceFixtures pins the python lexical reference controls.
func pythonReferenceFixtures() []referenceFixture {
	return []referenceFixture{
		{"lex_python_single.py.txt", 2, true, true, true, true},
		{"lex_python_single_undoc.py.txt", 2, false, true, true, true},
		{"lex_python_double.py.txt", 2, true, true, true, true},
		{"lex_python_double_undoc.py.txt", 2, false, true, true, true},
		{"lex_python_triple_single.py.txt", 4, true, true, true, true},
		{"lex_python_triple_single_undoc.py.txt", 4, false, true, true, true},
		{"lex_python_triple_double.py.txt", 4, true, true, true, true},
		{"lex_python_triple_double_undoc.py.txt", 4, false, true, true, true},
		{"lex_python_prefixes.py.txt", 2, true, true, true, true},
		{"lex_python_prefixes_undoc.py.txt", 2, false, true, true, true},
		{"lex_python_bytes.py.txt", 2, true, true, true, true},
		{"lex_python_bytes_undoc.py.txt", 2, false, true, true, true},
		{"lex_python_raw_triple.py.txt", 4, true, true, true, true},
		{"lex_python_raw_triple_undoc.py.txt", 4, false, true, true, true},
		{"lex_python_formatted.py.txt", 2, true, true, true, true},
		{"lex_python_formatted_undoc.py.txt", 2, false, true, true, true},
		{"lex_python_formatted_triple.py.txt", 4, true, true, true, true},
		{"lex_python_formatted_triple_undoc.py.txt", 4, false, true, true, true},
		{"lex_python_formatted_raw.py.txt", 2, true, true, true, true},
		{"lex_python_formatted_raw_undoc.py.txt", 2, false, true, true, true},
		{"lex_python_formatted_nested.py.txt", 2, true, true, true, true},
		{"lex_python_formatted_nested_undoc.py.txt", 2, false, true, true, true},
		{"lex_python_formatted_format.py.txt", 2, true, true, true, true},
		{"lex_python_formatted_format_undoc.py.txt", 2, false, true, true, true},
		{"lex_python_template.py.txt", 2, true, true, true, true},
		{"lex_python_template_undoc.py.txt", 2, false, true, true, true},
		{"lex_python_comments.py.txt", 2, true, true, true, true},
		{"lex_python_comments_undoc.py.txt", 2, false, true, true, true},
	}
}

// javascriptReferenceFixtures pins the javascript lexical reference controls.
func javascriptReferenceFixtures() []referenceFixture {
	return []referenceFixture{
		{"lex_javascript_strings.js.txt", 3, true, true, true, true},
		{"lex_javascript_strings_undoc.js.txt", 2, false, true, true, true},
		{"lex_javascript_continued_string.js.txt", 4, true, true, true, true},
		{"lex_javascript_continued_string_undoc.js.txt", 3, false, true, true, true},
		{"lex_javascript_template.js.txt", 5, true, true, true, true},
		{"lex_javascript_template_undoc.js.txt", 4, false, true, true, true},
		{"lex_javascript_nested_template.js.txt", 3, true, true, true, true},
		{"lex_javascript_nested_template_undoc.js.txt", 2, false, true, true, true},
		{"lex_javascript_regex.js.txt", 3, true, true, true, true},
		{"lex_javascript_regex_undoc.js.txt", 2, false, true, true, true},
		{"lex_javascript_comments.js.txt", 3, true, true, true, true},
		{"lex_javascript_comments_undoc.js.txt", 2, false, true, true, true},
		{"lex_javascript_legacy_comments.js.txt", 5, true, true, true, true},
		{"lex_javascript_legacy_comments_undoc.js.txt", 4, false, true, true, true},
		{"lex_javascript_hashbang.js.txt", 4, true, true, true, true},
		{"lex_javascript_hashbang_undoc.js.txt", 3, false, true, true, true},
		{"lex_javascript_regex_statement.js.txt", 3, false, false, false, false},
		{"lex_javascript_regex_statement_undoc.js.txt", 2, false, false, false, false},
		{"lex_javascript_nesting_boundary.js.txt", 3, true, true, true, true},
		{"lex_javascript_nesting_boundary_undoc.js.txt", 2, false, true, true, true},
		{"lex_javascript_nesting_overflow.js.txt", 3, false, false, false, false},
		{"lex_javascript_nesting_overflow_undoc.js.txt", 2, false, false, false, false},
	}
}

// typescriptReferenceFixtures pins the typescript lexical reference controls.
func typescriptReferenceFixtures() []referenceFixture {
	return []referenceFixture{
		{"lex_typescript_strings.ts.txt", 3, true, true, true, true},
		{"lex_typescript_strings_undoc.ts.txt", 2, false, true, true, true},
		{"lex_typescript_continued_string.ts.txt", 4, true, true, true, true},
		{"lex_typescript_continued_string_undoc.ts.txt", 3, false, true, true, true},
		{"lex_typescript_template.ts.txt", 5, true, true, true, true},
		{"lex_typescript_template_undoc.ts.txt", 4, false, true, true, true},
		{"lex_typescript_nested_template.ts.txt", 3, true, true, true, true},
		{"lex_typescript_nested_template_undoc.ts.txt", 2, false, true, true, true},
		{"lex_typescript_regex.ts.txt", 3, true, true, true, true},
		{"lex_typescript_regex_undoc.ts.txt", 2, false, true, true, true},
		{"lex_typescript_comments.ts.txt", 3, true, true, true, true},
		{"lex_typescript_comments_undoc.ts.txt", 2, false, true, true, true},
		{"lex_typescript_legacy_comments.ts.txt", 5, true, true, true, true},
		{"lex_typescript_legacy_comments_undoc.ts.txt", 4, false, true, true, true},
		{"lex_typescript_hashbang.ts.txt", 4, true, true, true, true},
		{"lex_typescript_hashbang_undoc.ts.txt", 3, false, true, true, true},
		{"lex_typescript_regex_statement.ts.txt", 3, false, false, false, false},
		{"lex_typescript_regex_statement_undoc.ts.txt", 2, false, false, false, false},
		{"lex_typescript_nesting_boundary.ts.txt", 3, true, true, true, true},
		{"lex_typescript_nesting_boundary_undoc.ts.txt", 2, false, true, true, true},
		{"lex_typescript_nesting_overflow.ts.txt", 3, false, false, false, false},
		{"lex_typescript_nesting_overflow_undoc.ts.txt", 2, false, false, false, false},
	}
}

// svelteReferenceFixtures pins the svelte lexical reference controls.
func svelteReferenceFixtures() []referenceFixture {
	return []referenceFixture{
		{"lex_svelte_strings.svelte.txt", 4, true, true, true, true},
		{"lex_svelte_strings_undoc.svelte.txt", 3, false, true, true, true},
		{"lex_svelte_continued_string.svelte.txt", 5, true, true, true, true},
		{"lex_svelte_continued_string_undoc.svelte.txt", 4, false, true, true, true},
		{"lex_svelte_template.svelte.txt", 6, true, true, true, true},
		{"lex_svelte_template_undoc.svelte.txt", 5, false, true, true, true},
		{"lex_svelte_nested_template.svelte.txt", 4, true, true, true, true},
		{"lex_svelte_nested_template_undoc.svelte.txt", 3, false, true, true, true},
		{"lex_svelte_regex.svelte.txt", 4, true, true, true, true},
		{"lex_svelte_regex_undoc.svelte.txt", 3, false, true, true, true},
		{"lex_svelte_comments.svelte.txt", 4, true, true, true, true},
		{"lex_svelte_comments_undoc.svelte.txt", 3, false, true, true, true},
		{"lex_svelte_legacy_comments.svelte.txt", 6, true, true, true, true},
		{"lex_svelte_legacy_comments_undoc.svelte.txt", 5, false, true, true, true},
		{"lex_svelte_hashbang.svelte.txt", 5, true, true, true, true},
		{"lex_svelte_hashbang_undoc.svelte.txt", 4, false, true, true, true},
		{"lex_svelte_regex_statement.svelte.txt", 4, false, false, false, false},
		{"lex_svelte_regex_statement_undoc.svelte.txt", 3, false, false, false, false},
		{"lex_svelte_nesting_boundary.svelte.txt", 4, true, true, true, true},
		{"lex_svelte_nesting_boundary_undoc.svelte.txt", 3, false, true, true, true},
		{"lex_svelte_nesting_overflow.svelte.txt", 4, false, false, false, false},
		{"lex_svelte_nesting_overflow_undoc.svelte.txt", 3, false, false, false, false},
		{"lex_svelte_markup_comments.svelte.txt", 14, true, true, true, true},
		{"lex_svelte_markup_comments_undoc.svelte.txt", 13, false, true, true, true},
	}
}

// TestReferenceLexicalForms_3D checks reference forms and explicitly rejected constructs against fixture twins.
func TestReferenceLexicalForms_3D(t *testing.T) {
	cases := referenceFixtures()
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) { checkReferenceFixture(t, tc) })
	}
}

// checkReferenceFixture verifies a reference fixture's documentation and lexical visibility independently.
func checkReferenceFixture(t *testing.T, tc referenceFixture) {
	t.Helper()
	target := strings.TrimSuffix(tc.name, ".txt")
	raw := readDocstringFixture(t, tc.name, target)
	lines := strings.Split(raw, "\n")
	fn := hiss.FunctionSpan{Name: "run", Start: tc.line, End: tc.line}
	if got := docstrings.HasDocstring(target, []byte(raw), lines, fn); got != tc.covered {
		t.Errorf("documentation = %v, want %v at line %d", got, tc.covered, tc.line)
	}
	lexed, complete := hiss.ScanLines(lines, util.SourceLanguage(target))
	if complete != tc.complete {
		t.Errorf("complete = %v, want %v", complete, tc.complete)
	}
	visible := lexed[tc.line-1].StartClass == hiss.LexCode && strings.Contains(lexed[tc.line-1].Code, "run(")
	if visible != tc.visible {
		t.Errorf("function visible = %v, want %v: %+v", visible, tc.visible, lexed[tc.line-1])
	}
}

// addReferenceFixtureSpans pins reference spans, including explicit empty expectations for rejected forms.
func addReferenceFixtureSpans(spans map[string][]hiss.FunctionSpan) {
	spans["lex_empty.rs.txt"] = nil
	cases := referenceFixtures()
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		spans[tc.name] = nil
		if tc.extracted {
			spans[tc.name] = []hiss.FunctionSpan{{Name: "run", Start: tc.line, End: tc.line}}
		}
	}
}

// TestPythonDocstringPrefixes_3D separates constant text docstrings from bytes and interpolated values.
func TestPythonDocstringPrefixes_3D(t *testing.T) {
	cases := []struct {
		body    string
		covered bool
	}{
		{`"Run the operation."`, true},
		{`r"Run the operation."`, true},
		{`U"Run the operation."`, true},
		{`b"No text docstring."`, false},
		{`f"No constant docstring."`, false},
		{`rf"No constant docstring."`, false},
		{`t"No constant docstring."`, false},
		{`r`, false},
		{``, false},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		raw := "def run():\n    " + tc.body + "\n"
		fn := hiss.FunctionSpan{Name: "run", Start: 1, End: 2}
		if got := docstrings.HasDocstring("run.py", []byte(raw), strings.Split(raw, "\n"), fn); got != tc.covered {
			t.Errorf("body %q: documentation = %v, want %v", tc.body, got, tc.covered)
		}
	}
}

// TestUnrecognisedLexicalFilesFailClosed_3D refuses incomplete source when extraction loses every candidate.
func TestUnrecognisedLexicalFilesFailClosed_3D(t *testing.T) {
	cases := referenceFixtures()
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		if tc.complete || tc.extracted {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			target := strings.TrimSuffix(tc.name, ".txt")
			raw := readDocstringFixture(t, tc.name, target)
			dir := newDocstringTestRepo(t, map[string]string{"seed.go": "package seed\n"})
			writeTestFile(t, dir, target, raw)
			runGitCmd(t, dir, "add", ".")
			rep, err := docstrings.Check(t.Context(), docstrings.Options{RepoDir: dir, BaseRef: "origin/main"})
			if err == nil || !strings.Contains(err.Error(), "lexically incomplete") {
				t.Fatalf("incomplete source passed without an explicit lexical error: report=%+v err=%v", rep, err)
			}
		})
	}
}
