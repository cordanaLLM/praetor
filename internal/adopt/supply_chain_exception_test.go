// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// PlannedReleaseWorkflow (#1259). Positive: a repository whose workflows write no provenance
// plans the release workflow adoption records. Negative: once a workflow writes provenance,
// nothing is planned. Boundary: the planned path is the one newSupplyChainException writes.
func TestPlannedReleaseWorkflow(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".github/workflows/ci.yml", "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: make test\n")
	planned, err := PlannedReleaseWorkflow(context.Background(), root)
	if err != nil {
		t.Fatalf("PlannedReleaseWorkflow: %v", err)
	}
	if planned != ".github/workflows/release.yml" {
		t.Fatalf("planned = %q, want .github/workflows/release.yml", planned)
	}
	gap, err := measureSupplyChainGap(context.Background(), root, supplyChainPolicy(3, false, false).SupplyChain)
	if err != nil {
		t.Fatal(err)
	}
	if recorded := newSupplyChainException(gap, time.Now()).Path; recorded != planned {
		t.Fatalf("adoption records %q but the audit plans %q", recorded, planned)
	}

	write(".github/workflows/publish.yml", releaseSteps(directAttestation))
	planned, err = PlannedReleaseWorkflow(context.Background(), root)
	if err != nil {
		t.Fatalf("PlannedReleaseWorkflow with a provenance workflow: %v", err)
	}
	if planned != "" {
		t.Fatalf("planned = %q after a workflow began writing provenance, want none", planned)
	}
}
