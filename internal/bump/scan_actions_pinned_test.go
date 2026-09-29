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
// read at that release, whatever whitespace precedes the "#" (#610). A current release is
// compared as up to date, not drift, and a SHA pin of a deprecated major carries the same
// deprecation as the tag would. The scan records the line and the commit, and leaves the
// pin unverified until VerifyActionPins asks its upstream.
func TestScanWorkflowActionsSHAPinsReadRelease(t *testing.T) {
	for name, gap := range map[string]string{"one space": " ", "two spaces": "  ", "tab": "\t"} {
		repo := writePagesWorkflow(t, "name: CI\njobs:\n  test:\n    steps:\n"+
			"      - uses: actions/checkout@"+pinnedCheckoutSHA+gap+"# v7.0.1\n"+
			"      - name: Setup\n        uses: actions/setup-node@"+pinnedNodeSHA+gap+"# v4\n")
		got, deps, err := ScanWorkflowActions(t.Context(), repo)
		if err != nil {
			t.Fatalf("%s: scan: %v", name, err)
		}
		checkout := findPagesAction(t, got, "actions/checkout")
		if checkout.CurrentVersion != "v7.0.1" || !checkout.UpToDate || checkout.Deprecated ||
			checkout.Line != 5 || checkout.PinnedSHA != pinnedCheckoutSHA || checkout.Pin != PinUnverified {
			t.Errorf("%s: checkout SHA pin at v7.0.1: want current, line 5, unverified, got %+v", name, checkout)
		}
		node := findPagesAction(t, got, "actions/setup-node")
		if node.CurrentVersion != "v4" || !node.Deprecated || len(deps) != 1 || deps[0].Component != "actions/setup-node@v4" {
			t.Errorf("%s: setup-node SHA pin at v4: want the Node.js 20 deprecation, got %+v, %+v", name, node, deps)
		}
	}
}

// Negative: a SHA without a release comment, or with a comment that names no release, is
// an unversioned SHA pin: neither drift from a SHA to a tag nor up to date, even for an
// action the registry does not list (#610). A 39-character SHA with a comment is no SHA pin
// at all, so the scan compares it as the bare reference it is and reports drift.
func TestScanWorkflowActionsSHAPinsWithoutReleaseAreUnversioned(t *testing.T) {
	for name, line := range map[string]string{
		"no comment":    "      - uses: actions/checkout@" + pinnedCheckoutSHA + "\n",
		"not a release": "      - uses: actions/checkout@" + pinnedCheckoutSHA + "  # pinned\n",
		"unlisted":      "      - uses: example/unlisted-action@" + pinnedCheckoutSHA + "\n",
	} {
		repo := writePagesWorkflow(t, "name: CI\njobs:\n  test:\n    steps:\n"+line)
		got, _, err := ScanWorkflowActions(t.Context(), repo)
		if err != nil || len(got) != 1 {
			t.Fatalf("%s: scan = %+v, %v", name, got, err)
		}
		if got[0].CurrentVersion != pinnedCheckoutSHA || got[0].UpToDate || got[0].Pin != PinUnversioned ||
			ActionDriftStatus(got[0]) != "[UNVERSIONED]" {
			t.Errorf("%s: want an unversioned SHA pin, got %+v (%s)", name, got[0], ActionDriftStatus(got[0]))
		}
	}
	repo := writePagesWorkflow(t, "name: CI\njobs:\n  test:\n    steps:\n"+
		"      - uses: actions/checkout@"+pinnedCheckoutSHA[:39]+" # v7.0.1\n")
	got, _, err := ScanWorkflowActions(t.Context(), repo)
	if err != nil {
		t.Fatalf("short SHA: scan: %v", err)
	}
	checkout := findPagesAction(t, got, "actions/checkout")
	if checkout.CurrentVersion != pinnedCheckoutSHA[:39] || checkout.UpToDate || checkout.Pin != "" || checkout.PinnedSHA != "" {
		t.Errorf("short SHA: want the bare reference reported as drift, got %+v", checkout)
	}
}

// Boundary: a commented-out SHA pin is still no step, with one space before its "#" as with
// two, CRLF line endings keep the release, a release without its "v" is read as written, and
// a workflow over the line bound is refused rather than half rewritten.
func TestScanWorkflowActionsSHAPinsBoundary(t *testing.T) {
	repo := writePagesWorkflow(t, "name: CI\r\njobs:\r\n  test:\r\n    steps:\r\n"+
		"      # - uses: actions/cache@"+pinnedNodeSHA+"  # v4\r\n"+
		"      # - uses: actions/cache@"+pinnedNodeSHA+" # v4\r\n"+
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
