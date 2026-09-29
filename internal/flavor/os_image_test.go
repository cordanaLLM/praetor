package flavor_test

import (
	"errors"
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
		name, ok := resolve(root)
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
	if name, _ := resolve(root); name != "os-image" {
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
	if name, ok := resolve(root); ok && name == "os-image" {
		t.Fatal("a packer directory without a template matched")
	}
}

// kernelForgeFiles is a kernel forge declaring os-image: Kconfig fragments and a version
// manifest beside the Go CLI that drives the build, and no Packer template or mkosi.conf.
func kernelForgeFiles() map[string]string {
	return map[string]string{
		"kconfig/base.config":      "CONFIG_MODULES=y\n",
		"kconfig/hardening.config": "CONFIG_HARDENED_USERCOPY=y\n",
		"versions.json":            "{\"stable\": \"6.12\"}\n",
		"go.mod":                   "module fixture\n",
		"cmd/forge/main.go":        "package main\n",
	}
}

// TestKernelForgeResolvesToOSImageEverywhere is #615's measured case: a kernel forge declaring
// os-image matched no flavor, so the flavor audit and the gate's Flavor Conformance stage failed
// while adoption reported it not applicable. Resolve (flavor audit and the gate),
// ResolveForProfile (adoption) and the audit itself now name os-image for the same checkout.
func TestKernelForgeResolvesToOSImageEverywhere(t *testing.T) {
	repo := declaringRepo(t, "os-image", kernelForgeFiles())
	if got, err := flavor.Resolve(repo); err != nil || got != "os-image" {
		t.Fatalf("Resolve = %q, %v; want os-image", got, err)
	}
	if got, err := flavor.ResolveForProfile(repo, "os-image"); err != nil || got != "os-image" {
		t.Fatalf("ResolveForProfile = %q, %v; want os-image, the flavor adoption scaffolds", got, err)
	}
	report, err := flavor.AuditFlavor(repo, "auto")
	if err != nil || report == nil || report.Flavor != "os-image" {
		t.Fatalf("AuditFlavor = %+v, %v; want an os-image verdict", report, err)
	}
	// Without the declaration the fragments still classify it, ahead of its go.mod.
	if got, ok := resolve(repoWithFiles(t, kernelForgeFiles())); !ok || got != "os-image" {
		t.Fatalf("an undeclared kernel forge resolved to %q (matched=%v)", got, ok)
	}
}

// TestOSImageWithoutAnyMarkerStillFailsClosed: widening the markers turns no unmarked
// repository into a match. A declared os-image without a forge marker, and an undeclared tree
// with nothing to classify, are still nothing-matched, never a silent pass.
func TestOSImageWithoutAnyMarkerStillFailsClosed(t *testing.T) {
	declared := declaringRepo(t, "os-image", map[string]string{"versions.json": "{}\n", "go.mod": "module fixture\n"})
	undeclared := repoWithFiles(t, map[string]string{"versions.json": "{}\n"})
	for name, repo := range map[string]string{"declared": declared, "undeclared": undeclared} {
		if got, err := flavor.Resolve(repo); !errors.Is(err, flavor.ErrNoFlavorMatched) {
			t.Errorf("%s: Resolve = %q, %v; want ErrNoFlavorMatched", name, got, err)
		}
		if report, err := flavor.AuditFlavor(repo, "auto"); !errors.Is(err, flavor.ErrNoFlavorMatched) || report != nil {
			t.Errorf("%s: AuditFlavor = %+v, %v; want a nothing-matched refusal", name, report, err)
		}
	}
}

// TestOSImageKernelMarkerBoundary: an empty kconfig/ or the .config file Kconfig writes is no
// forge, a forge carrying both a Packer template and Kconfig fragments is one os-image
// repository, and an empty path probes nothing.
func TestOSImageKernelMarkerBoundary(t *testing.T) {
	notForge := declaringRepo(t, "os-image", map[string]string{".config": "CONFIG_X=y\n", "kconfig/.config": "CONFIG_X=y\n"})
	if err := os.MkdirAll(filepath.Join(notForge, "kconfig", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := flavor.Resolve(notForge); !errors.Is(err, flavor.ErrNoFlavorMatched) {
		t.Errorf("Resolve = %q, %v; a .config and an empty kconfig/ are no forge", got, err)
	}
	files := kernelForgeFiles()
	files["packer/ubuntu.pkr.hcl"] = "source \"null\" \"x\" {}\n"
	both := declaringRepo(t, "os-image", files)
	if got, err := flavor.Resolve(both); err != nil || got != "os-image" {
		t.Errorf("Packer and Kconfig together resolved to %q, %v; want os-image", got, err)
	}
	if (&flavor.OSImageFlavor{}).Detect("") {
		t.Error("an empty repository path was detected as a forge")
	}
}
