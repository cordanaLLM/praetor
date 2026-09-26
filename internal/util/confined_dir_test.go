package util

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestInConfinedDirectory_Positive pins the lent handle: the callback writes through a
// handle on rel, an in-root link resolves to its target, and the callback's own error is
// returned unchanged rather than relabelled as an escape.
func TestInConfinedDirectory_Positive(t *testing.T) {
	root, _ := confinedFixture(t)
	err := InConfinedDirectory(root, "alias", func(dir *os.Root) error {
		return dir.WriteFile("ledger", []byte("kept"), 0o600)
	})
	if err != nil {
		t.Fatalf("InConfinedDirectory through an in-root link: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "inner", "ledger")) // #nosec G304 -- test-local path from t.TempDir
	if err != nil || string(data) != "kept" {
		t.Fatalf("write through the handle = (%q, %v), want it at the link target", data, err)
	}
	sentinel := errors.New("callback failed")
	err = InConfinedDirectory(root, "inner", func(*os.Root) error { return sentinel })
	if !errors.Is(err, sentinel) || errors.Is(err, ErrPathEscapesRoot) {
		t.Errorf("callback error = %v, want it passed through unlabelled", err)
	}
}

// TestInConfinedDirectory_Negative pins every refusal: an escaping link, a parent
// traversal and an absolute rel never reach the callback and write nothing outside; a
// missing directory and a nil callback are errors.
func TestInConfinedDirectory_Negative(t *testing.T) {
	root, outside := confinedFixture(t)
	cases := []struct {
		rel  string
		want error
	}{
		{"out", ErrPathEscapesRoot},
		{filepath.Join("..", "sibling"), ErrPathEscapesRoot},
		{outside, ErrAbsoluteRelPath},
	}
	for _, tc := range cases {
		called := false
		err := InConfinedDirectory(root, tc.rel, func(*os.Root) error { called = true; return nil })
		if !errors.Is(err, tc.want) || called {
			t.Errorf("InConfinedDirectory(%q) = (%v, called %v), want %v and no callback", tc.rel, err, called, tc.want)
		}
	}
	assertEmptyDir(t, outside)
	if err := InConfinedDirectory(root, "missing", func(*os.Root) error { return nil }); err == nil || errors.Is(err, ErrPathEscapesRoot) {
		t.Errorf("missing directory = %v, want a plain open failure", err)
	}
	if err := InConfinedDirectory(root, "inner", nil); err == nil {
		t.Error("expected a nil callback to be refused")
	}
}

// TestInConfinedDirectory_Boundary_RootAndSwapAfterCheck pins the two edges: a rel naming
// the root lends the root's own handle, and a directory swapped for an escaping link after
// ConfinePath's check is refused by the pinned handle instead of followed (BUG-826).
func TestInConfinedDirectory_Boundary_RootAndSwapAfterCheck(t *testing.T) {
	root, outside := confinedFixture(t)
	err := InConfinedDirectory(root, ".", func(dir *os.Root) error {
		_, statErr := dir.Stat("inner")
		return statErr
	})
	if err != nil {
		t.Fatalf(`InConfinedDirectory(root, "."): %v`, err)
	}

	absRoot, inside, err := confineBelow(root, "inner")
	if err != nil {
		t.Fatalf("confineBelow: %v", err)
	}
	swapForLink(t, filepath.Join(root, "inner"), outside)
	called := false
	err = inConfinedDirectory(absRoot, inside, func(*os.Root) error { called = true; return nil })
	if !errors.Is(err, ErrPathEscapesRoot) || called {
		t.Errorf("directory swapped for an escaping link = (%v, called %v), want ErrPathEscapesRoot and no callback", err, called)
	}
	assertEmptyDir(t, outside)
}
