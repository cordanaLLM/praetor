// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package bump

import (
	"os"
	"path/filepath"
	"strings"
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

// Boundary: the first Node.js 24 major below the latest is drift but not deprecated, as a
// tag and as an exact release of that major.
func TestScanWorkflowActionsNode24FirstMajorNotDeprecated(t *testing.T) {
	repo := writePagesWorkflow(t, `name: CI
jobs:
  test:
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6.0.0
`)
	got, deps, err := ScanWorkflowActions(t.Context(), repo)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("checkout@v5 and setup-go@v6.0.0 must not warn, got %+v", deps)
	}
	checkout := findPagesAction(t, got, "actions/checkout")
	if checkout.Deprecated || checkout.LatestVersion != "v7" {
		t.Errorf("checkout@v5: want drift to v7 without deprecation, got %+v", checkout)
	}
}

// Positive: an exact release carries its major's runtime deprecation, whether a tag names
// it or a SHA pin's release comment does, with or without its "v" (#614).
func TestScanWorkflowActionsExactReleaseCarriesMajorDeprecation(t *testing.T) {
	repo := writePagesWorkflow(t, "name: CI\njobs:\n  test:\n    steps:\n"+
		"      - uses: actions/checkout@"+pinnedCheckoutSHA+"  # v4.2.2\n"+
		"      - uses: actions/setup-python@v5.4.0\n"+
		"      - uses: actions/setup-go@"+pinnedNodeSHA+" # 5.4.0\n"+
		"      - uses: actions/cache@v4.2\n")
	got, deps, err := ScanWorkflowActions(t.Context(), repo)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(deps) != 4 {
		t.Fatalf("want four Node.js 20 deprecations, got %+v", deps)
	}
	for _, dep := range deps {
		if dep.Kind != "runner-runtime-deprecated" || !strings.HasPrefix(dep.Details, node20Deprecated+"; upgrade to v") {
			t.Errorf("deprecation %+v: want the Node.js 20 advisory with its upgrade target", dep)
		}
	}
	for _, name := range []string{"actions/checkout", "actions/setup-python", "actions/setup-go", "actions/cache"} {
		if cand := findPagesAction(t, got, name); !cand.Deprecated || ActionDriftStatus(cand) != "[DEPRECATED]" {
			t.Errorf("%s: want deprecated, got %+v", name, cand)
		}
	}
}

// Negative and boundary: an exact release of a major the table does not list is not
// deprecated (checkout v5.0.0 drifts, setup-go v7.0.1 is current), a version that is no tag
// is never looked up by major, and an exact entry outranks its major's, so the table can
// record a major whose runtime changed between releases.
func TestDeprecatedRuntimeByMajor(t *testing.T) {
	for version, want := range map[string]bool{
		"v4": true, "v4.2.2": true, "4.2.2": true, "v4.2": true, "v4.2.2-rc.1": true,
		"v5.0.0": false, "v7.0.1": false, "main": false, pinnedCheckoutSHA: false,
	} {
		if _, got := deprecatedRuntime("actions/checkout", version); got != want {
			t.Errorf("actions/checkout@%s deprecated = %v, want %v", version, got, want)
		}
	}
	if _, got := deprecatedRuntime("example/unlisted-action", "v1.0.0"); got {
		t.Error("an action the table does not list was deprecated")
	}
	checkout := deprecatedActionVersions["actions/checkout"]
	checkout["v4.9.9"] = "exact entry"
	t.Cleanup(func() { delete(checkout, "v4.9.9") })
	if reason, _ := deprecatedRuntime("actions/checkout", "v4.9.9"); reason != "exact entry" {
		t.Errorf("exact entry lost to its major: %q", reason)
	}
	repo := writePagesWorkflow(t, "name: CI\njobs:\n  test:\n    steps:\n"+
		"      - uses: actions/checkout@v5.0.0\n      - uses: actions/setup-go@v7.0.1\n")
	got, deps, err := ScanWorkflowActions(t.Context(), repo)
	if err != nil || len(deps) != 0 {
		t.Fatalf("scan = %+v, %v; want no deprecation", deps, err)
	}
	if status := ActionDriftStatus(findPagesAction(t, got, "actions/checkout")); status != "[DRIFT]" {
		t.Errorf("checkout@v5.0.0 = %s, want [DRIFT]", status)
	}
	if status := ActionDriftStatus(findPagesAction(t, got, "actions/setup-go")); status != "[UP-TO-DATE]" {
		t.Errorf("setup-go@v7.0.1 = %s, want [UP-TO-DATE]", status)
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
// action runtime, and every core action present is at the registry version: a tag at its
// major or a SHA pin whose release comment is (ActionPinCurrent), such as praetor-docs.yml's.
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
		released, releaseErr := withPinnedReleases(string(content))
		if releaseErr != nil {
			t.Fatalf("read %s: %v", rel, releaseErr)
		}
		for _, m := range workflowActionRegex.FindAllStringSubmatch(released, 100) {
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
		if _, core := node20CorePins[cand.Action]; core && !cand.UpToDate {
			t.Errorf("%s pins %s@%s but the registry names %s", cand.WorkflowFile, cand.Action, cand.CurrentVersion, cand.LatestVersion)
		}
	}
}
