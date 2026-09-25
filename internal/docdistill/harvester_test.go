// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// writeHarvestFixture writes body to root/rel, creating parent directories.
func writeHarvestFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// upperCaseModule is a module path whose cache directory needs escaping. It is
// not a real module, so `go doc` cannot answer for it and the harvest reaches
// the module-cache probe.
var upperCaseModule = PackageRef{Name: "example.com/BurntSushi/Fixture", Version: "v1.4.0", Kind: KindGoModule}

// Positive: a module with upper-case letters is found under its "!"-escaped
// directory in GOMODCACHE. The probe used the raw path, which never exists on
// disk, and never read GOMODCACHE.
func TestHarvestFindsUpperCaseModuleUnderEscapedGOMODCACHEPath(t *testing.T) {
	modCache := t.TempDir()
	t.Setenv("GOMODCACHE", modCache)
	t.Setenv("GOPATH", t.TempDir())
	writeHarvestFixture(t, modCache, "example.com/!burnt!sushi/!fixture@v1.4.0/README.md", "# Fixture\nEscaped module-cache documentation.")

	got, err := HarvestDocumentation(t.Context(), t.TempDir(), upperCaseModule, true)
	if err != nil || !strings.Contains(got, "Escaped module-cache documentation.") {
		t.Fatalf("escaped cache entry not found: %q, %v", got, err)
	}
}

// Boundary: with GOMODCACHE unset the probe falls back to GOPATH/pkg/mod.
func TestHarvestFallsBackToGOPATHWhenGOMODCACHEIsUnset(t *testing.T) {
	goPath := t.TempDir()
	t.Setenv("GOMODCACHE", "")
	t.Setenv("GOPATH", goPath)
	writeHarvestFixture(t, goPath, "pkg/mod/example.com/!burnt!sushi/!fixture@v1.4.0/README.md", "# Fixture\nGOPATH module-cache documentation.")

	got, err := HarvestDocumentation(t.Context(), t.TempDir(), upperCaseModule, true)
	if err != nil || !strings.Contains(got, "GOPATH module-cache documentation.") {
		t.Fatalf("GOPATH fallback not used: %q, %v", got, err)
	}
}

// Positive: `go doc` runs inside the scanned repository, so a package that only
// the repository's module graph can resolve is documented. It used to run in
// the process's working directory, where the package does not exist.
func TestHarvestRunsGoDocInTheScannedRepository(t *testing.T) {
	repo := t.TempDir()
	writeHarvestFixture(t, repo, "go.mod", "module example.com/harvested\n\ngo 1.21\n")
	writeHarvestFixture(t, repo, "widget/widget.go", "// Package widget is documented only inside the scanned repository, never elsewhere.\npackage widget\n\n// Size is a documented constant.\nconst Size = 1\n")
	t.Chdir(t.TempDir())

	got, err := HarvestDocumentation(t.Context(), repo, PackageRef{Name: "example.com/harvested/widget", Kind: KindGoModule}, true)
	if err != nil || !strings.Contains(got, "documented only inside the scanned repository") {
		t.Fatalf("go doc did not run in the repository: %q, %v", got, err)
	}
}

// Boundary: online harvests pin the declared version; offline ones leave the
// version to the repository's module graph, since resolving pkg@version needs
// the module proxy.
func TestGoDocTargetPinsTheVersionOnlyOnline(t *testing.T) {
	ref := PackageRef{Name: "example.com/mod", Version: "v1.2.3", Kind: KindGoModule}
	for _, tc := range []struct {
		ref     PackageRef
		offline bool
		want    string
	}{
		{ref, false, "example.com/mod@v1.2.3"},
		{ref, true, "example.com/mod"},
		{PackageRef{Name: "example.com/mod", Kind: KindGoModule}, false, "example.com/mod"},
	} {
		if got := goDocTarget(tc.ref, tc.offline); got != tc.want {
			t.Errorf("goDocTarget(%+v, %v) = %q, want %q", tc.ref, tc.offline, got, tc.want)
		}
	}
}

// Negative: a node_modules in the process's working directory is not the
// scanned repository's. The harvest used to read it and attribute its README
// to the repository.
func TestHarvestIgnoresNodeModulesOutsideTheRepository(t *testing.T) {
	cwd := t.TempDir()
	writeHarvestFixture(t, cwd, "node_modules/left-pad/README.md", "# left-pad from the wrong directory")
	t.Chdir(cwd)

	ref := PackageRef{Name: "left-pad", Version: "1.3.0", Kind: KindNodePackage, Manifest: "package.json"}
	if got, err := HarvestDocumentation(t.Context(), t.TempDir(), ref, true); err == nil || got != "" {
		t.Fatalf("read node_modules outside the repository: %q, %v", got, err)
	}
}

// Positive: the repository's own node_modules is read, beside the declaring
// workspace member first and at the root second.
func TestHarvestReadsTheRepositoryNodeModules(t *testing.T) {
	repo := t.TempDir()
	writeHarvestFixture(t, repo, "node_modules/left-pad/README.md", "# left-pad hoisted to the root")
	writeHarvestFixture(t, repo, "packages/alpha/node_modules/zod/README.md", "# zod linked beside the member")
	t.Chdir(t.TempDir())

	for _, tc := range []struct {
		ref  PackageRef
		want string
	}{
		{PackageRef{Name: "left-pad", Kind: KindNodePackage, Manifest: "package.json"}, "hoisted to the root"},
		{PackageRef{Name: "left-pad", Kind: KindNodePackage, Manifest: "packages/alpha/package.json"}, "hoisted to the root"},
		{PackageRef{Name: "zod", Kind: KindNodePackage, Manifest: "packages/alpha/package.json"}, "linked beside the member"},
	} {
		got, err := HarvestDocumentation(t.Context(), repo, tc.ref, true)
		if err != nil || !strings.Contains(got, tc.want) {
			t.Errorf("%s from %s: %q, %v", tc.ref.Name, tc.ref.Manifest, got, err)
		}
	}
}

// linkNodePackage links root/linkRel to target, a path relative to the link's
// directory the way pnpm writes it, and skips where the host cannot create
// symlinks (HISS-21).
func linkNodePackage(t *testing.T, root, linkRel, target string) {
	t.Helper()
	link := filepath.Join(root, filepath.FromSlash(linkRel))
	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.FromSlash(target), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// Positive: pnpm's isolated linker makes node_modules/<pkg> a symlink into
// node_modules/.pnpm, both beside a workspace member and at the root. The
// README is read through the link; the strict snapshot refused a symlinked
// package directory, so no pnpm-installed README was ever harvested.
func TestHarvestFollowsPnpmPackageLinksInsideTheRepository(t *testing.T) {
	repo := t.TempDir()
	writeHarvestFixture(t, repo, "node_modules/.pnpm/zod@3.23.8/node_modules/zod/README.md", "# zod from the pnpm store")
	writeHarvestFixture(t, repo, "node_modules/.pnpm/left-pad@1.3.0/node_modules/left-pad/README.md", "# left-pad from the pnpm store")
	linkNodePackage(t, repo, "packages/alpha/node_modules/zod", "../../../node_modules/.pnpm/zod@3.23.8/node_modules/zod")
	linkNodePackage(t, repo, "node_modules/left-pad", ".pnpm/left-pad@1.3.0/node_modules/left-pad")
	t.Chdir(t.TempDir())

	for _, tc := range []struct {
		ref  PackageRef
		want string
	}{
		{PackageRef{Name: "zod", Kind: KindNodePackage, Manifest: "packages/alpha/package.json"}, "zod from the pnpm store"},
		{PackageRef{Name: "left-pad", Kind: KindNodePackage, Manifest: "package.json"}, "left-pad from the pnpm store"},
	} {
		got, err := HarvestDocumentation(t.Context(), repo, tc.ref, true)
		if err != nil || !strings.Contains(got, tc.want) {
			t.Errorf("%s from %s: %q, %v", tc.ref.Name, tc.ref.Manifest, got, err)
		}
	}
}

// Negative: following a package link stays confined. A node_modules entry
// linking outside the repository is refused, not read.
func TestHarvestRefusesPackageLinksLeavingTheRepository(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	writeHarvestFixture(t, outside, "private/README.md", "# private README outside the repository")
	linkNodePackage(t, repo, "node_modules/private", filepath.Join(outside, "private"))

	ref := PackageRef{Name: "private", Kind: KindNodePackage, Manifest: "package.json"}
	got, err := HarvestDocumentation(t.Context(), repo, ref, true)
	if !errors.Is(err, util.ErrPathEscapesRoot) || got != "" {
		t.Fatalf("read a README outside the repository through a package link: %q, %v", got, err)
	}
}

// Boundary: a dangling member link does not end the lookup; the root copy is
// still read.
func TestHarvestFallsBackToTheRootPastADanglingMemberLink(t *testing.T) {
	repo := t.TempDir()
	writeHarvestFixture(t, repo, "node_modules/zod/README.md", "# zod hoisted to the root")
	linkNodePackage(t, repo, "packages/alpha/node_modules/zod", "../../../node_modules/.pnpm/zod@0.0.0/node_modules/zod")

	ref := PackageRef{Name: "zod", Kind: KindNodePackage, Manifest: "packages/alpha/package.json"}
	got, err := HarvestDocumentation(t.Context(), repo, ref, true)
	if err != nil || !strings.Contains(got, "hoisted to the root") {
		t.Fatalf("dangling member link: %q, %v", got, err)
	}
}
