// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	figureassets "github.com/cordanaLLM/praetor/tools/figures"
)

const (
	fixtureOlderEngineRevision = "32a35c8"
	fixtureRunningRevision     = "18c50fc7476a"
	fixtureChecks32a35c8Digest = "42aeec6906cd60ec2b1ee6cdde24ad312f37abca46a6e71a3016e05786f2919a"
)

func fixtureChecks32a35c8Text(t *testing.T) string {
	t.Helper()
	// Exact 32a35c8 bytes; reconstructing them from the current asset drifts on later edits.
	raw, err := os.ReadFile(filepath.Join("testdata", "managed-family", "checks-32a35c8.mjs.txt"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	if digest != fixtureChecks32a35c8Digest {
		t.Fatalf("checks.mjs 32a35c8 digest = %s, want %s", digest, fixtureChecks32a35c8Digest)
	}
	return string(raw)
}

const fixtureFacetDocsSEO = `---
id: "docs:seo-portal"
name: "Documentation SEO & Web Vitals Portal"
description: "Enforces Schema.org JSON-LD structured data, sitemap generation,
  robots.txt integrity, and Core Web Vitals optimizations"

linters:
  - "markdownlint"
  - "spectral"

devcontainer_features:
  - "ghcr.io/devcontainers/features/common-utils:2"
`

func setupFixtureAdopterRepo(t *testing.T, lockVersion string) string {
	t.Helper()
	root := documentationAuditFixture(t)
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\nrepository:\n  default_branch: main\nfacets:\n  - docs:seo-portal\n")
	writeFixtureFile(t, root, ".config/archetypes/facets/docs-seoportal.yaml", fixtureFacetDocsSEO)
	lockContent := fmt.Sprintf(`version: 1
pinned_version: %q
digest: "sha256:3495794d21766391ad5f8c07bd18104bc3a06afbf3aa54f772fa3ee887fa70c8"
facets:
  - id: "docs:seo-portal"
    version: "v1.0.0"
    digest: "sha256:145722c161ed8e436369d573c2b736fe7f579b8a1e33a85119e3b10268563428"
`, lockVersion)
	writeFixtureFile(t, root, ".standards.lock", lockContent)
	return root
}

// TestAuditManagedAssetLock_Positive_OlderLockedEngineWithLockedTextPassesWithNotice:
// Positive: a fixture adopter locked at an older engine passes audit with locked text,
// and one notice line naming both engines and the force refresh command is emitted.
func TestAuditManagedAssetLock_Positive_OlderLockedEngineWithLockedTextPassesWithNotice(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })
	version = fixtureRunningRevision

	root := setupFixtureAdopterRepo(t, config.UnreleasedLockVersion(fixtureOlderEngineRevision))
	checksText := fixtureChecks32a35c8Text(t)
	writeFixtureFile(t, root, filepath.Join(figureassets.Directory, "checks.mjs"), checksText)

	enabled := &config.Manifest{Facets: []string{"docs:seo-portal"}}
	out, err := captureStdout(t, func() error {
		return docGate(t.Context(), enabled, root)
	})
	if err != nil {
		t.Fatalf("audit failed on older locked engine with locked text: %v", err)
	}
	if !strings.Contains(out, "[WARN] Managed assets match locked Praetor") || !strings.Contains(out, adopt.ForceCommand("")) {
		t.Errorf("expected [WARN] notice naming the force refresh (plain adopt keeps the locked text), got:\n%s", out)
	}
	if !strings.Contains(out, "praetorctl adopt") {
		t.Errorf("expected refresh command 'praetorctl adopt' in stdout, got:\n%s", out)
	}
	running := buildVersion()
	if !strings.Contains(out, running) {
		t.Errorf("expected newer engine %q in stdout, got:\n%s", running, out)
	}
}

// TestAuditManagedAssetLock_Negative_HandEditedAssetFails:
// Negative: a hand-edited asset still fails with named reason (strictness unchanged).
func TestAuditManagedAssetLock_Negative_HandEditedAssetFails(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })
	version = fixtureRunningRevision

	root := setupFixtureAdopterRepo(t, config.UnreleasedLockVersion(fixtureOlderEngineRevision))
	checksText := fixtureChecks32a35c8Text(t) + "\n// operator hand-edit\n"
	writeFixtureFile(t, root, filepath.Join(figureassets.Directory, "checks.mjs"), checksText)

	enabled := &config.Manifest{Facets: []string{"docs:seo-portal"}}
	_, err := captureStdout(t, func() error {
		return docGate(t.Context(), enabled, root)
	})
	if err == nil {
		t.Fatal("expected audit to fail on hand-edited asset, but it passed")
	}
	const wantPrefix = "[FAIL] Documentation gate asset tools/figures/checks.mjs differs from the locked Praetor asset"
	if !strings.Contains(err.Error(), wantPrefix) {
		t.Fatalf("expected error containing %q, got: %v", wantPrefix, err)
	}
}

// TestAuditManagedAssetLock_Boundary_LockEqualToBinaryNoNotice:
// Boundary: when lock is equal to binary, audit passes and no notice is emitted.
func TestAuditManagedAssetLock_Boundary_LockEqualToBinaryNoNotice(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })
	version = "v1.0.0"

	root := setupFixtureAdopterRepo(t, "v1.0.0")

	enabled := &config.Manifest{Facets: []string{"docs:seo-portal"}}
	out, err := captureStdout(t, func() error {
		return docGate(t.Context(), enabled, root)
	})
	if err != nil {
		t.Fatalf("audit failed when lock equals binary: %v", err)
	}
	if strings.Contains(out, "Managed assets match locked Praetor") {
		t.Errorf("expected no notice when lock equals binary, got:\n%s", out)
	}
}

// TestAdopt_Boundary_PlainAdoptLeavesLockedTextAndLockConsistent:
// Boundary: plain adopt (no --force) writes either text matching lock or moves lock
// consistently in same run; never leaves pair audit refuses.
func TestAdopt_Boundary_PlainAdoptLeavesLockedTextAndLockConsistent(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })
	version = fixtureRunningRevision

	root := setupFixtureAdopterRepo(t, config.UnreleasedLockVersion(fixtureOlderEngineRevision))
	checksText := fixtureChecks32a35c8Text(t)
	checksRel := filepath.Join(figureassets.Directory, "checks.mjs")
	writeFixtureFile(t, root, checksRel, checksText)

	// Run plain adopt
	_, err := adopt.Adopt(t.Context(), adopt.AdoptOptions{
		Path:   root,
		Engine: runningBuild(),
	})
	if err != nil {
		t.Fatalf("plain adopt failed: %v", err)
	}

	actualChecks, err := os.ReadFile(filepath.Join(root, checksRel))
	if err != nil {
		t.Fatal(err)
	}
	lockAfter := readFixtureFile(t, root, ".standards.lock")
	if strings.Contains(lockAfter, fixtureOlderEngineRevision) {
		// Lock did not move: checks.mjs must still match the locked text
		if string(actualChecks) != checksText {
			t.Fatalf("plain adopt rewrote asset to newer canonical while lock stayed at %s", fixtureOlderEngineRevision)
		}
	}

	// Verify audit passes after plain adopt
	enabled := &config.Manifest{Facets: []string{"docs:seo-portal"}}
	_, auditErr := captureStdout(t, func() error {
		return docGate(t.Context(), enabled, root)
	})
	if auditErr != nil {
		t.Fatalf("audit refused assets after plain adopt: %v", auditErr)
	}
}
