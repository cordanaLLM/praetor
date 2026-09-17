package flavor_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

func writeForgeFile(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDetectOSImageForge(t *testing.T) {
	for _, marker := range []string{"packer/ubuntu.pkr.hcl", "mkosi.conf", "build/mkosi.conf"} {
		root := t.TempDir()
		writeForgeFile(t, root, marker)
		name, ok := flavor.Detect(root)
		if !ok || name != "os-image" {
			t.Errorf("marker %q gave %q (matched=%v)", marker, name, ok)
		}
	}
}

func TestOSImageOutranksTheToolingItBuildsWith(t *testing.T) {
	root := t.TempDir()
	writeForgeFile(t, root, "packer/ubuntu.pkr.hcl")
	writeForgeFile(t, root, "go.mod")
	writeForgeFile(t, root, "pyproject.toml")
	if err := os.MkdirAll(filepath.Join(root, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if name, _ := flavor.Detect(root); name != "os-image" {
		t.Fatalf("expected os-image, got %q", name)
	}
}

func TestOSImageIsRegisteredAndImplementsItsArchetype(t *testing.T) {
	f, err := flavor.Get("os-image")
	if err != nil {
		t.Fatalf("os-image is not registered: %v", err)
	}
	if f.HISSProfile() != "os-image" {
		t.Fatalf("flavor implements %q, not the os-image archetype", f.HISSProfile())
	}
	if len(f.RequiredTemplates()) == 0 || len(f.RequiredToolchains()) == 0 {
		t.Fatal("flavor declares no templates or toolchains, so an audit of it is vacuous")
	}
}

func TestDetectDoesNotInventAnImageForge(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "packer"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForgeFile(t, root, "packer/notes.txt")
	if name, ok := flavor.Detect(root); ok && name == "os-image" {
		t.Fatal("a packer directory without a template matched")
	}
}
