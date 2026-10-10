// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package apicompat exposes the locked Go API compatibility gate the api:public-contract facet
// emits. The managed asset family registry (internal/managedasset) declares it as the API
// compatibility family; adoption, audit and the devcontainer bootstrap read it through it.
//
// The gate is one Go program, gate/main.go. Its build constraint (BuildTag) keeps it out of
// every ./... pattern, so an adopting module neither builds, tests, lints nor publishes the
// program as API, while "go run tools/apicompat/gate/main.go", which names the file, still
// builds it. A second file, gate/placeholder.go, holds the opposite constraint and a main that
// says how to run the gate, so the directory is a buildable package without the tag: a hook that
// vets or lints it by directory finds Go files instead of failing on a package whose files the
// constraint excludes (#842). The cost is that a ./... pattern now reaches the placeholder
// (a package main with no tests and no exported API), where it used to reach nothing there. Praetor lints and vets the program with the tag (Makefile lint,
// .golangci.yml) and tests it by building the embedded bytes (gate_test.go). Both files are
// kept clean under gofmt, gofumpt and go vet (scripts/test_emitted_hook_lint.py).
package apicompat

import (
	"embed"
	"io/fs"
	"maps"
	"slices"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// Directory is the repository-relative home of the gate.
	Directory = "tools/apicompat"
	// SourceFile is the Go file carrying the go:embed directive over the assets.
	SourceFile = Directory + "/assets.go"
	// GateFile is the gate program, relative to Directory.
	GateFile = "gate/main.go"
	// PlaceholderFile is the Go file that stands in for GateFile in a build without BuildTag.
	PlaceholderFile = "gate/placeholder.go"
	// BuildTag is the build constraint that keeps GateFile out of ./... patterns; PlaceholderFile
	// holds its negation.
	BuildTag = "apicompatgate"
	// WorkflowFile is the repository-relative hosted API compatibility gate.
	WorkflowFile = ".github/workflows/praetor-api.yml"
	// StatusContext is the exact required check emitted by WorkflowFile.
	StatusContext = "Go API Compatibility"
	// MaxAssets bounds all asset iteration.
	MaxAssets = 2
)

// Workflow is the hosted gate adoption writes to WorkflowFile, rendered for a repository whose
// default branch is main; adoption and audit render it for the repository's own default branch
// (managedasset.Family.ForBranch), which names the one branch a push runs it on. Its trigger and
// draft handling are the hosted gate shape (ghworkflow.HostedGateOn, HostedGateDraftStep,
// HostedGateStepIf in internal/ghworkflow/hostedgate.go), so a push to another branch or a tag
// starts no API comparison (#815). Its one job, named StatusContext, has no condition, so it
// reports on every pull request and the branch ruleset adoption renders from the workflows
// requires it. On a draft the job fails by design without comparing anything, saying the gate
// runs when the pull request is marked ready; the ready_for_review run then reports the context
// on the same head commit.
//
// A pull request compares its base commit with the merge commit the checkout action checks
// out; a push compares HEAD with the newest root release tag, and passes saying so while there
// is none. fetch-depth 0 fetches every commit and tag that comparison may name. setup-go
// pins stable so the gate runs on every runner without relying on a pre-installed toolchain
// or assuming a root go.mod exists. Because setup-go exports GOTOOLCHAIN=local unconditionally,
// the compare step sets GOTOOLCHAIN: auto in its environment so a module go or toolchain
// directive selects the toolchain (#1037). This fixes root modules and patch-level nested
// modules requiring a newer toolchain; a nested module whose go language version is newer than
// the checker toolchain is not covered (docs/guides/api-compatibility.md).
// The job keeps module and build caches of its own, keyed by its job name, instead of
// setup-go's shared one (internal/forge/go_cache_checks.go).
//
// Audit locks an adopter's copy to these bytes, so the text holds to the policies an adopter
// may enforce without being able to edit it: every action is pinned by full commit SHA with its
// release as a trailing comment, which Renovate's github-actions manager keeps current here and
// in the repository's own copy (renovate.json); and the text passes yamllint --strict under its
// default rules, with a line-length exemption for the lines a 40-hex SHA or a cache key carries
// past 80 columns.
const Workflow = `---
name: Praetor API Compatibility

` + ghworkflow.HostedGateOn + `
permissions:
  contents: read

jobs:
  api-compatibility:
    name: Go API Compatibility
    runs-on: ubuntu-26.04
    timeout-minutes: 60
    steps:
` + ghworkflow.HostedGateDraftStep + `      - name: Checkout source` + ghworkflow.HostedGateStepIf + `
        # yamllint disable-line rule:line-length
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1  # v7.0.1
        with:
          fetch-depth: 0
      - name: Setup Go` + ghworkflow.HostedGateStepIf + `
        # yamllint disable-line rule:line-length
        uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e  # v7.0.0
        with:
          go-version: stable
          cache: false
      # yamllint disable rule:line-length
      - name: Restore Go module cache` + ghworkflow.HostedGateStepIf + `
        uses: actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9  # v6.1.0
        with:
          path: ~/go/pkg/mod
          key: gomod-${{ runner.os }}-${{ runner.arch }}-${{ hashFiles('**/go.sum') }}
          restore-keys: |
            gomod-${{ runner.os }}-${{ runner.arch }}-
      - name: Restore Go build cache` + ghworkflow.HostedGateStepIf + `
        uses: actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9  # v6.1.0
        with:
          path: ~/.cache/go-build
          key: gobuild-${{ runner.os }}-${{ runner.arch }}-api-compatibility-${{ hashFiles('**/go.sum') }}-${{ github.sha }}
          restore-keys: |
            gobuild-${{ runner.os }}-${{ runner.arch }}-api-compatibility-${{ hashFiles('**/go.sum') }}-
            gobuild-${{ runner.os }}-${{ runner.arch }}-api-compatibility-
      # yamllint enable rule:line-length
      - name: Compare the API of every Go module` + ghworkflow.HostedGateStepIf + `
        env:
          BASE: ${{ github.event.pull_request.base.sha }}
          GOTOOLCHAIN: auto
        run: go run tools/apicompat/gate/main.go -base="$BASE"
`

// priorDigests maps the SHA-256 of every text an earlier Praetor shipped at one of the family's
// managed paths, taken with LF line endings, to that path: the family's Prior
// (internal/managedasset). Adoption refreshes a file holding exactly one of these texts without
// --force. testdata/prior holds each text (the gate program's start with api-gate-main), and
// TestPriorDigests recomputes every digest from it.
// internal/managedasset/testdata/shipped/api-compatibility.sha256 records every text ever
// shipped, and TestShippedTextLedger fails until each outgoing text is listed here.
var priorDigests = map[string]string{
	// The first gate, which ran on every push to every branch and tag and on every draft pull
	// request (#815).
	"a123aa00616a9ee913dda943e288a64f8f3e3518b7777a4f8ed866c10c47f37e": WorkflowFile,
	// The gate before it was gofumpt-formatted and before its header comment said that pushes run
	// on the default branch only and a draft fails without running it (#842, #824).
	"0f1a48dcdccd12c57c7c8cc80dfd7718339272b66c546bf6f80a19336bb74554": Directory + "/" + GateFile,
	// The gate before it excused a module that cannot be built on an api-compatibility exception
	// of the manifest (#849).
	"c785a3669ba9e88b2f6306ff0733f109fef2fad27c426759eb63936fd82c666b": Directory + "/" + GateFile,
	// The gate whose draft step ran under the runner's default shell, which is pwsh on a Windows
	// runner and cannot read the step's script.
	"e4306dbb7b164e9ca3040668b4a2b63b9092b8f35645b348252fb6d55f062f29": WorkflowFile,
	// The gate that installed the checker as module@version, whose x/tools cannot read Go 1.27.2
	// export data (#1049).
	"10c868a450275a43abc625176bd27cdf5e148f509f8704019940ca01076ee5f8": Directory + "/" + GateFile,
	// The gate before it ran on merge_group, so a merge queue never got its Go API Compatibility
	// context (#893).
	"d64b71821e056174c3127244483c916a3baa646b6f37f419eac44102c8c30dbd": WorkflowFile,
	// The gate in the draft skip shape before it ran on merge_group (#893).
	"2c9bafc92efe6d114d15881f3659020624a2e37920aab9628196a06d30b9816e": WorkflowFile,
	// The gate before the compare step set GOTOOLCHAIN: auto to override setup-go's
	// GOTOOLCHAIN=local (#1037).
	"f7e57ed83e70886bb6a37906d040d4e6352c71d79e3da67215d0243f6ad02a4c": WorkflowFile,
	// The draft skip rendering of the gate before the compare step set GOTOOLCHAIN: auto.
	"63c91ef6890b046db5a87ad1ec222e4db8a680f4350350b60e5db2934f74f35a": WorkflowFile,
}

var assetNames = [...]string{GateFile, PlaceholderFile}

//go:embed gate/main.go gate/placeholder.go
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
	return util.ReadEmbeddedAsset(assets, "apicompat", Names(), name)
}
