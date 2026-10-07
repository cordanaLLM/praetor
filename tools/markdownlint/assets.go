// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package markdownlint exposes the locked documentation-gate assets emitted by adoption.
// The managed asset family registry (internal/managedasset) declares them as the Markdown
// family; adoption, audit and the devcontainer bootstrap read them through it.
package markdownlint

import (
	"embed"
	"io/fs"
	"maps"
	"slices"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// Directory is the repository-relative home of the documentation gate.
	Directory = "tools/markdownlint"
	// SourceFile is the Go file carrying the go:embed directive over the assets.
	SourceFile = Directory + "/assets.go"
	// WorkflowFile is the repository-relative hosted documentation gate.
	WorkflowFile = ".github/workflows/praetor-docs.yml"
	// StatusContext is the exact required check emitted by WorkflowFile.
	StatusContext = "Documentation Governance"
	// MaxAssets bounds all asset iteration.
	MaxAssets = 5
)

// Workflow is the dedicated, required hosted documentation gate adoption writes to
// WorkflowFile, rendered for a repository whose default branch is main; adoption and audit
// render it for the repository's own default branch (managedasset.Family.ForBranch), which names
// the one branch a push runs it on. Its job name is StatusContext. The job runs on every pull
// request that is not a draft and again when a draft is marked ready (ready_for_review), so the
// branch ruleset still requires it (the draft skip in internal/forge/workflow_guard.go); a push
// to another branch or a tag, and a draft, start no run (#815).
//
// After the Markdown check the job runs the figure engine's check and sources commands
// (docs/adr/0016-figures-for-adopters.md, operator decision 4), which the documentation facet
// writes beside this gate and which skip, saying why, in a repository without a figure; the job
// stays on Linux, where the Node set up for the Markdown gate runs them.
//
// Audit locks an adopter's copy to these bytes, so the text holds to the policies an adopter
// may enforce without being able to edit it: every action is pinned by full commit SHA with
// its release as a trailing comment, which repositories requiring SHA pinning demand and
// Renovate's github-actions manager keeps current (renovate.json reads this file too, so one
// update moves this text and the repository's own copy together, and the update fails
// internal/managedasset's TestShippedTextLedger until the outgoing text is in priorDigests);
// and the text passes yamllint --strict under its default rules: a document start, a quoted
// 'on' key that the truthy rule does not read as a boolean, and a line-length exemption for
// the two pin lines, which a 40-hex SHA plus its comment carries past 80 columns at step
// indentation.
const Workflow = `---
name: Praetor Documentation Governance

'on':
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review]
  push:
    branches: ['main']

permissions:
  contents: read

jobs:
  documentation:
    name: Documentation Governance
    # A draft skips the job; marking it ready runs it (ready_for_review).
    if: github.event.pull_request.draft != true
    runs-on: ubuntu-26.04
    timeout-minutes: 10
    steps:
      - name: Checkout source
        # yamllint disable-line rule:line-length
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1  # v7.0.1
        with:
          fetch-depth: 0
      - name: Setup Node.js
        # yamllint disable-line rule:line-length
        uses: actions/setup-node@820762786026740c76f36085b0efc47a31fe5020  # v7.0.0
        with:
          node-version: "24"
          cache: npm
          cache-dependency-path: tools/markdownlint/package-lock.json
      - name: Verify public Markdown
        run: node tools/markdownlint/verify.mjs
      - name: Verify figures
        run: |
          node tools/figures/build.mjs check
          node tools/figures/build.mjs sources
`

// priorDigests maps the SHA-256 of every text an earlier Praetor shipped at one of the
// family's managed paths, taken with LF line endings, to that path: the family's Prior
// (internal/managedasset). Adoption refreshes a file holding exactly one of these texts
// without --force. testdata/prior holds each text, and TestPriorDigestsReproduce recomputes
// every digest from it. internal/managedasset/testdata/shipped/markdown.sha256 records every
// text ever shipped, and TestShippedTextLedger fails until each outgoing text is listed here.
var priorDigests = map[string]string{
	// The first documentation gate: ubuntu-latest, checkout and setup-node v4.
	"d4e893f5fee713d3a13d88277097a8b85adce9a886151a776fe24ee54bfe49fa": WorkflowFile,
	// The explicit ubuntu-26.04 runner, actions still on v4.
	"c474aa586d9e96f354027fc507543850bc7cf6e31fe927075604856649caff3a": WorkflowFile,
	// Actions on the v7 tags, before SHA pinning and the yamllint document start.
	"97d1fad8184587e73dfa25af2cc4e30cf9fa278abf5868fdc0dc27c7a222c95e": WorkflowFile,
	// The SHA-pinned gate before it ran the figure engine's check and sources commands.
	"938c1926d853a57149b0ede1cc6b9b7ce68eba62f6af30e8d0768458df98a62e": WorkflowFile,
	// The gate that ran on every push to every branch and tag and on every draft pull request
	// (#815).
	"05d50eae0edd1599f23468e570e0ebf0af3d2e7cc65bf126d4f7976b002b2a84": WorkflowFile,
	// The markdownlint configuration before its yamllint document start.
	"67aad4771daac4e6db3c2f8b65dfbd93f72c4067c9187ec759014bbc71bbfd0d": Directory + "/markdownlint-cli2.yaml",
	// The first verify.mjs, before its self-test ran the scratch rule through a symlinked
	// ancestor.
	"8273fa87112eb15cc3382ba23ba894c542d2ae8352042eab253e9c8b503704f2": Directory + "/verify.mjs",
	// verify.mjs before it excluded vendored upstream Markdown from the style rules.
	"52cce450d5fd46647d2852919fb279122be22134c81353707a4f214f5f59c9c4": Directory + "/verify.mjs",
	// verify.mjs while the vendored interfig tree sat at the repository root, before it moved
	// under tools/figures.
	"a74e1e4f30edb5edb32d5e54b0d21d0d1d7dcfb1984293275ae6ddb6092c77de": Directory + "/verify.mjs",
	// verify.mjs with fixed 4,096-file and 1 MiB per-file bounds, no .standards.yaml style
	// exclusions, and markdownlint-cli2 run from the repository root, where it discovered
	// repository configuration files.
	"fa82c6380aaf001bab89f25c5dbdceecfa2c2ce59ffc4b3e2ad157693bc1b245": Directory + "/verify.mjs",
	// verify.mjs with one 125-line inventory self-test, before the HISS script scanner held it
	// to the HISS-04 function length.
	"cdf3f38807fb22c4a59c0e082fc3c94232cd4d691fa5a87df6cc49c1e2c7caba": Directory + "/verify.mjs",
	// verify.mjs checking markdownlint-cli2 0.23.3 after the advisory-free lock, before the HISS
	// script scanner held it to the HISS-04 function length.
	"4736fa57692bd9a585e4ecb94b00b1762e8fa53e8222bcd987c96878c31f79e1": Directory + "/verify.mjs",
	// package.json and package-lock.json before js-yaml and micromatch became direct
	// dependencies for the .standards.yaml documentation block.
	"9eec2a40bdaff8ec72019d859c7c4f5a969ddd3b816106eb9629c54de52f71f2": Directory + "/package.json",
	"7b2c98f403f05acb4cb6a9645ae2ef9dac9e824ec83eb6b19281e1fd55dda1c7": Directory + "/package-lock.json",
	// The first scratch-link rule, which compared process.argv[1] by spelling and skipped
	// main() under a symlinked temporary directory.
	"56d7c5a15f5622e6ecf4e6fc2877bb796fd0db4fb73c6f07a41bec637830242c": Directory + "/no-private-scratch-links.mjs",
	// The scratch-link rule with a fixed 4,096-file inventory and a fixed 262,144-event parse
	// bound, before either grew with the bounds a repository declares.
	"fec9b4f1b48f5847c91fb64e52483a51ae3c1cc4b2574af6eedce7210cad0d5f": Directory + "/no-private-scratch-links.mjs",
	// package.json, package-lock.json and verify.mjs on markdownlint-cli2 0.23.2 and js-yaml
	// 5.2.2, whose lock installed smol-toml 1.7.0 (GHSA-7w5x-hrqm-74c2), js-yaml 5.2.2
	// (GHSA-r3ph-w7gj-g6xm) and markdown-it 14.3.0 (GHSA-253c-mchw-3w2r).
	"1ef55cd03a7401bae13927bbca5d85ad601dd2ad4b8b931d79a3317551b83c2a": Directory + "/package.json",
	"abbe0b93eb99ce54d6cadc43ecb131a655d1aaf28009aa3804ad2ba97796b479": Directory + "/package-lock.json",
	// The scratch-link rule that read a srcdoc document by recursion, dropped a failed worker
	// stop with an empty .catch and carried functions over the HISS-04 length, before the HISS
	// script scanner read it.
	"93ee4469a98c4d44c19b74ca75b06391c2f360add39dd19b5120dbb7645520a9": Directory + "/no-private-scratch-links.mjs",
	// package.json and package-lock.json on markdownlint-cli2 0.23.3, js-yaml 5.4.1 and
	// micromark 4.0.2.
	"ae5668d6a66dfd57f83b298d09a82f5143b0894c0214a5db5e079cf54d6b92b3": Directory + "/package.json",
	"3be5e6c9037886cc39192da27e536b9dcefa3860074efed81d3dd9ea4e4a5d72": Directory + "/package-lock.json",
	// verify.mjs once the HISS script scanner read the documentation gate's scripts, before the
	// non-major dependency update.
	"b39f736a108e13579aa25249ef2ccb0fec28981f9fe86027f15793be6b2443e6": Directory + "/verify.mjs",
	// package.json, package-lock.json and verify.mjs that ran the markdownlint-cli2 0.23.3 binary
	// and matched style exclusions with micromatch 4.0.8, whose lock installed braces 3.0.3
	// (GHSA-vfj7-8cjw-p6xm, no fixed release; #736).
	"321e5210fc0ec943b369164932e87622811ebb0c7c49907343a25fd2d28d8ae5": Directory + "/package.json",
	"b1cb57bf987fe1f70a9e80e627fe2d3838bbab6dacc7150a1b66df6460364d2a": Directory + "/package-lock.json",
	"70a908977d34a88a789e3139209f3d501fe2493076f194527332487353ebf9b4": Directory + "/verify.mjs",
	// package.json, package-lock.json and verify.mjs on smol-toml 1.8.0 before the 1.9.0 update and lockfile
	// maintenance refresh (#645, #658).
	"1c9f14af6a030dc78bce47b521711a44d5c37af67dadd5d77358f104f4059cba": Directory + "/package.json",
	"2a11b6947b2513b654772a53c9249e0ed1322e9344666f0a42aaa1f72670d266": Directory + "/package-lock.json",
	"a3080c17ab3bb62974b1347950a1dc3feac795f758abfb416edf22854dcff45f": Directory + "/verify.mjs",
	// verify.mjs with a fixed 120 s budget per lint child, whose timeout named only the node
	// executable, before documentation.lint_timeout_seconds and the suspects report (#784).
	"5dfc7a8138b5020526fa481b315f4888789f831b11f054f4b6cddfd6572d1f16": Directory + "/verify.mjs",
	// package.json, package-lock.json and verify.mjs before the katex override, whose lock
	// installed katex 0.16.47 (GHSA-238p-pmpm-9mq7) through micromark-extension-math (#793).
	"5d769f012c60688714ef40fe3498174559d5e9eec7c527e5b6605a75b2571d24": Directory + "/package.json",
	"d94aec74aec5ae8fedb118122b74aaa3ba347ec7cf413cc3cee975f1e2eacd2a": Directory + "/package-lock.json",
	"b5da2d1072959fe473d2866919ae756f2ab81484175f583475118d490a81ef13": Directory + "/verify.mjs",
}

var assetNames = [...]string{
	"package.json",
	"package-lock.json",
	"markdownlint-cli2.yaml",
	"verify.mjs",
	"no-private-scratch-links.mjs",
}

//go:embed package.json package-lock.json markdownlint-cli2.yaml verify.mjs no-private-scratch-links.mjs
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
	return util.ReadEmbeddedAsset(assets, "markdownlint", Names(), name)
}
