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
	"maps"
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
	// SpecDirectory and OutputDirectory hold a repository's figure specs and their committed
	// outputs: SPEC_DIR and OUT_DIR in checks.mjs, which assets_test.go holds to these values.
	SpecDirectory   = "docs/figures"
	OutputDirectory = "docs/assets/figures"
	// VendoredTree is the Directory-relative glob of the vendored interfig files, which keep
	// upstream's bytes and its MIT terms (VendoredLicense).
	VendoredTree    = "third_party/interfig/upstream/**"
	VendoredLicense = "MIT"
)

// Attributes returns the .gitattributes rules adoption writes for the engine
// (docs/adr/0016-figures-for-adopters.md, section 5). The engine hash, the spec and SVG hashes in
// each figure's JSON and the committed player are compared byte for byte, so the engine, the specs
// and the outputs keep LF on every platform, and the vendored files, whose SHA-256 vendor.json
// records, are never converted: the -text rule comes last and wins for them.
func Attributes() []string {
	return []string{
		Directory + "/** text eol=lf",
		SpecDirectory + "/*.ts text eol=lf",
		OutputDirectory + "/* text eol=lf",
		Directory + "/" + VendoredTree + " -text",
	}
}

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

// priorDigests maps the SHA-256, taken with LF line endings, of every text an earlier Praetor
// shipped at one of the family's managed paths to that path: the family's Prior
// (internal/managedasset). Adoption refreshes a file holding exactly one of these texts without
// --force. internal/managedasset/testdata/shipped/figure-engine.sha256 records every text ever
// shipped, and TestShippedTextLedger fails until each outgoing text is listed here. The texts are
// not kept as fixtures, since one player is about 240 kB; git history holds each of them, and the
// ledger line was appended from the text itself. The registry allows 64 entries per family
// (managedasset.MaxPriorTexts); the family's declaration states what each kind of change costs.
var priorDigests = map[string]string{
	// build.mjs before check and sources skipped a repository without a figure spec.
	"f75975e7f7cbb147b156c3a8f4a00bffef985aa18fa4ee67f6505479fd8e730c": Directory + "/build.mjs",
	// README.md before it named make docs-figures, the .gitattributes block and the REUSE
	// override.
	"043ff416cab0a7536c0816f564e2f44ea0e8daa7d294ef7722d1878b1c0962de": Directory + "/README.md",
	// build.mjs, checks.mjs and README.md while sources read the MkDocs defaults (mkdocs.yml,
	// docs/) unless --config and --docs named others, so the managed target and the workflow step
	// never read a Starlight site's pages.
	"47beb62eee90e992412312cdccbd0d91f54f090dbb342916efdf1eb08a816ac5": Directory + "/build.mjs",
	"03d1e126c3d583f264d54a646b1f3532b6a5e989edfbf1911353c9dedb97584b": Directory + "/checks.mjs",
	"1ee2a92653faea8a36e8e11a788ffa7004bb8b3e8844cb360a113bff5501b66f": Directory + "/README.md",
	// README.md while its restore command named adopt --force without the lock source a forced
	// run needs (#502).
	"1879a244bd33b2fb50e95d848f65bf1bab2ca09fcc0507d997d0e11e8d5a6fd0": Directory + "/README.md",
	// astro.mjs while it added its remark plugin only to markdown.remarkPlugins. Without
	// @astrojs/markdown-remark, Astro 7 refuses that list at config setup
	// (coerceLegacyMarkdownPlugins in astro/dist/core/config/validate.js), so the site did not
	// build; with it installed, the default Sätteri processor does not run the list, so every
	// figure block stayed a code block.
	"b767cd0c78901610f3f35e7a983e73eaad62b5b1d0f6f2a0e369213abb80ef05": Directory + "/astro.mjs",
	// README.md while it said a block naming a figure without JSON fails an Astro build; an .md
	// page only logs the error and builds without its content.
	"5c4f4f5eb3e714fd2b3fee90505ef73b147c943cea8cc99a3aaec9096e5c0572": Directory + "/README.md",
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

// PriorDigests returns a copy of the digests of every earlier text of a managed path.
func PriorDigests() map[string]string {
	return maps.Clone(priorDigests)
}

// Read returns one canonical asset without exposing mutable embedded storage.
func Read(name string) ([]byte, error) {
	return util.ReadEmbeddedAsset(assets, "figures", Names(), name)
}
