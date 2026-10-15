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

// playerTerms returns "<name> <version>" for every package the committed player bundles, at the
// version the checkout's figure lock installs: the same derivation CheckCredits holds the page to,
// so a dependency bump needs no edit here (#1354 moved react 19.3.0 to 19.3.1).
func playerTerms(t *testing.T, sources NoticeSources) []string {
	t.Helper()
	components, err := figurePlayerRows(sources.FigureLicenses, sources.FigureLock)
	if err != nil || len(components) < 2 {
		t.Fatalf("player packages from the checkout's lock: %v (%d)", err, len(components))
	}
	terms := make([]string, 0, len(components))
	for _, component := range components {
		terms = append(terms, component.name+" "+component.version)
	}
	return terms
}

// playerCreditsRow is a minimal credits row naming terms as the player's packages.
func playerCreditsRow(terms []string) string {
	return "| " + strings.Join(terms, ", ") + " | Bundled into the figure player (`tools/figures/dist/player.js`) | MIT |"
}

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
	if err := CheckCredits("| Project | Use | License |\n| :-- | :-- | :-- |\n"+playerCreditsRow(playerTerms(t, sources))+"\n", sources); err != nil {
		t.Fatal(err)
	}
}

// Negative: a row naming an outgoing version, a row missing a package, and a page without the row
// or with two of them fail, naming the file and what is missing.
func TestCreditsRejectStaleOrMissingPlayerRows(t *testing.T) {
	sources := checkoutNoticeSources(t)
	terms := playerTerms(t, sources)
	first := strings.Fields(terms[0])
	outgoing := append([]string{first[0] + " 0.0.0-outgoing"}, terms[1:]...)
	row := playerCreditsRow(terms)
	for name, credits := range map[string]string{
		"outgoing first package": playerCreditsRow(outgoing),
		"missing last package":   playerCreditsRow(terms[:len(terms)-1]),
		"no row":                 "| Project | Use | License |\n",
		"two rows":               row + "\n" + row + "\n",
	} {
		err := CheckCredits(credits, sources)
		if err == nil || !strings.HasPrefix(err.Error(), AcknowledgementsFile) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	err := CheckCredits(playerCreditsRow(outgoing), sources)
	if err == nil || !strings.Contains(err.Error(), "does not name "+terms[0]+";") {
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
