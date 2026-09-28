// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package figures exposes the locked figure engine files adoption writes into a repository:
// the render core and its checks, the vendored interfig render source with its license, the
// committed player, the stylesheet, the two site generators and the authoring guide
// (docs/adr/0016-figures-for-adopters.md, section 2). The managed asset family registry
// (internal/managedasset) declares them as the figure engine family; adoption, audit and the
// devcontainer bootstrap read them through it.
//
// The inventory is an explicit list, never a directory pattern: the npm manifest and lock,
// the player sources, the bundler, the smoke test, the tests and their fixtures, and the rest
// of the vendored upstream tree stay in this repository, and assets_test.go holds the list to
// git ls-files so a new file is either listed here or named as repository-only there.
package figures

import (
	"embed"
	"io/fs"
	"slices"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// Directory is the repository-relative home of the figure engine.
	Directory = "tools/figures"
	// SourceFile is the Go file carrying the go:embed directive over the assets.
	SourceFile = Directory + "/assets.go"
	// MaxAssets bounds all asset iteration: the inventory's exact size.
	MaxAssets = 18
)

// assetNames is the inventory in emission order: render and check, the vendored render source,
// the committed player and the stylesheet, the site generators, then the authoring guide.
var assetNames = [...]string{
	"core.mjs",
	"checks.mjs",
	"build.mjs",
	"types.ts",
	"third_party/interfig/vendor.json",
	"third_party/interfig/VENDOR.md",
	"third_party/interfig/upstream/LICENSE",
	"third_party/interfig/upstream/src/svg.ts",
	"third_party/interfig/upstream/src/geometry.ts",
	"third_party/interfig/upstream/src/model.ts",
	"dist/loader.js",
	"dist/player.js",
	"dist/THIRD-PARTY-LICENSES.txt",
	"figures.css",
	"mkdocs_hook.py",
	"astro.mjs",
	"serve.mjs",
	"README.md",
}

//go:embed core.mjs checks.mjs build.mjs types.ts third_party/interfig/vendor.json third_party/interfig/VENDOR.md third_party/interfig/upstream/LICENSE third_party/interfig/upstream/src/svg.ts third_party/interfig/upstream/src/geometry.ts third_party/interfig/upstream/src/model.ts dist/loader.js dist/player.js dist/THIRD-PARTY-LICENSES.txt figures.css mkdocs_hook.py astro.mjs serve.mjs README.md
var assets embed.FS

// FS returns the embedded asset tree. It is read-only; Read is the bounded accessor.
func FS() fs.FS {
	return assets
}

// Names returns the complete deterministic asset inventory.
func Names() []string {
	return slices.Clone(assetNames[:min(len(assetNames), MaxAssets)])
}

// Read returns one canonical asset without exposing mutable embedded storage.
func Read(name string) ([]byte, error) {
	return util.ReadEmbeddedAsset(assets, "figures", Names(), name)
}
