// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package changelog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// TestRenderKeepsFragmentDirectory_Positive: a release render removes the rendered fragments
// and leaves the empty placeholder, so the directory survives in version control and still
// counts as a fragment directory. The placeholder is no fragment: the next render has nothing
// to publish.
func TestRenderKeepsFragmentDirectory_Positive(t *testing.T) {
	root := t.TempDir()
	fragment, err := CreateFragment(root, Fragment{Type: TypeFixed, Title: "Fix a defect"})
	if err != nil {
		t.Fatal(err)
	}
	if err := RenderReleaseContext(t.Context(), root, "1.2.3", "2026-09-12"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fragment); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rendered fragment not removed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, FragmentDir, FragmentPlaceholder))
	if err != nil || len(data) != 0 {
		t.Fatalf("render left no empty %s: %q, %v", FragmentPlaceholder, data, err)
	}
	assertFragmentDirPresent(t, root, true, "fragment directory after a release render")
	if err := RenderReleaseContext(t.Context(), root, "1.2.4", "2026-09-13"); !errors.Is(err, ErrNoFragments) {
		t.Fatalf("a render of only the placeholder: error = %v, want %v", err, ErrNoFragments)
	}
}

// TestRenderKeepsFragmentDirectory_Boundary: a placeholder the repository already keeps is left
// byte for byte as it was.
func TestRenderKeepsFragmentDirectory_Boundary(t *testing.T) {
	root := t.TempDir()
	if _, err := CreateFragment(root, Fragment{Type: TypeAdded, Title: "Add a feature"}); err != nil {
		t.Fatal(err)
	}
	placeholder := filepath.Join(root, FragmentDir, FragmentPlaceholder)
	const kept = "keeps the fragment directory\n"
	if err := os.WriteFile(placeholder, []byte(kept), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RenderReleaseContext(t.Context(), root, "1.2.3", "2026-09-12"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(placeholder); err != nil || string(data) != kept {
		t.Fatalf("existing placeholder changed: %q, %v", data, err)
	}
}

// TestRenderKeepsFragmentDirectory_Negative: a placeholder that cannot be written fails the render
// with the journal retained, and the matching rerun completes the cleanup. Writing it needs a
// live context and a pinned directory.
func TestRenderKeepsFragmentDirectory_Negative(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	dirRoot, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dirRoot.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := keepFragmentDir(canceled, dirRoot); err == nil {
		t.Fatal("a canceled context must not write the placeholder")
	}
	if err := keepFragmentDir(t.Context(), nil); err == nil {
		t.Fatal("an unpinned directory must be an error")
	}
	root, fragment, _ := interruptedRender(t)
	if err := os.Remove(fragment); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, FragmentDir)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	err = RenderReleaseContext(t.Context(), root, "1.2.3", "2026-09-12")
	if chmodErr := os.Chmod(dir, 0o700); chmodErr != nil {
		t.Fatal(chmodErr)
	}
	if err == nil || !strings.Contains(err.Error(), FragmentPlaceholder) {
		t.Fatalf("blocked placeholder: error = %v, want a failure naming %s", err, FragmentPlaceholder)
	}
	if _, exists, err := contextopt.ObserveSnapshot(t.Context(), filepath.Join(root, renderJournalName)); err != nil || !exists {
		t.Fatalf("journal not retained after a failed placeholder: exists=%v err=%v", exists, err)
	}
	if err := RenderReleaseContext(t.Context(), root, "1.2.3", "2026-09-12"); err != nil {
		t.Fatalf("rerun after the placeholder failure: %v", err)
	}
	assertReleaseCount(t, root, 1)
	assertFragmentDirPresent(t, root, true, "fragment directory after the resumed render")
}
