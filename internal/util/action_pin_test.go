// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package util

import (
	"strings"
	"testing"
)

const pinTestSHA = "3d3c42e5aac5ba805825da76410c181273ba90b1"

// Positive: a step's uses: value is read with its comment, and the SHA-pinned form splits
// into action, SHA and release, a sub-path action included.
func TestParsePinnedActionPositive(t *testing.T) {
	ref, uses := ActionUsesValue("      - uses: actions/checkout@" + pinTestSHA + "  # v7.0.1  ")
	if !uses || ref != "actions/checkout@"+pinTestSHA+"  # v7.0.1" {
		t.Fatalf("uses value = %q, %v", ref, uses)
	}
	pin, ok := ParsePinnedAction(ref)
	if !ok || pin != (PinnedAction{Action: "actions/checkout", SHA: pinTestSHA, Release: "v7.0.1"}) {
		t.Fatalf("pin = %+v, %v", pin, ok)
	}
	pin, ok = ParsePinnedAction("github/codeql-action/init@" + pinTestSHA + "  # v4")
	if !ok || pin.Action != "github/codeql-action/init" || pin.Release != "v4" {
		t.Fatalf("sub-path pin = %+v, %v", pin, ok)
	}
	// #610: the one-space form Renovate and pinact write reads as the two-space form does.
	pin, ok = ParsePinnedAction("actions/checkout@" + pinTestSHA + " # v7.0.1")
	if !ok || pin.Release != "v7.0.1" {
		t.Fatalf("one-space pin = %+v, %v", pin, ok)
	}
}

// Positive: ParseSHAPin reads every SHA pin, a bare SHA and one whose comment names no
// release included, and keeps the release only where the comment names one.
func TestParseSHAPin(t *testing.T) {
	for ref, release := range map[string]string{
		"actions/checkout@" + pinTestSHA:                 "",
		"actions/checkout@" + pinTestSHA + "  # pinned":  "",
		"actions/checkout@" + pinTestSHA + " # v7.0.1":   "v7.0.1",
		"actions/checkout@" + pinTestSHA + "\t# 7.0.1":   "7.0.1",
		"actions/checkout@" + pinTestSHA + " # v7.0.1 x": "",
		"actions/checkout@" + pinTestSHA + " #v7.0.1":    "",
		"actions/checkout@" + pinTestSHA + "  #  \tv7.1": "v7.1",
	} {
		pin, ok := ParseSHAPin(ref)
		if !ok || pin.Action != "actions/checkout" || pin.SHA != pinTestSHA || pin.Release != release {
			t.Errorf("%q: pin = %+v, %v; want release %q", ref, pin, ok, release)
		}
	}
	for _, ref := range []string{
		"actions/checkout@v7",
		"actions/checkout@" + pinTestSHA[:39] + " # v7.0.1",
		"actions/checkout@" + pinTestSHA + "0",
		"actions/checkout@" + pinTestSHA + "# v7.0.1",
		"./.github/actions/local",
	} {
		if pin, ok := ParseSHAPin(ref); ok {
			t.Errorf("%q: parsed %+v", ref, pin)
		}
	}
}

// Negative: a comment line declares no uses: key, and a tag, a branch, a short or
// uppercase SHA, a SHA without its release comment, a non-release comment, a comment
// glued to the SHA, a local action and a Docker reference are not the pinned form.
func TestParsePinnedActionNegative(t *testing.T) {
	if ref, uses := ActionUsesValue("      # - uses: actions/checkout@v7"); uses {
		t.Fatalf("a comment line declared %q", ref)
	}
	if ref, uses := ActionUsesValue("      run: echo uses: x"); uses {
		t.Fatalf("a run line declared %q", ref)
	}
	for name, ref := range map[string]string{
		"tag":             "actions/checkout@v7",
		"tag and comment": "actions/checkout@v7.0.1  # v7.0.1",
		"branch":          "owner/action/path@main",
		"short SHA":       "actions/checkout@" + pinTestSHA[:39] + "  # v7.0.1",
		"uppercase SHA":   "actions/checkout@" + strings.ToUpper(pinTestSHA) + "  # v7.0.1",
		"no comment":      "actions/checkout@" + pinTestSHA,
		"glued comment":   "actions/checkout@" + pinTestSHA + "# v7.0.1",
		"39-hex, 1 space": "actions/checkout@" + pinTestSHA[:39] + " # v7.0.1",
		"not a release":   "actions/checkout@" + pinTestSHA + "  # pinned",
		"local":           "./.github/actions/local",
		"docker":          "docker://alpine:3",
	} {
		if pin, ok := ParsePinnedAction(ref); ok {
			t.Fatalf("%s: parsed %+v", name, pin)
		}
	}
}

// Boundary: a release without its "v", a single-number release, a wider comment gap, a tab
// before the "#" and a bare "uses:" with no value hold at the edges of the form.
func TestParsePinnedActionBoundary(t *testing.T) {
	for ref, release := range map[string]string{
		"actions/setup-node@" + pinTestSHA + "  # 7.0.0":   "7.0.0",
		"actions/setup-node@" + pinTestSHA + "  # v7":      "v7",
		"actions/setup-node@" + pinTestSHA + "     # v7.1": "v7.1",
		"actions/setup-node@" + pinTestSHA + "\t# v7.1":    "v7.1",
	} {
		if pin, ok := ParsePinnedAction(ref); !ok || pin.Release != release {
			t.Fatalf("%q: pin = %+v, %v", ref, pin, ok)
		}
	}
	if pin, ok := ParsePinnedAction("actions/setup-node@" + pinTestSHA + "  # v7.0.1-rc"); ok {
		t.Fatalf("a prerelease comment parsed as %+v", pin)
	}
	if ref, uses := ActionUsesValue("uses:"); !uses || ref != "" {
		t.Fatalf("bare uses: = %q, %v", ref, uses)
	}
}

// Positive, negative and boundary: ScanActionUses returns the lines and each uses: line in
// order, pinned or not, skips comment and run lines, and refuses one line past its bound.
func TestScanActionUses(t *testing.T) {
	workflow := "steps:\n  - uses: actions/checkout@" + pinTestSHA + "  # v7.0.1\n" +
		"  # - uses: actions/cache@v4\n  - run: echo uses: x\n  - uses: ./local\n"
	lines, uses, err := ScanActionUses(workflow, 6)
	if err != nil || len(lines) != 6 || len(uses) != 2 {
		t.Fatalf("scan = %d lines, %+v, %v", len(lines), uses, err)
	}
	if uses[0].Line != 1 || !uses[0].Pinned || uses[0].Pin.Release != "v7.0.1" {
		t.Fatalf("pinned use = %+v", uses[0])
	}
	if uses[1].Line != 4 || uses[1].Pinned || uses[1].SHAPinned || uses[1].Ref != "./local" {
		t.Fatalf("local use = %+v", uses[1])
	}
	_, bare, err := ScanActionUses("- uses: actions/checkout@"+pinTestSHA+"\n", 2)
	if err != nil || len(bare) != 1 || !bare[0].SHAPinned || bare[0].Pinned || bare[0].Pin.SHA != pinTestSHA {
		t.Fatalf("bare SHA use = %+v, %v", bare, err)
	}
	if _, _, err := ScanActionUses(workflow, 5); err == nil {
		t.Fatal("a workflow one line past the bound was scanned")
	}
	if lines, uses, err := ScanActionUses("", 1); err != nil || len(lines) != 1 || len(uses) != 0 {
		t.Fatalf("empty workflow = %v, %+v, %v", lines, uses, err)
	}
}
