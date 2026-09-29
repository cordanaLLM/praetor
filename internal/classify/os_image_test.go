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

// kernelForge lays out a kernel forge: Kconfig fragments under kconfig/ and a version
// manifest, with the Go CLI and Python suite that drive and verify the build, and no Packer
// template or mkosi.conf (#615).
func kernelForge(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{"kconfig/base.config", "kconfig/hardening.config", "versions.json", "go.mod", "pyproject.toml"} {
		writeAt(t, root, rel)
	}
	return root
}

func TestByMarkersDetectsAKernelForge(t *testing.T) {
	root := kernelForge(t)
	got := ByMarkers(root)
	if got.Archetype != "os-image" || got.Source != SourceMarkers {
		t.Fatalf("a kernel forge with Kconfig fragments gave %+v; want os-image by markers", got)
	}
	if !HasMarkerOf(root, "os-image") {
		t.Fatal("HasMarkerOf misses the Kconfig fragments ByMarkers classifies by")
	}
}

// TestByMarkersDoesNotInventAKernelForge: neither an empty kconfig/, nor the .config file
// Kconfig writes, nor prose or a directory shaped like a fragment makes a kernel forge.
func TestByMarkersDoesNotInventAKernelForge(t *testing.T) {
	cases := map[string]func(t *testing.T, root string){
		"empty kconfig directory":  func(t *testing.T, root string) { mkdirAt(t, root, "kconfig") },
		"lone root .config":        func(t *testing.T, root string) { writeAt(t, root, ".config") },
		"lone kconfig/.config":     func(t *testing.T, root string) { writeAt(t, root, "kconfig/.config") },
		"kconfig prose only":       func(t *testing.T, root string) { writeAt(t, root, "kconfig/README.md") },
		"fragment-named directory": func(t *testing.T, root string) { mkdirAt(t, root, "kconfig/base.config") },
		"fragment outside kconfig": func(t *testing.T, root string) { writeAt(t, root, "configs/base.config") },
	}
	for name, lay := range cases {
		root := t.TempDir()
		lay(t, root)
		if got := ByMarkers(root); got.Archetype == "os-image" {
			t.Errorf("%s: classified as an image forge: %+v", name, got)
		}
		if HasMarkerOf(root, "os-image") {
			t.Errorf("%s: HasMarkerOf reported an os-image marker", name)
		}
	}
}

// TestByMarkersBoundaryPackerAndKernelFragmentsResolveOnce: a forge holding both a Packer
// template and Kconfig fragments is one os-image repository, the same answer every time.
func TestByMarkersBoundaryPackerAndKernelFragmentsResolveOnce(t *testing.T) {
	root := kernelForge(t)
	writeAt(t, root, "packer/ubuntu.pkr.hcl")
	for i := 0; i < 3; i++ {
		if got := ByMarkers(root); got.Archetype != "os-image" || got.Source != SourceMarkers {
			t.Fatalf("run %d: %+v; want os-image by markers", i, got)
		}
	}
}

func TestHasMarkerOfReadsOnlyTheNamedArchetype(t *testing.T) {
	for _, marker := range []string{"packer/ubuntu.pkr.hcl", "mkosi.conf", "build/mkosi.conf", "kconfig/base.config"} {
		root := t.TempDir()
		writeAt(t, root, marker)
		if !HasMarkerOf(root, "os-image") {
			t.Errorf("marker %q: no os-image marker found", marker)
		}
		if HasMarkerOf(root, "framework") {
			t.Errorf("marker %q: counted for framework", marker)
		}
	}
}

// TestHasMarkerOfBoundary: an empty path probes nothing, an unknown archetype has no markers,
// and a more specific rule ahead of os-image does not hide its markers from the flavor, which
// is asked only once the profile is already decided.
func TestHasMarkerOfBoundary(t *testing.T) {
	root := t.TempDir()
	writeAt(t, root, "harness.json")
	writeAt(t, root, "mkosi.conf")
	if got := ByMarkers(root); got.Archetype != "framework" {
		t.Fatalf("harness.json must still classify first, got %+v", got)
	}
	if !HasMarkerOf(root, "os-image") {
		t.Error("the mkosi.conf behind a more specific rule was not found")
	}
	if HasMarkerOf(root, "no-such-archetype") || HasMarkerOf(root, "") {
		t.Error("an archetype with no rule matched")
	}
	if HasMarkerOf("", "os-image") || HasMarkerOf("  ", "os-image") {
		t.Error("an empty repository path matched")
	}
}

func mkdirAt(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
}
