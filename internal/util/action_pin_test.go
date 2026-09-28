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
}

// Negative: a comment line declares no uses: key, and a tag, a branch, a short or
// uppercase SHA, a SHA without its release comment or one space from it, a non-release
// comment, a local action and a Docker reference are not the pinned form.
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
		"one space":       "actions/checkout@" + pinTestSHA + " # v7.0.1",
		"not a release":   "actions/checkout@" + pinTestSHA + "  # pinned",
		"local":           "./.github/actions/local",
		"docker":          "docker://alpine:3",
	} {
		if pin, ok := ParsePinnedAction(ref); ok {
			t.Fatalf("%s: parsed %+v", name, pin)
		}
	}
}

// Boundary: a release without its "v", a single-number release, a wider comment gap and a
// bare "uses:" with no value hold at the edges of the form.
func TestParsePinnedActionBoundary(t *testing.T) {
	for ref, release := range map[string]string{
		"actions/setup-node@" + pinTestSHA + "  # 7.0.0":   "7.0.0",
		"actions/setup-node@" + pinTestSHA + "  # v7":      "v7",
		"actions/setup-node@" + pinTestSHA + "     # v7.1": "v7.1",
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
