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
	return "| " + strings.Join(terms, ", ") + " | Bundled into the figure player (`tools/figures/player/player.js`) | MIT |"
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
	if err := CheckCredits(string(readRepoFile(t, AcknowledgementsFile)), sources, UpstreamCreditSources{}); err != nil {
		t.Fatal(err)
	}
	if err := CheckCredits("| Project | Use | License |\n| :-- | :-- | :-- |\n"+playerCreditsRow(playerTerms(t, sources))+"\n", sources, UpstreamCreditSources{}); err != nil {
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
		err := CheckCredits(credits, sources, UpstreamCreditSources{})
		if err == nil || !strings.HasPrefix(err.Error(), AcknowledgementsFile) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	err := CheckCredits(playerCreditsRow(outgoing), sources, UpstreamCreditSources{})
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
	linkOnly := "| [react 19.3.0](https://example.test/react-dom 19.3.0 scheduler 0.28.0) | `tools/figures/player/player.js` | MIT |"
	if err := CheckCredits(linkOnly, checkoutNoticeSources(t), UpstreamCreditSources{}); err == nil {
		t.Fatal("package names inside a link URL counted")
	}
}

// consoleCreditsFixture supplies explicit console pins independently of the checkout's bumps.
func consoleCreditsFixture(detail string) UpstreamCreditSources {
	return UpstreamCreditSources{
		Credits: Credits{Entries: []CreditEntry{{
			Name: "SvelteKit", Detail: detail, Paths: []string{"console/package.json"},
			Packages: []string{"npm:@sveltejs/kit", "npm:@sveltejs/adapter-static"},
		}}},
		PathTexts:     map[string]string{"console/package.json": `{"devDependencies":{"@sveltejs/kit":"3.1.1","@sveltejs/adapter-static":"4.0.0"}}`},
		ManifestTexts: map[string]string{"console/package.json": `{"devDependencies":{"@sveltejs/kit":"3.1.1","@sveltejs/adapter-static":"4.0.0"}}`},
	}
}

// Negative and positive: a scope-only identifier answers for every pinned package it covers, so
// a stale claim of one of them is compared with its pin instead of passing unread.
func TestCreditsScopeIdentifierCoversItsPins(t *testing.T) {
	for detail, want := range map[string]string{
		"kit 3.1.0":                       "SvelteKit) names @sveltejs/kit 3.1.0; console/package.json pins 3.1.1",
		"adapter-static 4.0.1":            "SvelteKit) names @sveltejs/adapter-static 4.0.1; console/package.json pins 4.0.0",
		"kit 3.1.1, adapter-static 4.0.0": "",
		"SvelteKit without a version":     "",
	} {
		t.Run(detail, func(t *testing.T) {
			sources := consoleCreditsFixture(detail)
			sources.Credits.Entries[0].Packages = []string{"npm:@sveltejs"}
			err := CheckCredits(playerCreditsRow([]string{"react 19.0.0"}), renderFixtureSources(), sources)
			if want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v; want %q", err, want)
			}
		})
	}
}

// Negative: a named console version must equal the manifest pin, including every occurrence.
func TestCreditsRejectStaleConsoleVersions(t *testing.T) {
	for detail, want := range map[string]string{
		"kit 3.1.0, adapter-static 4.0.0": "SvelteKit) names @sveltejs/kit 3.1.0; console/package.json pins 3.1.1",
		"kit 3.1.1, adapter-static 4.0.1": "SvelteKit) names @sveltejs/adapter-static 4.0.1; console/package.json pins 4.0.0",
		"@sveltejs/kit 3.1.0":             "SvelteKit) names @sveltejs/kit 3.1.0; console/package.json pins 3.1.1",
		"kit 3.1.1, kit 3.1.0":            "SvelteKit) names @sveltejs/kit 3.1.0; console/package.json pins 3.1.1",
		"kit 3.1.10":                      "SvelteKit) names @sveltejs/kit 3.1.10; console/package.json pins 3.1.1",
		"kit 3.1.1-rc.1":                  "SvelteKit) names @sveltejs/kit 3.1.1-rc.1; console/package.json pins 3.1.1",
		"kit 3.1.1+build":                 "SvelteKit) names @sveltejs/kit 3.1.1+build; console/package.json pins 3.1.1",
		"kit 3.1.0.":                      "SvelteKit) names @sveltejs/kit 3.1.0; console/package.json pins 3.1.1",
		"kit 3.1":                         "SvelteKit) names @sveltejs/kit 3.1; console/package.json pins 3.1.1",
		"kit 3.1.01":                      "SvelteKit) names @sveltejs/kit 3.1.01; console/package.json pins 3.1.1",
		"kit ^3.1.1":                      "SvelteKit) names @sveltejs/kit ^3.1.1; console/package.json pins 3.1.1",
		"kit 3.1.1 || 3.1.0":              "SvelteKit) names @sveltejs/kit 3.1.1 || 3.1.0; console/package.json pins 3.1.1",
		"kit 3.1.0 || 3.1.1":              "SvelteKit) names @sveltejs/kit 3.1.0 || 3.1.1; console/package.json pins 3.1.1",
	} {
		t.Run(detail, func(t *testing.T) {
			err := CheckCredits(playerCreditsRow([]string{"react 19.0.0"}), renderFixtureSources(), consoleCreditsFixture(detail))
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("console version mismatch accepted or unnamed: %v; want %q", err, want)
			}
		})
	}
}

// Negative: separated comparators remain version claims even when their bound equals the pin.
func TestCreditsRejectSeparatedConsoleComparators(t *testing.T) {
	for _, claim := range []string{">= 3.1.0", "<= 3.1.1", "> 3.1.1", "< 3.1.1", "= 3.1.1", "^ 3.1.1", "~ 3.1.1"} {
		for _, name := range []string{"kit", "@sveltejs/kit"} {
			t.Run(name+" "+claim, func(t *testing.T) {
				err := CheckCredits(playerCreditsRow([]string{"react 19.0.0"}), renderFixtureSources(), consoleCreditsFixture(name+" "+claim))
				want := "SvelteKit) names @sveltejs/kit " + claim + "; console/package.json pins 3.1.1"
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("separated comparator accepted or unnamed: %v; want %q", err, want)
				}
			})
		}
	}
}

// Positive and boundary: matching pins, prose without versions, and unrelated terms pass.
func TestCreditsAcceptMatchingOrUnversionedConsoleDetails(t *testing.T) {
	for _, detail := range []string{
		"kit 3.1.1", "kit", "kit ^", "kit >=", "kit >= current", "other-kit >= 3.1.0", "@other/kit ^ 3.1.1",
		"kit 3.1.1, adapter-static 4.0.0", "kit 3.1.1, other 2.0.0", "kit 3.1.1, 7zip-bin 5.2.0",
		"@sveltejs/kit 3.1.1; @sveltejs/adapter-static 4.0.0",
		"`kit` `3.1.1`, (adapter-static 4.0.0)", "", "framework and static adapter",
		"other-kit 3.1.0, @other/kit 3.1.0, unrelated 1.0.0",
	} {
		t.Run(detail, func(t *testing.T) {
			if err := CheckCredits(playerCreditsRow([]string{"react 19.0.0"}), renderFixtureSources(), consoleCreditsFixture(detail)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Boundary: bare versions, every dependency group, invalid manifests and rows outside the
// console retain the same comparison rules; a version claim for an absent package fails.
func TestCreditsConsoleManifestBoundaries(t *testing.T) {
	for name, test := range map[string]struct {
		paths    []string
		packages []string
		detail   string
		manifest string
		want     string
	}{
		"bare match":          {[]string{consoleManifestFile}, []string{"npm:svelte"}, "5.57.3", `{"dependencies":{"svelte":"5.57.3"}}`, ""},
		"two named pins":      {[]string{consoleManifestFile}, []string{"npm:@sveltejs/kit", "npm:other"}, "kit 3.1.1, other 2.0.0", `{"dependencies":{"@sveltejs/kit":"3.1.1","other":"2.0.0"}}`, ""},
		"digit package match": {[]string{consoleManifestFile}, []string{"npm:@sveltejs/kit", "npm:7zip-bin"}, "kit 3.1.1, 7zip-bin 5.2.0", `{"dependencies":{"@sveltejs/kit":"3.1.1","7zip-bin":"5.2.0"}}`, ""},
		"digit package stale": {[]string{consoleManifestFile}, []string{"npm:@sveltejs/kit", "npm:7zip-bin"}, "kit 3.1.1, 7zip-bin 5.2.1", `{"dependencies":{"@sveltejs/kit":"3.1.1","7zip-bin":"5.2.0"}}`, "names 7zip-bin 5.2.1; console/package.json pins 5.2.0"},
		"digit package range": {[]string{consoleManifestFile}, []string{"npm:7zip-bin"}, "7zip-bin 5.2.0 || 5.1.0", `{"dependencies":{"7zip-bin":"5.2.0"}}`, "names 7zip-bin 5.2.0 || 5.1.0; console/package.json pins 5.2.0"},
		"bare major":          {[]string{consoleManifestFile}, []string{"npm:svelte"}, "svelte 5", `{"dependencies":{"svelte":"5.57.3"}}`, "names svelte 5;"},
		"bare stale":          {[]string{consoleManifestFile}, []string{"npm:svelte"}, "5.57.2", `{"dependencies":{"svelte":"5.57.3"}}`, "names svelte 5.57.2; console/package.json pins 5.57.3"},
		"second path":         {[]string{"other/package.json", consoleManifestFile}, []string{"npm:svelte"}, "svelte 5.57.2", `{"dependencies":{"svelte":"5.57.3"}}`, "pins 5.57.3"},
		"peer pin":            {[]string{consoleManifestFile}, []string{"npm:svelte"}, "svelte 5.57.3", `{"peerDependencies":{"svelte":"5.57.3"}}`, ""},
		"optional pin":        {[]string{consoleManifestFile}, []string{"npm:svelte"}, "svelte 5.57.2", `{"optionalDependencies":{"svelte":"5.57.3"}}`, "pins 5.57.3"},
		"absent pin":          {[]string{consoleManifestFile}, []string{"npm:svelte"}, "svelte 5.57.3", `{}`, "has no pin for that package"},
		"other path":          {[]string{"other/package.json"}, []string{"npm:svelte"}, "svelte 5.57.2", "{", ""},
		"other ecosystem":     {[]string{consoleManifestFile}, []string{"pypi:svelte"}, "svelte 5.57.2", `{}`, ""},
		"other package":       {[]string{consoleManifestFile}, []string{"npm:svelte"}, "kit 3.1.0", `{}`, ""},
		"bad JSON":            {[]string{consoleManifestFile}, nil, "", "{", "parse console/package.json for credits"},
		"repeated key":        {[]string{consoleManifestFile}, nil, "", `{"dependencies":{},"dependencies":{}}`, "parse console/package.json for credits"},
		"URL only":            {[]string{consoleManifestFile}, []string{"npm:svelte"}, "[framework](https://example.test/svelte 5.57.2)", `{}`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			sources := consoleCreditsFixture(test.detail)
			sources.Credits.Entries[0].Paths = test.paths
			sources.Credits.Entries[0].Packages = test.packages
			sources.PathTexts[consoleManifestFile] = test.manifest
			sources.ManifestTexts[consoleManifestFile] = test.manifest
			err := CheckCredits(playerCreditsRow([]string{"react 19.0.0"}), renderFixtureSources(), sources)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v; want %q", err, test.want)
			}
		})
	}
}

// The checkout's credits compare against the console manifest on every package test run.
func TestCreditsNameConsolePackagesAtManifestVersions(t *testing.T) {
	listed, err := ReadCredits(context.Background(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	sources := UpstreamCreditSources{
		Credits: listed, ManifestTexts: map[string]string{consoleManifestFile: string(readRepoFile(t, consoleManifestFile))},
	}
	if err := CheckCredits(string(readRepoFile(t, AcknowledgementsFile)), checkoutNoticeSources(t), sources); err != nil {
		t.Fatal(err)
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
