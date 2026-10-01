// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
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
		if got := namesTerm(text, "react 19.3.0"); got != want {
			t.Errorf("namesTerm(%q) = %v, want %v", text, got, want)
		}
	}
	linkOnly := "| [react 19.3.0](https://example.test/react-dom 19.3.0 scheduler 0.28.0) | `tools/figures/dist/player.js` | MIT |"
	if err := CheckCredits(linkOnly, checkoutNoticeSources(t)); err == nil {
		t.Fatal("package names inside a link URL counted")
	}
}

// checkoutCreditsSection returns the section of the committed credits page that heading opens,
// lower-cased and joined, and fails the test when the page has no such section.
func checkoutCreditsSection(t *testing.T, heading string) string {
	t.Helper()
	section, _, err := creditsSection(string(readRepoFile(t, AcknowledgementsFile)), heading)
	if err != nil {
		t.Fatal(err)
	}
	if len(section) == 0 {
		t.Fatalf("%s has no %q section", AcknowledgementsFile, heading)
	}
	return strings.ToLower(strings.Join(section, "\n"))
}

// workflowActionFiles are the globs, relative to the checkout, of the files whose uses: lines
// the GitHub Actions section answers: the workflows, the composite actions, and the CI templates
// adoption writes.
var workflowActionFiles = []string{
	".github/workflows/*.yml", ".github/workflows/*.yaml",
	".github/actions/*/action.yml", ".github/actions/*/action.yaml",
	templates.Directory + "/" + templates.Pattern,
}

// remoteActionRepository returns the owner/repository a uses: value runs, lower-cased, or
// false for a local action or a Docker reference.
func remoteActionRepository(ref string) (string, bool) {
	action, _, _ := strings.Cut(ref, "@")
	parts := strings.Split(action, "/")
	if strings.HasPrefix(action, "./") || strings.HasPrefix(action, "docker://") || len(parts) < 2 {
		return "", false
	}
	return strings.ToLower(parts[0] + "/" + parts[1]), true
}

// Every remote action a workflow, a composite action or a CI template uses has a row in the
// GitHub Actions section that links its repository.
func TestCreditsNameEveryWorkflowAction(t *testing.T) {
	section := checkoutCreditsSection(t, "## GitHub Actions")
	root := filepath.Join("..", "..")
	used := map[string]string{}
	for _, pattern := range workflowActionFiles {
		paths, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatal(err)
			}
			_, uses, err := util.ScanActionUses(string(readRepoFile(t, filepath.ToSlash(rel))), maxNoticeLines)
			if err != nil {
				t.Fatalf("%s: %v", rel, err)
			}
			for _, use := range uses {
				if repository, remote := remoteActionRepository(use.Ref); remote {
					used[repository] = filepath.ToSlash(rel)
				}
			}
		}
	}
	if len(used) < 10 {
		t.Fatalf("found %d remote actions, fewer than the workflows use; the scan read too little: %v", len(used), used)
	}
	for repository, rel := range used {
		if !strings.Contains(section, "](https://github.com/"+repository+")") {
			t.Errorf("%s uses %s, and the GitHub Actions section of %s links no https://github.com/%s", rel, repository, AcknowledgementsFile, repository)
		}
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

// Every package a documentation preset installs directly, from its requirements.in or its
// package.json, is named in a code span in the Documentation presets section.
func TestCreditsNameEveryPresetPackage(t *testing.T) {
	section := checkoutCreditsSection(t, "## Documentation presets")
	var names []string
	for _, line := range strings.Split(string(readRepoFile(t, "docs/presets/mkdocs/requirements.in")), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, _, _ := strings.Cut(line, "==")
		names = append(names, strings.TrimSpace(name))
	}
	var manifest struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(readRepoFile(t, "docs/presets/starlight/package.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	for name := range manifest.Dependencies {
		names = append(names, name)
	}
	for name := range manifest.DevDependencies {
		names = append(names, name)
	}
	if len(names) < 6 {
		t.Fatalf("read %d preset packages, fewer than the presets pin: %v", len(names), names)
	}
	slices.Sort(names)
	for _, name := range names {
		if !strings.Contains(section, "`"+strings.ToLower(name)+"`") {
			t.Errorf("a documentation preset installs %s, and the Documentation presets section of %s names no `%s`", name, AcknowledgementsFile, name)
		}
	}
}
