// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// playerCreditsRow is a credits row naming the player packages the checkout's lock installs.
const playerCreditsRow = "| [React](https://github.com/facebook/react) 19.3.0, with react-dom 19.3.0 and scheduler 0.28.0 | " +
	"Bundled into the figure player (`tools/figures/dist/player.js`) | MIT |"

func checkoutNoticeSources(t *testing.T) NoticeSources {
	t.Helper()
	sources, err := ReadNoticeSources(context.Background(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return sources
}

// Positive: the committed credits page names every package the committed player bundles at the
// version the figure lock installs, and a minimal row naming them passes too.
func TestCreditsNameThePlayerPackagesAtTheirLockVersions(t *testing.T) {
	sources := checkoutNoticeSources(t)
	if err := CheckCredits(string(readRepoFile(t, AcknowledgementsFile)), sources); err != nil {
		t.Fatal(err)
	}
	if err := CheckCredits("| Project | Use | License |\n| :-- | :-- | :-- |\n"+playerCreditsRow+"\n", sources); err != nil {
		t.Fatal(err)
	}
}

// Negative: a row naming an outgoing version, a row missing a package, and a page without the row
// or with two of them fail, naming the file and what is missing.
func TestCreditsRejectStaleOrMissingPlayerRows(t *testing.T) {
	sources := checkoutNoticeSources(t)
	for name, credits := range map[string]string{
		"outgoing React":    strings.Replace(playerCreditsRow, ") 19.3.0,", ") 19.2.0,", 1),
		"missing scheduler": strings.Replace(playerCreditsRow, " and scheduler 0.28.0", "", 1),
		"no row":            "| Project | Use | License |\n",
		"two rows":          playerCreditsRow + "\n" + playerCreditsRow + "\n",
	} {
		err := CheckCredits(credits, sources)
		if err == nil || !strings.HasPrefix(err.Error(), AcknowledgementsFile) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	err := CheckCredits(strings.Replace(playerCreditsRow, ") 19.3.0,", ") 19.2.0,", 1), sources)
	if err == nil || !strings.Contains(err.Error(), "does not name react 19.3.0;") {
		t.Fatalf("the stale package is not named: %v", err)
	}
}

// Boundary: a package name or version only as part of a longer term does not count, a version
// followed by punctuation does, and the link text counts while its URL does not.
func TestCreditsPackageTermBoundary(t *testing.T) {
	for text, want := range map[string]bool{
		"react 19.3.0, with":  true,
		"react 19.3.0":        true,
		"preact 19.3.0":       false,
		"react 19.3.01":       false,
		"react 19.3.0-rc":     false,
		"react-dom 19.3.0":    false,
		"(react 19.3.0)":      true,
		"@scope/react 19.3.0": false,
	} {
		if got := namesTerm(text, "react 19.3.0", termByte); got != want {
			t.Errorf("namesTerm(%q) = %v, want %v", text, got, want)
		}
	}
	linkOnly := "| [react 19.3.0](https://example.test/react-dom 19.3.0 scheduler 0.28.0) | `tools/figures/dist/player.js` | MIT |"
	if err := CheckCredits(linkOnly, checkoutNoticeSources(t)); err == nil {
		t.Fatal("package names inside a link URL counted")
	}
}

// remoteActionRepository: a remote action with or without a path, a local action, a Docker
// reference and a bare name.
func TestRemoteActionRepository(t *testing.T) {
	for ref, want := range map[string]string{
		"actions/checkout@v7":                     "actions/checkout",
		"Azure/setup-helm@0123 # v4":              "azure/setup-helm",
		"anchore/sbom-action/download-syft@v0.20": "anchore/sbom-action",
		"./.github/actions/go-cache":              "",
		"docker://alpine:3":                       "",
		"checkout":                                "",
	} {
		got, remote := remoteActionRepository(ref)
		if got != want || remote != (want != "") {
			t.Errorf("remoteActionRepository(%q) = %q, %v; want %q", ref, got, remote, want)
		}
	}
}
