// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package bump

import (
	"os"
	"path/filepath"
	"testing"
)

// node20CorePins are the last Node.js 20 majors of the four core setup actions.
var node20CorePins = map[string]string{
	"actions/checkout":     "v4",
	"actions/setup-go":     "v5",
	"actions/setup-node":   "v4",
	"actions/setup-python": "v5",
}

// Positive: each core action's last Node.js 20 major is flagged, and the warning names the
// registry's Node.js 24 major as the upgrade target.
func TestScanWorkflowActionsNode20CorePinsDeprecated(t *testing.T) {
	repo := writePagesWorkflow(t, `name: CI
jobs:
  test:
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
      - uses: actions/setup-node@v4
      - uses: actions/setup-python@v5
`)
	got, deps, err := ScanWorkflowActions(t.Context(), repo)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(deps) != len(node20CorePins) {
		t.Errorf("want %d Node.js 20 deprecations, got %+v", len(node20CorePins), deps)
	}
	for name, ver := range node20CorePins {
		cand := findPagesAction(t, got, name)
		want := "Node.js 20 runtime deprecated; upgrade to v7"
		if cand.CurrentVersion != ver || cand.LatestVersion != "v7" || !cand.Deprecated || cand.Warning != want {
			t.Errorf("%s@%s: want deprecated with %q, got %+v", name, ver, want, cand)
		}
	}
}

// Negative: the v7 pins are current, so nothing is deprecated and nothing drifts.
func TestScanWorkflowActionsNode24CorePinsCurrent(t *testing.T) {
	repo := writePagesWorkflow(t, `name: CI
jobs:
  test:
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
      - uses: actions/setup-node@v7
      - uses: actions/setup-python@v7
`)
	got, deps, err := ScanWorkflowActions(t.Context(), repo)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("v7 pins must not warn, got %+v", deps)
	}
	for name := range node20CorePins {
		cand := findPagesAction(t, got, name)
		if cand.Deprecated || cand.CurrentVersion != cand.LatestVersion {
			t.Errorf("%s: want current == latest and not deprecated, got %+v", name, cand)
		}
	}
}

// Boundary: the first Node.js 24 major below the latest is drift but not deprecated, and an
// exact tag of a deprecated major is outside the major-keyed table.
func TestScanWorkflowActionsNode24FirstMajorNotDeprecated(t *testing.T) {
	repo := writePagesWorkflow(t, `name: CI
jobs:
  test:
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v5.4.0
`)
	got, deps, err := ScanWorkflowActions(t.Context(), repo)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("checkout@v5 and an exact setup-go tag must not warn, got %+v", deps)
	}
	checkout := findPagesAction(t, got, "actions/checkout")
	if checkout.Deprecated || checkout.LatestVersion != "v7" {
		t.Errorf("checkout@v5: want drift to v7 without deprecation, got %+v", checkout)
	}
}

// engineWorkflowFiles are the workflow-shaped files the engine ships outside
// .github/workflows: its composite action and the Go workflow templates adoption emits.
var engineWorkflowFiles = []string{
	filepath.Join(".github", "actions", "praetor-adopt", "action.yml"),
	filepath.Join("templates", "go", "ci-go.yml.tmpl"),
	filepath.Join("templates", "go", "security-go.yml.tmpl"),
}

// Positive: no engine workflow, composite action or emitted template pins a deprecated
// action runtime, and every core action present is at the registry version.
func TestEngineWorkflowsCarryNoDeprecatedActionPins(t *testing.T) {
	root := filepath.Join("..", "..")
	got, deps, err := ScanWorkflowActions(t.Context(), root)
	if err != nil {
		t.Fatalf("scan engine workflows: %v", err)
	}
	for _, rel := range engineWorkflowFiles {
		content, readErr := os.ReadFile(filepath.Join(root, rel))
		if readErr != nil {
			t.Fatalf("read %s: %v", rel, readErr)
		}
		for _, m := range workflowActionRegex.FindAllStringSubmatch(string(content), 100) {
			cand, dep := buildActionCandidate(m[1], m[2], rel)
			got = append(got, cand)
			if dep != nil {
				deps = append(deps, *dep)
			}
		}
	}
	if len(deps) != 0 {
		t.Errorf("engine files pin deprecated action runtimes: %+v", deps)
	}
	for _, cand := range got {
		if _, core := node20CorePins[cand.Action]; core && cand.CurrentVersion != cand.LatestVersion {
			t.Errorf("%s pins %s@%s but the registry names %s", cand.WorkflowFile, cand.Action, cand.CurrentVersion, cand.LatestVersion)
		}
	}
}
