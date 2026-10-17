// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"context"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxRustLibraryMembers bounds the crates rustLibraryOnly judges (HISS-02).
const maxRustLibraryMembers = 256

// rustLibraryDetectTimeout bounds reading the workspace members' manifests (HISS-02).
const rustLibraryDetectTimeout = 5 * time.Second

// maxRustLibraryTemplates bounds the walk over the rust-systems templates the library flavor
// reuses (HISS-02); rust-systems ships three.
const maxRustLibraryTemplates = 8

// rustLibraryOnly reports whether the Cargo workspace at repoPath is a library: at least one
// crate has a library target, and no crate has a binary target it publishes. A binary in a
// crate with publish = false, set on the crate or inherited from [workspace.package], is a
// private helper (an xtask or a comparison tool) and does not make the workspace an
// application. An unreadable manifest or member list is not a library.
func rustLibraryOnly(repoPath string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), rustLibraryDetectTimeout)
	defer cancel()
	root, problem := readRustLibraryManifest(repoPath, "Cargo.toml")
	if problem != "" || root.invalid != "" {
		return false
	}
	crates, problem := workspaceCrates(ctx, repoPath, root)
	if problem != "" || len(crates) == 0 || len(crates) > maxRustLibraryMembers {
		return false
	}
	hasLibrary := false
	for i := 0; i < len(crates) && i < maxRustLibraryMembers; i++ {
		library, published := rustCrateTargets(repoPath, root, crates[i])
		if published {
			return false
		}
		hasLibrary = hasLibrary || library
	}
	return hasLibrary
}

// readRustLibraryManifest reads and parses the Cargo.toml at rel under root, or says why it
// cannot.
func readRustLibraryManifest(root, rel string) (cargoManifest, string) {
	data, err := util.ReadConfinedLimited(root, filepath.FromSlash(rel), maxSettingBytes)
	if err != nil {
		return cargoManifest{}, "cannot read Cargo.toml"
	}
	manifest, err := parseCargoManifest(filepath.Join(root, filepath.FromSlash(rel)), data)
	if err != nil {
		return cargoManifest{}, err.Error()
	}
	return manifest, ""
}

// rustCrateTargets reports whether crate of the workspace whose root manifest is workspace has a
// library target, and whether it has a binary target it publishes. Targets count when the
// manifest declares them ([lib], [[bin]]) or Cargo infers them (src/lib.rs, src/main.rs, src/bin/).
func rustCrateTargets(root string, workspace cargoManifest, crate cargoCrate) (library, publishedBinary bool) {
	crateDir := root
	if !crate.root {
		crateDir = filepath.Join(root, filepath.Dir(crate.name))
	}
	library = crate.manifest.hasLibraryTarget || CheckFileExists(filepath.Join(crateDir, "src", "lib.rs"))
	binary := crate.manifest.hasBinaryTarget || CheckFileExists(filepath.Join(crateDir, "src", "main.rs")) ||
		CheckFileExists(filepath.Join(crateDir, "src", "bin"))
	return library, binary && !crate.manifest.unpublishedIn(workspace)
}

// rustLibraryTemplates are the rust-systems templates with the CI workflow replaced by the
// library one, which adds the published API check.
func rustLibraryTemplates() []TemplateItem {
	items := (&RustSystemsFlavor{}).RequiredTemplates()
	for i := 0; i < len(items) && i < maxRustLibraryTemplates; i++ {
		if items[i].Path == ".github/workflows/ci.yml" {
			items[i] = rustLibraryCITemplate()
		}
	}
	return items
}

// rustLibrarySettings are the rust-systems settings; a library needs no setting a crate
// workspace does not.
func rustLibrarySettings() []SettingItem {
	return (&RustSystemsFlavor{}).RequiredSettings()
}

// rustLibraryToolchains are the rust-systems toolchains plus cargo-semver-checks, the version the
// library CI template pins.
func rustLibraryToolchains() []ToolchainItem {
	items := (&RustSystemsFlavor{}).RequiredToolchains()
	return append(items, ToolchainItem{
		Binary: "cargo-semver-checks", Purpose: "Published Rust API compatibility analysis",
		InstallGuide: "cargo install cargo-semver-checks --version 0.51.0 --locked",
	})
}

// rustLibraryCITemplate is the library CI workflow. It shares the rust-systems prior digests, so
// an unedited rust-systems workflow an earlier adoption wrote is replaced rather than kept.
func rustLibraryCITemplate() TemplateItem {
	return TemplateItem{
		Path: ".github/workflows/ci.yml", Description: "Rust library CI with API compatibility checks",
		Source: "rust/ci-rust-library.yml.tmpl", Validator: validWorkflow, Prior: priorRustCIDigests,
	}
}
