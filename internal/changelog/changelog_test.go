package changelog

import (
	"fmt"
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
	remaining, _, err := LoadFragments(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
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

func TestRenderRelease_Positive_IssueHashNormalisation(t *testing.T) {
	tmpDir := t.TempDir()

	_, err := CreateFragment(tmpDir, Fragment{
		Type:  TypeFixed,
		Title: "Fix with hash prefix",
		Issue: "#502",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = CreateFragment(tmpDir, Fragment{
		Type:  TypeFixed,
		Title: "Fix without hash prefix",
		Issue: "502",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = CreateFragment(tmpDir, Fragment{
		Type:  TypeFixed,
		Title: "Mixed list fix",
		Issue: "5, owner/repo#6",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := RenderRelease(tmpDir, "1.2.0", "2026-10-09"); err != nil {
		t.Fatalf("RenderRelease failed: %v", err)
	}

	changelogFile := filepath.Join(tmpDir, "CHANGELOG.md")
	data, err := os.ReadFile(changelogFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "- Fix with hash prefix (#502)") {
		t.Errorf("expected '- Fix with hash prefix (#502)', got:\n%s", content)
	}
	if !strings.Contains(content, "- Fix without hash prefix (#502)") {
		t.Errorf("expected '- Fix without hash prefix (#502)', got:\n%s", content)
	}
	if !strings.Contains(content, "- Mixed list fix (#5, owner/repo#6)") {
		t.Errorf("expected '- Mixed list fix (#5, owner/repo#6)', got:\n%s", content)
	}
}

func TestLoadFragments_Negative_InvalidIssue(t *testing.T) {
	tests := []struct {
		name  string
		issue string
	}{
		{name: "double-hash", issue: "##502"},
		{name: "space-after-hash", issue: "# 502"},
		{name: "leading-zero", issue: "007"},
		{name: "zero", issue: "0"},
		{name: "alpha", issue: "abc"},
		{name: "empty", issue: ""},
		{name: "whitespace-only", issue: "   "},
		{name: "trailing-comma", issue: "502,"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			dir := filepath.Join(tmpDir, FragmentDir)
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			fragFile := filepath.Join(dir, "20261009-invalid.yaml")
			content := fmt.Sprintf("type: fixed\ntitle: Invalid issue test\nissue: %q\n", tc.issue)
			if err := os.WriteFile(fragFile, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			_, _, err := LoadFragments(tmpDir)
			if err == nil {
				t.Fatalf("expected validation error for issue %q, got nil", tc.issue)
			}
			if !strings.Contains(err.Error(), fragFile) {
				t.Errorf("error %q should name the fragment path %s", err.Error(), fragFile)
			}
		})
	}
}

func TestNormaliseIssue_Positive(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "single-value-bare", input: "502", want: "#502"},
		{name: "single-value-hash", input: "#502", want: "#502"},
		{name: "comma-list-hashes", input: "#348, #349", want: "#348, #349"},
		{name: "comma-list-bare", input: "565, 604", want: "#565, #604"},
		{name: "comma-list-mixed-hashes", input: "565, #604", want: "#565, #604"},
		{name: "comma-list-spaces", input: "332,   #334, #351", want: "#332, #334, #351"},
		{name: "owner-repo", input: "owner/repo#5", want: "owner/repo#5"},
		{name: "mixed-list", input: "5, owner/repo#6", want: "#5, owner/repo#6"},
		{name: "mixed-list-hashes", input: "#5, owner/repo#6", want: "#5, owner/repo#6"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normaliseIssue(tc.input)
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("normaliseIssue(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestNormaliseIssue_Negative(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "double-hash", input: "##502"},
		{name: "space-after-hash", input: "# 502"},
		{name: "leading-zero", input: "007"},
		{name: "zero", input: "0"},
		{name: "alpha", input: "abc"},
		{name: "empty", input: ""},
		{name: "whitespace-only", input: "   "},
		{name: "trailing-comma", input: "502,"},
		{name: "leading-comma", input: ",502"},
		{name: "empty-component", input: "502,,503"},
		{name: "hash-before-owner-repo", input: "#owner/repo#5"},
		{name: "owner-repo-no-num", input: "owner/repo#"},
		{name: "owner-repo-zero", input: "owner/repo#0"},
		{name: "owner-repo-leading-zero", input: "owner/repo#007"},
		{name: "owner-repo-alpha", input: "owner/repo#abc"},
		{name: "owner-repo-invalid", input: "owner/repo/sub#5"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normaliseIssue(tc.input)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tc.input)
			}
		})
	}
}

func TestNormaliseIssue_Boundary(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "min-int-bare", input: "1", want: "#1"},
		{name: "min-int-hash", input: "#1", want: "#1"},
		{name: "single-char-owner-repo", input: "a/b#1", want: "a/b#1"},
		{name: "large-issue-number", input: "999999999", want: "#999999999"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normaliseIssue(tc.input)
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("normaliseIssue(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}
