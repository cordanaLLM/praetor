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
	// PlayerDirectory is the committed bundle's home. Go consumers derive its path from here;
	// bundle.mjs and sync_interfig.py each declare the same repository-relative name for their
	// own runtimes.
	PlayerDirectory = Directory + "/player"
	// MaxAssets bounds all asset iteration: the inventory's exact size.
	MaxAssets = 16
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
// the stylesheet, the site generators, the ESLint gate config, then the authoring guide.
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
	"figures.css",
	"mkdocs_hook.py",
	"astro.mjs",
	"serve.mjs",
	"eslint.figures.mjs",
	"README.md",
}

// priorPlayerDigests records the bundle texts releases wrote under dist before #1356 and under
// player before ADR-0021 retired the committed adopter bundle. Adoption removes only these
// unedited files; any other file belongs to the adopter and is kept. Keys are legacy paths,
// values are canonical text digests, so CRLF checkouts remain recognisable through
// util.LookupCanonicalText.
var priorPlayerDigests = map[string]map[string]string{
	Directory + "/dist/loader.js": {
		"9c741950900a175696e47248c2acd258cb1bb8703f84221d18a0a21146d340fb": "figure player before #1356",
		"abd878ebc16730be4fe5aef0316230f079825a5723b461bcde48c87e770d8b53": "figure player before #1356",
	},
	Directory + "/dist/player.js": {
		"2db2e3bfda05a3776f165530a519b6c4a5def717b4dd4192484f8d86076a4a94": "figure player before #1356",
		"5e0f6d26e888c954ed2983ffe66d7fe8d68c1069b1489212ad6fc32477a52646": "figure player before #1356",
	},
	Directory + "/dist/THIRD-PARTY-LICENSES.txt": {
		"c2eed2e1431851ddca033c197728664db21fc82d766e465c44f210b949d57a18": "figure player before #1356",
		"fe156b1f350ef8779088bc9c73f37609d6266d8f2f04a29995d1586b0451cf9d": "figure player before #1356",
	},
	Directory + "/player/loader.js": {
		"9c741950900a175696e47248c2acd258cb1bb8703f84221d18a0a21146d340fb": "figure player before ADR-0021",
		"abd878ebc16730be4fe5aef0316230f079825a5723b461bcde48c87e770d8b53": "figure player before ADR-0021",
	},
	Directory + "/player/player.js": {
		"2db2e3bfda05a3776f165530a519b6c4a5def717b4dd4192484f8d86076a4a94": "figure player before React 19.3.1 (#1363)",
		"5e0f6d26e888c954ed2983ffe66d7fe8d68c1069b1489212ad6fc32477a52646": "figure player before ADR-0021",
	},
	Directory + "/player/THIRD-PARTY-LICENSES.txt": {
		"c2eed2e1431851ddca033c197728664db21fc82d766e465c44f210b949d57a18": "figure player licenses before React 19.3.1 (#1363)",
		"fe156b1f350ef8779088bc9c73f37609d6266d8f2f04a29995d1586b0451cf9d": "figure player licenses before ADR-0021",
	},
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
	// MkDocs and Astro before SVG fallback omitted the unavailable player loader.
	"1cf80d7e961b928950a3836fe18175d4985b8705e8135ab4d5b515ad9922f47f": Directory + "/mkdocs_hook.py",
	"5ebe22430d1dc231c008dca43e3353eb6285f15e32735707064e2e5ed797eb2f": Directory + "/astro.mjs",
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
	// mkdocs_hook.py, VENDOR.md and README.md before they held to the lint settings adopters run
	// over every tracked file: ruff check with the I and RUF100 rules and ruff format at 100
	// columns, and markdownlint at its default 80-column MD013 (#845, #578).
	"8cb476e7baceb9e1f9205248c125284d43c4eaf69886c84d00db999738f21fb0": Directory + "/mkdocs_hook.py",
	"93c89f5ba72ef0111c735aebda14a5bf2bc3012948f092b64ff312f93510910c": Directory + "/third_party/interfig/VENDOR.md",
	"06a88460af38141f8a1ae087a4d54b97514749cb0ad5459a8acec171e0a42580": Directory + "/README.md",
	// checks.mjs before readBytes read through one open descriptor (CodeQL file-system race) and
	// the slot and bracket escapes became complete.
	"26f8704a73120d008c3dd1428ef5d1e80921f75c8d2db85b3a43db6a99b46e5b": Directory + "/checks.mjs",
	// checks.mjs whose bracket() escaped class syntax without the backslash in the same pass (CodeQL
	// incomplete sanitization at that site), shipped by #1252.
	"2e10d401929e7880d606f4e7721727c546ad3c3d0790826910eda5ef7a9c9bce": Directory + "/checks.mjs",
	// astro.mjs, checks.mjs, serve.mjs and VENDOR.md while they named the player directory
	// tools/figures/dist/, which a common dist/ ignore rule hid from commits (#1356).
	"6fb3d70f670f1b7bf76dc985621053c926879fc574575b264b4960e59ba6841e": Directory + "/astro.mjs",
	"8ead48a0270ab3c18d06a1be1fa545af576b1a92d31f023c35111bc363eef4df": Directory + "/checks.mjs",
	"0eeb8c757e73dcb06007af467d979c275fa4a5c164ff73339985032edd90d115": Directory + "/serve.mjs",
	"f25ec96946a63eca0aba9621d0c16553d906175e8c4243b5ca57761eaf0b5178": Directory + "/third_party/interfig/VENDOR.md",
	// Texts that still read the player from tools/figures/dist/ after #1356 moved it.
	"f43f86f52694396378a5ba52a405cd5566a38bd7164131813fc8c53833f528ba": Directory + "/astro.mjs",
	"b48768908b0e088b2369ac9d0d6e797462e6609b9cca7930e6d7ba20e07d73ce": Directory + "/README.md",
	"5fd25882376baf07f054f8e81486b98fdb049edbc8bae4301467494d0dce95a3": Directory + "/third_party/interfig/VENDOR.md",
	"d3c1e4371141fe5ebd1eb52dac43b0a721e1956ef8d4e50a4492df6e5907b6aa": Directory + "/serve.mjs",
	"a4a98cb1195488fbf1d7812bfb021fd5ac5f562f42dcbb2368758b6da3087b69": Directory + "/mkdocs_hook.py",
	// mkdocs_hook.py while a docstring line exceeded the 100 columns adopters' flake8 allows.
	"c9c20ae005cabdf8e10d683f31e2e5159552b8f901995bec855184d22aeaf460": Directory + "/mkdocs_hook.py",
	"c2cfffd51d9773c3f894eee3a55993786c6c778117ea5dfd0c24ac42d1753044": Directory + "/mkdocs_hook.py",
	// README.md before documenting the emitted ESLint configuration for figure assets.
	"15f75d9348ee456c9d78594307df374ebcc24427fdf435e920a54c4e5461cbfa": Directory + "/README.md",
	// checks.mjs before the marker pattern was built with new RegExp to parse whole under Semgrep.
	"42aeec6906cd60ec2b1ee6cdde24ad312f37abca46a6e71a3016e05786f2919a": Directory + "/checks.mjs",
	// checks.mjs before a site without a released player (ADR-0021) needed no loader.
	"c52f169e426695c14b2af587bcc253d46d8612a352a2ef77e6f0a84229ce84a9": Directory + "/checks.mjs",
	// mkdocs_hook.py before ADR-0021 fetched figures-player.tar.gz for adopters without a local
	// tools/figures/player directory.
	"40577f164ab56013875f3736d1436ba44a703b1c8b82a73235e7f65ecba8e227": Directory + "/mkdocs_hook.py",
	// mkdocs_hook.py before the https-guarded player download carried semgrep's nosemgrep marker.
	"bf2646129a0c5923daad5b36fdae6b0d7abd7748e6150a2b846a0f51d09fe0c8": Directory + "/mkdocs_hook.py",
	// VENDOR.md while lines 33, 123, 128, 135 still named the engine-only player directory and bundle step.
	"89e498132bbf2b9ec15d91b8043395dfe02c352580b89affe8b4a3d7fe13f454": Directory + "/third_party/interfig/VENDOR.md",
	// Assets before the release host check, Astro locked-player fetch and unreleased-pin docs.
	"9387798626f5e7ee522be147d140bbc14a6d42ada1cdc7d49ba719ad58447f20": Directory + "/mkdocs_hook.py",
	"8db774ed65d587aefeffdae6d2bcc227d9bd3c7a61d01f51bd1580dd1445cdde": Directory + "/astro.mjs",
	"00a60626eb2d7e232a3aeb831f854ca4f97393570ba6fca07220c1c78930da5f": Directory + "/checks.mjs",
	"8aa976bb6c60cb167e100b5256002559d86373951d0f6d9435b9750a6bf64ec7": Directory + "/README.md",
	// Outgoing review-round text; fixture: figures-astro-review-round1.mjs.txt.
	"1ff5529c53aa9c7c1514eeea40e2772e7838fd9622d979122eaab75dfb95ef1b": Directory + "/astro.mjs",
	// Outgoing review-round text; fixture: figures-astro-review-round2-part1.mjs.txt.
	"28c187d2788633dcbe68f5b11c599683f1bc2606696556b4f043cc51dac933f3": Directory + "/astro.mjs",
	// Outgoing review-round text; fixture: figures-mkdocs_hook-review-round1.py.txt.
	"84afc44008d4f5432e62dc3739d8fa0b1922ee2ff101195de877741c766e97cc": Directory + "/mkdocs_hook.py",
	// Outgoing review-round text; fixture: figures-mkdocs_hook-review-round2-part1.py.txt.
	"8b84933fa5d19f74c6e9d642da60f674ed5bad091267c2691c316476ad9b97eb": Directory + "/mkdocs_hook.py",
	// Outgoing review-round text; fixture: figures-astro-review-round2-part2.mjs.txt.
	"f2ad5050408a1938cfdb646e2389f5aead53a8364606f79932310a082833d4c2": Directory + "/astro.mjs",
	// Outgoing review-round text; fixture: figures-mkdocs_hook-review-round2-part2.py.txt.
	"c98a28d2e3a43ca2963d7cbdbf636ad78ed620cc4639c1102bc1c74ee8286cc9": Directory + "/mkdocs_hook.py",
	// astro.mjs before the player download validated every redirect.
	"f9d0f2df2922acbe2abd75bf1437fca1264a35a5c2e118beafd6fc2abdbc2f10": Directory + "/astro.mjs",
	// astro.mjs before the player lock read was bounded by bytes read.
	"8d2a6afce5c8fd2cf0bf0c9457199a19a6fad93868a5f5dd9f9faf32241fe257": Directory + "/astro.mjs",
	// checks.mjs before the player lock read was bounded by bytes read.
	"547d4bfe5d1895a907cba6c0aaf4540f3825811838af94c1b6c0e77082acb211": Directory + "/checks.mjs",
}

//go:embed core.mjs checks.mjs build.mjs types.ts third_party/interfig/vendor.json third_party/interfig/VENDOR.md third_party/interfig/upstream/LICENSE third_party/interfig/upstream/src/svg.ts third_party/interfig/upstream/src/geometry.ts third_party/interfig/upstream/src/model.ts figures.css mkdocs_hook.py astro.mjs serve.mjs eslint.figures.mjs README.md
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

// PriorPlayerDigests returns private copies of the legacy dist bundle digests for migration.
func PriorPlayerDigests() map[string]map[string]string {
	result := make(map[string]map[string]string, len(priorPlayerDigests))
	for rel, digests := range priorPlayerDigests {
		result[rel] = maps.Clone(digests)
	}
	return result
}

// Read returns one canonical asset without exposing mutable embedded storage.
func Read(name string) ([]byte, error) {
	return util.ReadEmbeddedAsset(assets, "figures", Names(), name)
}
