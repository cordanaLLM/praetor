package changelog

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is this repository's root as seen from the package directory. The tests below
// read the tracked changelog.d, so a fragment the strict loader rejects fails `go test`
// in the change that adds it, instead of failing the release render that trips over it
// (#194: three tracked fragments carried a `package:` key and blocked every render).
var repoRoot = filepath.Join("..", "..")

func TestTrackedFragments_Positive_DecodeUnderStrictSchema(t *testing.T) {
	frags, files, err := LoadFragments(repoRoot)
	if err != nil {
		t.Fatalf("a tracked changelog fragment does not decode; a release render would fail: %v", err)
	}
	if len(frags) != len(files) {
		t.Errorf("decoded %d fragments from %d files", len(frags), len(files))
	}
}

// maxTrackedFragments bounds the loops over the tracked fragment set (HISS-02).
const maxTrackedFragments = 4096

func TestTrackedFragments_Positive_RenderIntoCopy(t *testing.T) {
	root := t.TempDir()
	copyTrackedChangelog(t, root)

	if err := RenderReleaseContext(context.Background(), root, "0.0.0-test", "2026-09-18"); err != nil {
		t.Fatalf("rendering the tracked fragments failed; `praetorctl release` would too: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "## [0.0.0-test] - 2026-09-18") {
		t.Errorf("rendered changelog lacks the release heading:\n%.400s", data)
	}
}

func TestTrackedFragments_Negative_UnknownKeyIsRejected(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "changelog.d")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	stray := "type: added\ntitle: carries a key the schema lacks\npackage: config\n"
	if err := os.WriteFile(filepath.Join(dir, "20260918-stray.yaml"), []byte(stray), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := LoadFragments(root)
	if err == nil {
		t.Fatal("a fragment with an unknown key must be rejected, as it was on main before #194")
	}
	if !strings.Contains(err.Error(), "package") {
		t.Errorf("error should name the unknown key, got: %v", err)
	}
}

func TestTrackedFragments_Boundary_OptionalKeysOnly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "changelog.d")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	full := "type: fixed\ntitle: every key the schema knows\nissue: \"194\"\nbreaking: true\n"
	if err := os.WriteFile(filepath.Join(dir, "20260918-full.yaml"), []byte(full), 0o600); err != nil {
		t.Fatal(err)
	}

	frags, _, err := LoadFragments(root)
	if err != nil {
		t.Fatalf("a fragment using every known key must load: %v", err)
	}
	if len(frags) != 1 || frags[0].Issue != "194" || !frags[0].Breaking {
		t.Errorf("unexpected decode: %+v", frags)
	}
}

// copyTrackedChangelog mirrors CHANGELOG.md and changelog.d into root, so the render test
// never writes to the checkout.
func copyTrackedChangelog(t *testing.T, root string) {
	t.Helper()
	copyFile(t, filepath.Join(repoRoot, "CHANGELOG.md"), filepath.Join(root, "CHANGELOG.md"))
	entries, err := os.ReadDir(filepath.Join(repoRoot, "changelog.d"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "changelog.d"), 0o750); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(entries) && i < maxTrackedFragments; i++ {
		if entries[i].Type()&fs.ModeType != 0 {
			continue
		}
		name := entries[i].Name()
		copyFile(t, filepath.Join(repoRoot, "changelog.d", name), filepath.Join(root, "changelog.d", name))
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
