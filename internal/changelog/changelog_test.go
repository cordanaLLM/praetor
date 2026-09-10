package changelog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAndLoadFragments_Positive(t *testing.T) {
	tmpDir := t.TempDir()

	f1 := Fragment{
		Type:     TypeAdded,
		Title:    "Add HISS multi-language invariant scanning engine",
		Issue:    "42",
		Breaking: false,
	}
	f2 := Fragment{
		Type:     TypeSecurity,
		Title:    "Remediate secret leaks across git history",
		Breaking: false,
	}

	p1, err := CreateFragment(tmpDir, f1)
	if err != nil {
		t.Fatalf("CreateFragment failed: %v", err)
	}
	if p1 == "" {
		t.Fatal("expected non-empty path")
	}

	_, err = CreateFragment(tmpDir, f2)
	if err != nil {
		t.Fatalf("CreateFragment failed: %v", err)
	}

	fragments, files, err := LoadFragments(tmpDir)
	if err != nil {
		t.Fatalf("LoadFragments failed: %v", err)
	}
	if len(fragments) != 2 {
		t.Fatalf("expected 2 fragments, got %d", len(fragments))
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
}

func TestRenderRelease_Positive(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := CreateFragment(tmpDir, Fragment{
		Type:     TypeAdded,
		Title:    "Fleet dependency unification subsystem",
		Breaking: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = CreateFragment(tmpDir, Fragment{
		Type:  TypeFixed,
		Title: "Fix race condition in worktree manager",
		Issue: "101",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = RenderRelease(tmpDir, "1.1.0", "2026-09-11")
	if err != nil {
		t.Fatalf("RenderRelease failed: %v", err)
	}

	// CHANGELOG.md should exist
	changelogFile := filepath.Join(tmpDir, "CHANGELOG.md")
	data, err := os.ReadFile(changelogFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "## [1.1.0] - 2026-09-11") {
		t.Errorf("missing version header in changelog:\n%s", content)
	}
	if !strings.Contains(content, "- **BREAKING**: Fleet dependency unification subsystem") {
		t.Errorf("missing breaking change notation in changelog:\n%s", content)
	}
	if !strings.Contains(content, "- Fix race condition in worktree manager (#101)") {
		t.Errorf("missing fix entry in changelog:\n%s", content)
	}

	// changelog.d should now have 0 fragments
	remaining, _, _ := LoadFragments(tmpDir)
	if len(remaining) != 0 {
		t.Errorf("expected 0 remaining fragments after render, got %d", len(remaining))
	}
}

func TestCreateFragment_Negative_EmptyTitleOrInvalidType(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := CreateFragment(tmpDir, Fragment{Type: TypeAdded, Title: ""})
	if err == nil {
		t.Error("expected error with empty title")
	}

	_, err = CreateFragment(tmpDir, Fragment{Type: "invalid-type", Title: "Valid title"})
	if err == nil {
		t.Error("expected error with invalid fragment type")
	}
}

func TestLoadFragments_Boundary_EmptyOrMissingDir(t *testing.T) {
	tmpDir := t.TempDir()

	frags, files, err := LoadFragments(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error for missing changelog.d: %v", err)
	}
	if len(frags) != 0 || len(files) != 0 {
		t.Errorf("expected 0 fragments, got %d", len(frags))
	}
}
