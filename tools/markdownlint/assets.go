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
// WorkflowFile. Its job name is StatusContext.
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
  push:

permissions:
  contents: read

jobs:
  documentation:
    name: Documentation Governance
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
	// The markdownlint configuration before its yamllint document start.
	"67aad4771daac4e6db3c2f8b65dfbd93f72c4067c9187ec759014bbc71bbfd0d": Directory + "/markdownlint-cli2.yaml",
	// The first verify.mjs, before its self-test ran the scratch rule through a symlinked
	// ancestor.
	"8273fa87112eb15cc3382ba23ba894c542d2ae8352042eab253e9c8b503704f2": Directory + "/verify.mjs",
	// verify.mjs before it excluded vendored upstream Markdown from the style rules.
	"52cce450d5fd46647d2852919fb279122be22134c81353707a4f214f5f59c9c4": Directory + "/verify.mjs",
	// The first scratch-link rule, which compared process.argv[1] by spelling and skipped
	// main() under a symlinked temporary directory.
	"56d7c5a15f5622e6ecf4e6fc2877bb796fd0db4fb73c6f07a41bec637830242c": Directory + "/no-private-scratch-links.mjs",
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
