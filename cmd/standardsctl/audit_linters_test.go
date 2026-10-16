// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// The CLI audit runs the declared linters gate (#1289). Boundary: the fixture declares no
// linters, so the gate passes without a workflow. Positive: a repository declaring gitleaks with
// a workflow step running it passes. Negative: the same without the step fails and reports a finding
// naming gitleaks and where it was declared.
func TestAuditDeclaredLinters_CLI(t *testing.T) {
	f := newAuditFixture(t)

	// 1. Boundary: fixture policy declares no linters
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit failed on empty linters: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[PASS] Declared linters: policy declares no linters; no workflow is required.")

	// 2. Declare gitleaks in facet security:high
	writeFixtureFile(t, f.dir, ".config/archetypes/facets/security-high.yaml", `id: "security:high"
name: "High security"
linters:
  - "gitleaks"
`)
	lf := &lockFixture{dir: f.dir, manifest: &config.Manifest{Profiles: []string{"framework"}, Facets: []string{"security:high"}}}
	lf.writeLock(t, lf.digestOf(t, ".config/archetypes/framework.yaml"), lf.digestOf(t, ".config/archetypes/facets/security-high.yaml"), "")
	gitCommitAll(t, f.dir, f.gitEnv, "declare gitleaks")

	// 3. Negative: workflow does not run gitleaks -> fails
	writeFixtureFile(t, f.dir, ".github/workflows/ci.yml", `name: CI
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo "build"
`)
	out, err = f.audit(t)
	if err == nil {
		t.Fatalf("expected audit failure when gitleaks is not run, got pass:\n%s", out)
	}
	mustErrContain(t, err, "[FAIL] Declared linters: 1 declared linter(s) not run by any workflow:")
	mustErrContain(t, err, `gitleaks: declared by facet "security:high", not run by any workflow`)

	// 4. Positive: workflow runs gitleaks in a step -> passes
	writeFixtureFile(t, f.dir, ".github/workflows/ci.yml", `name: CI
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: Secret Scan
        run: gitleaks detect --verbose
`)
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("expected audit pass when gitleaks is run, got: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[PASS] Declared linters: every declared linter is run by a CI workflow (1 verified).")

	// 5. Positive: declared-linters exception in .standards.yaml excuses missing linter -> passes
	writeFixtureFile(t, f.dir, ".github/workflows/ci.yml", `name: CI
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo "build"
`)
	manifestContent := fixtureManifest("acme", "widgets", false) + `exceptions:
  - rule: "declared-linters"
    tool: "gitleaks"
    reason: "scheduled for next quarter"
    expires: "2026-11-01"
`
	writeFixtureFile(t, f.dir, ".standards.yaml", manifestContent)
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("expected audit pass when gitleaks is excused by exception, got: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[PASS] Declared linters: 0 declared linters run; 1 excepted:")
	mustContain(t, out, "gitleaks, excepted until 2026-11-01")
}
