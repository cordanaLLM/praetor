package classify

import (
	"os"
	"path/filepath"
	"testing"
)

func writeAt(t *testing.T, root string, rel string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestByMarkersDetectsAnImageForge(t *testing.T) {
	for _, marker := range []string{"packer/ubuntu.pkr.hcl", "mkosi.conf", "build/mkosi.conf"} {
		root := t.TempDir()
		writeAt(t, root, marker)
		got := ByMarkers(root)
		if got.Archetype != "os-image" || got.Source != SourceMarkers {
			t.Errorf("marker %q gave %+v", marker, got)
		}
	}
}

func TestByMarkersPrefersTheProductOverItsTooling(t *testing.T) {
	// An image forge carries the CLI that drives the build and the suite that verifies the
	// result. Classifying it by either describes the tooling, which is what this pins.
	root := t.TempDir()
	writeAt(t, root, "packer/ubuntu.pkr.hcl")
	writeAt(t, root, "go.mod")
	writeAt(t, root, "pyproject.toml")
	writeAt(t, root, "Dockerfile")
	if got := ByMarkers(root); got.Archetype != "os-image" {
		t.Fatalf("tooling outranked the product: %+v", got)
	}
}

func TestByMarkersDoesNotInventAnImageForge(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "packer"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAt(t, root, "packer/README.md")
	writeAt(t, root, "go.mod")
	if got := ByMarkers(root); got.Archetype != "framework" {
		t.Fatalf("a packer directory without a template is not a forge: %+v", got)
	}
}
