// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package bump

import (
	"strings"
	"testing"
)

const (
	pinnedCheckoutSHA = "3d3c42e5aac5ba805825da76410c181273ba90b1"
	pinnedNodeSHA     = "820762786026740c76f36085b0efc47a31fe5020"
)

// Positive: an action pinned by full commit SHA with its release as a trailing comment is
// read at that release. A current release is up to date, not drift, and a SHA pin of a
// deprecated major carries the same deprecation as the tag would.
func TestScanWorkflowActionsSHAPinsReadRelease(t *testing.T) {
	repo := writePagesWorkflow(t, "name: CI\njobs:\n  test:\n    steps:\n"+
		"      - uses: actions/checkout@"+pinnedCheckoutSHA+"  # v7.0.1\n"+
		"      - name: Setup\n        uses: actions/setup-node@"+pinnedNodeSHA+"  # v4\n")
	got, deps, err := ScanWorkflowActions(t.Context(), repo)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	checkout := findPagesAction(t, got, "actions/checkout")
	if checkout.CurrentVersion != "v7.0.1" || !checkout.UpToDate || checkout.Deprecated {
		t.Errorf("checkout SHA pin at v7.0.1: want current and up to date, got %+v", checkout)
	}
	node := findPagesAction(t, got, "actions/setup-node")
	if node.CurrentVersion != "v4" || !node.Deprecated || len(deps) != 1 || deps[0].Component != "actions/setup-node@v4" {
		t.Errorf("setup-node SHA pin at v4: want the Node.js 20 deprecation, got %+v, %+v", node, deps)
	}
}

// Negative: a SHA without its release comment, or with a comment one space from it, is
// not the pinned form, so the scan compares the bare SHA as before and reports drift.
func TestScanWorkflowActionsSHAPinsWithoutReleaseStayBare(t *testing.T) {
	for name, line := range map[string]string{
		"no comment": "      - uses: actions/checkout@" + pinnedCheckoutSHA + "\n",
		"one space":  "      - uses: actions/checkout@" + pinnedCheckoutSHA + " # v7.0.1\n",
		"short SHA":  "      - uses: actions/checkout@" + pinnedCheckoutSHA[:39] + "  # v7.0.1\n",
	} {
		repo := writePagesWorkflow(t, "name: CI\njobs:\n  test:\n    steps:\n"+line)
		got, _, err := ScanWorkflowActions(t.Context(), repo)
		if err != nil {
			t.Fatalf("%s: scan: %v", name, err)
		}
		checkout := findPagesAction(t, got, "actions/checkout")
		if !strings.HasPrefix(pinnedCheckoutSHA, checkout.CurrentVersion) || checkout.UpToDate {
			t.Errorf("%s: want the bare SHA reported as drift, got %+v", name, checkout)
		}
	}
}

// Boundary: a commented-out SHA pin is still no step, CRLF line endings keep the release,
// a release without its "v" is read as written, and a workflow over the line bound is
// refused rather than half rewritten.
func TestScanWorkflowActionsSHAPinsBoundary(t *testing.T) {
	repo := writePagesWorkflow(t, "name: CI\r\njobs:\r\n  test:\r\n    steps:\r\n"+
		"      # - uses: actions/cache@"+pinnedNodeSHA+"  # v4\r\n"+
		"      - uses: actions/checkout@"+pinnedCheckoutSHA+"  # v7.0.1\r\n"+
		"      - uses: actions/setup-node@"+pinnedNodeSHA+"   # 7.0.0\r\n")
	got, deps, err := ScanWorkflowActions(t.Context(), repo)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 2 || len(deps) != 0 {
		t.Fatalf("want checkout and setup-node only, no deprecation, got %+v, %+v", got, deps)
	}
	if checkout := findPagesAction(t, got, "actions/checkout"); checkout.CurrentVersion != "v7.0.1" || !checkout.UpToDate {
		t.Errorf("CRLF SHA pin: got %+v", checkout)
	}
	if node := findPagesAction(t, got, "actions/setup-node"); node.CurrentVersion != "7.0.0" || !node.UpToDate {
		t.Errorf("release without v: got %+v", node)
	}
	atBound := strings.Repeat("\n", maxWorkflowLines-1)
	if _, err := withPinnedReleases(atBound); err != nil {
		t.Fatalf("a workflow at the line bound was refused: %v", err)
	}
	if _, err := withPinnedReleases(atBound + "\n"); err == nil {
		t.Fatal("a workflow over the line bound was rewritten")
	}
}
