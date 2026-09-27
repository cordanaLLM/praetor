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
const Workflow = `name: Praetor Documentation Governance

on:
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
        uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - name: Setup Node.js
        uses: actions/setup-node@v7
        with:
          node-version: "24"
          cache: npm
          cache-dependency-path: tools/markdownlint/package-lock.json
      - name: Verify public Markdown
        run: node tools/markdownlint/verify.mjs
`

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

// Read returns one canonical asset without exposing mutable embedded storage.
func Read(name string) ([]byte, error) {
	return util.ReadEmbeddedAsset(assets, "markdownlint", Names(), name)
}
