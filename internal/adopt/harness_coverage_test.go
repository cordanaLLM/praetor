// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdoptForceRendersCoverageClaims(t *testing.T) {
	repo := newTestRepo(t, "widget")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, "widget.go"), "package widget\n")
	stageAdoptPaths(t, repo, "go.mod", "widget.go")
	adoptHarnessForCoverage(t, repo)

	coverage := `version: 1
rules:
  - id: HISS-01
    coverage:
      - language: go
        state: partial
        mechanism: fixture scanner
        rationale: fixture claim
  - id: HISS-02
    coverage:
      - language: go
        state: manual
        mechanism: peer review
        rationale: human review
  - id: HISS-03
    coverage:
      - language: go
        state: unsupported
        mechanism: none
        rationale: no check exists
`
	mustWrite(t, filepath.Join(repo, ".config", "hiss", "coverage.yaml"), coverage)
	stageAdoptPaths(t, repo, ".config/hiss/coverage.yaml")
	adoptHarnessForCoverage(t, repo)

	harness := agentsHarness(t, repo)
	for _, want := range []string{"partial (go): fixture scanner", "manual (go): peer review", "unsupported (go): none", "| **HISS-05**"} {
		if !strings.Contains(harness, want) {
			t.Errorf("forced harness lacks %q", want)
		}
	}
	if row := rowFor(t, harness, "HISS-05"); !strings.Contains(row, "praetorctl audit` HISS scan") {
		t.Errorf("engine-scanned HISS-05 must name its scanner without a local claim: %s", row)
	}
}

func TestAdoptWithoutCoverageRendersEngineOrUnknown(t *testing.T) {
	repo := newTestRepo(t, "widget")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, "widget.go"), "package widget\n")
	stageAdoptPaths(t, repo, "go.mod", "widget.go")
	adoptHarnessForCoverage(t, repo)

	harness := agentsHarness(t, repo)
	if row := rowFor(t, harness, "HISS-01"); !strings.Contains(row, "praetorctl audit` HISS scan") {
		t.Errorf("engine-scanned rule must name its scanner: %s", row)
	}
	if row := rowFor(t, harness, "HISS-18"); !strings.Contains(row, "unknown") {
		t.Errorf("unscanned rule must render unknown: %s", row)
	}
	if strings.Contains(harness, "not enforced") {
		t.Error("harness without coverage must not claim rules are not enforced")
	}
}

func adoptHarnessForCoverage(t *testing.T, repo string) {
	t.Helper()
	_, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework", Force: true})
	if err != nil {
		t.Fatalf("force adopt %s: %v", repo, err)
	}
}
