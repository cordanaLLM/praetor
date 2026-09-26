package forge

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// approvedDiscussion is a discussion TranscribeDiscussionToADR accepts.
func approvedDiscussion() Discussion {
	return Discussion{
		ID:           7,
		Title:        "Confined Outputs",
		Status:       "approved",
		ContextText:  "Generated documents land inside the repository.",
		DecisionText: "Write them through the confined writers.",
	}
}

// linkOrSkip creates a symbolic link or skips on a platform that refuses one.
func linkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(link), err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
}

// requireEmptyDir fails when dir holds any entry: nothing may have been written there.
func requireEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Errorf("expected nothing written to %s, got %v", dir, entries)
	}
}

func TestGeneratedDir_Positive_RelativeOutputsLandInsideTheRepository(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "site"), 0o700); err != nil {
		t.Fatalf("mkdir site: %v", err)
	}
	linkOrSkip(t, "site", filepath.Join(root, "docs"))

	manifest, err := GenerateWiki(t.Context(), root, filepath.Join("docs", "wiki"))
	if err != nil {
		t.Fatalf("GenerateWiki through an in-root link: %v", err)
	}
	for _, page := range manifest.Pages {
		if page.Path != filepath.Join(root, "docs", "wiki", page.Name) {
			t.Errorf("page path = %q, want it joined onto the repository root", page.Path)
		}
		if _, err := os.Stat(filepath.Join(root, "site", "wiki", page.Name)); err != nil {
			t.Errorf("wiki page %s missing at the in-root link target: %v", page.Name, err)
		}
	}

	adr, err := TranscribeDiscussionToADR(t.Context(), approvedDiscussion(), root, filepath.Join("docs", "adr"))
	if err != nil {
		t.Fatalf("TranscribeDiscussionToADR through an in-root link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "site", "adr", "0001-confined-outputs.md")); err != nil {
		t.Errorf("ADR missing at the in-root link target: %v", err)
	}
	if adr.FilePath != filepath.Join(root, "docs", "adr", "0001-confined-outputs.md") {
		t.Errorf("ADR path = %q, want it joined onto the repository root", adr.FilePath)
	}
}

// TestGeneratedDir_Negative_EscapingLinkIsRefused is BUG-826 at the forge generators: a
// repository shipping docs/wiki or docs/adr as a link to a directory outside it used to get
// the pages written there, because the root-less writers follow every ancestor.
func TestGeneratedDir_Negative_EscapingLinkIsRefused(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	linkOrSkip(t, outside, filepath.Join(root, "docs", "wiki"))
	linkOrSkip(t, outside, filepath.Join(root, "docs", "adr"))

	if _, err := GenerateWiki(t.Context(), root, filepath.Join("docs", "wiki")); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Errorf("GenerateWiki through an escaping link = %v, want ErrPathEscapesRoot", err)
	}
	if _, err := TranscribeDiscussionToADR(t.Context(), approvedDiscussion(), root, filepath.Join("docs", "adr")); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Errorf("TranscribeDiscussionToADR through an escaping link = %v, want ErrPathEscapesRoot", err)
	}
	if _, err := GenerateWiki(t.Context(), root, filepath.Join("..", "wiki")); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Errorf("GenerateWiki climbing out with .. = %v, want ErrPathEscapesRoot", err)
	}
	requireEmptyDir(t, outside)
}

// TestGeneratedDir_Boundary_AncestorLinkAndAbsoluteChoice pins both edges of the contract:
// a link at an ancestor of a relative output is refused like one at the output itself,
// while an absolute output naming the same outside directory is the operator's explicit
// choice and is written as given.
func TestGeneratedDir_Boundary_AncestorLinkAndAbsoluteChoice(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	linkOrSkip(t, outside, filepath.Join(root, "docs"))

	if _, err := GenerateWiki(t.Context(), root, filepath.Join("docs", "wiki")); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Errorf("GenerateWiki below an escaping ancestor = %v, want ErrPathEscapesRoot", err)
	}
	requireEmptyDir(t, outside)

	if _, err := GenerateWiki(t.Context(), root, "."); err != nil {
		t.Fatalf("GenerateWiki into the repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Home.md")); err != nil {
		t.Errorf("Home.md missing at the repository root: %v", err)
	}

	absolute := filepath.Join(outside, "wiki")
	if _, err := GenerateWiki(t.Context(), root, absolute); err != nil {
		t.Fatalf("GenerateWiki into an absolute directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(absolute, "Home.md")); err != nil {
		t.Errorf("Home.md missing in the absolute output: %v", err)
	}
}
