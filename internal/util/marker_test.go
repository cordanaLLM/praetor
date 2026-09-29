package util

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeMarkerFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMarkerExistsMatchesFixedPathsAndGlobs(t *testing.T) {
	root := t.TempDir()
	writeMarkerFile(t, filepath.Join(root, "mkosi.conf"))
	writeMarkerFile(t, filepath.Join(root, "build", "mkosi.conf"))
	writeMarkerFile(t, filepath.Join(root, "packer", "ubuntu.pkr.hcl"))
	for _, marker := range []string{"mkosi.conf", "build/mkosi.conf", "packer/*.pkr.hcl"} {
		if !MarkerExists(root, marker) {
			t.Errorf("marker %q not found", marker)
		}
	}
}

func TestMarkerExistsRejectsDirectoriesEmptyMatchesAndBadPatterns(t *testing.T) {
	root := t.TempDir()
	// An empty packer directory, a packer directory holding only prose, and a directory
	// whose own name looks like a template: none of these is an image forge.
	if err := os.MkdirAll(filepath.Join(root, "packer", "modules.pkr.hcl"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeMarkerFile(t, filepath.Join(root, "packer", "README.md"))
	for _, marker := range []string{"packer/*.pkr.hcl", "mkosi.conf", "build/mkosi.conf", "packer/[", "absent.conf"} {
		if MarkerExists(root, marker) {
			t.Errorf("marker %q matched where it must not", marker)
		}
	}
}

func TestMarkerExistsBoundedExpansion(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "packer")
	// One more template than the inspection bound, with the only regular file last: the
	// bound may stop the walk, so this pins that behaviour rather than leaving it implicit.
	for i := 0; i < maxMarkerMatches; i++ {
		if err := os.MkdirAll(filepath.Join(dir, fmt.Sprintf("%03d.pkr.hcl", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if MarkerExists(root, "packer/*.pkr.hcl") {
		t.Fatal("directories at the bound matched")
	}
	writeMarkerFile(t, filepath.Join(dir, "000-real.pkr.hcl"))
	if !MarkerExists(root, "packer/*.pkr.hcl") {
		t.Fatal("a regular file within the bound was not found")
	}
}

func TestAnyMarkerExistsMatchesOneOfSeveral(t *testing.T) {
	root := t.TempDir()
	writeMarkerFile(t, filepath.Join(root, "kconfig", "base.config"))
	if !AnyMarkerExists(root, []string{"mkosi.conf", "packer/*.pkr.hcl", "kconfig/[^.]*.config"}) {
		t.Fatal("the one marker present, last in the list, was not found")
	}
}

func TestAnyMarkerExistsRejectsAbsentAndEmptyLists(t *testing.T) {
	root := t.TempDir()
	writeMarkerFile(t, filepath.Join(root, "kconfig", ".config"))
	for name, markers := range map[string][]string{
		"none present": {"mkosi.conf", "kconfig/[^.]*.config"},
		"empty list":   {},
		"nil list":     nil,
	} {
		if AnyMarkerExists(root, markers) {
			t.Errorf("%s: matched", name)
		}
	}
}

func TestAnyMarkerExistsBoundedList(t *testing.T) {
	root := t.TempDir()
	writeMarkerFile(t, filepath.Join(root, "present"))
	markers := make([]string, 0, maxMarkerList+1)
	for i := 0; i < maxMarkerList; i++ {
		markers = append(markers, fmt.Sprintf("absent-%02d", i))
	}
	// The one present marker sits just past the bound, so the bound is what stops it.
	if AnyMarkerExists(root, append(markers, "present")) {
		t.Fatal("a marker past the list bound was inspected")
	}
	markers[maxMarkerList-1] = "present"
	if !AnyMarkerExists(root, markers) {
		t.Fatal("a marker at the last place within the bound was not found")
	}
}
