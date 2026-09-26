package editor

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

func writeScratchFixture(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Negative: the scratch trees every repository walker shares hold agent state and whole copies
// of the checkout, not the workspace's own source, so their files add no language.
func TestDetectWorkspaceLanguages_Negative_ScratchTreesAreSkipped(t *testing.T) {
	root := t.TempDir()
	writeScratchFixture(t, root, "app.ts")
	for _, rel := range []string{
		".standards/worktrees/gate/src/lib.rs",
		".claude/worktrees/agent/main.go",
		".claude/skills/tool/run.py",
		".workingdir/notes.sh",
		".workingdir2/probe.c",
	} {
		writeScratchFixture(t, root, rel)
	}

	languages, err := DetectWorkspaceLanguages(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"rust", "go", "python", "shell", "c"} {
		if slices.Contains(languages, lang) {
			t.Fatalf("a scratch tree reported %s: %v", lang, languages)
		}
	}
	if !slices.Contains(languages, "typescript") {
		t.Fatalf("the workspace's own source was lost: %v", languages)
	}
}

// Boundary: a worktree under .standards is a full checkout copy, so a checkout carrying one no
// longer trips the file bound that the workspace itself stays well under.
func TestDetectWorkspaceLanguages_Boundary_StandardsWorktreeDoesNotExhaustTheBound(t *testing.T) {
	root := t.TempDir()
	writeScratchFixture(t, root, "main.go")
	for i := 0; i <= maxWorkspaceFiles; i++ {
		writeScratchFixture(t, root, filepath.ToSlash(filepath.Join(".standards", "worktrees", "gate", "pkg", "f"+strconv.Itoa(i)+".txt")))
	}

	languages, err := DetectWorkspaceLanguages(root)
	if err != nil {
		t.Fatalf("a .standards worktree exhausted the workspace bound: %v", err)
	}
	if !slices.Contains(languages, "go") {
		t.Fatalf("the workspace's own source was lost: %v", languages)
	}
}

// Positive: an ordinary directory whose name merely resembles a scratch root is scanned.
func TestDetectWorkspaceLanguages_Positive_LookalikeDirectoriesAreScanned(t *testing.T) {
	root := t.TempDir()
	writeScratchFixture(t, root, "claude/tool.rs")
	writeScratchFixture(t, root, "standards/check.py")

	languages, err := DetectWorkspaceLanguages(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"rust", "python"} {
		if !slices.Contains(languages, lang) {
			t.Fatalf("lookalike directory lost %s: %v", lang, languages)
		}
	}
}
