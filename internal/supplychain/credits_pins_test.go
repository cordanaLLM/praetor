// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/nodemanifest"
)

// Negative / planted case: package.json pins x@2.0.1 and credits.yaml records x@2.0.0;
// the check fails naming x and both versions; matching versions pass.
func TestCheckCreditPinsPlantedMismatch(t *testing.T) {
	credits := Credits{
		Entries: []CreditEntry{
			{
				Name:     "x",
				URL:      "https://example.test/x",
				Detail:   "2.0.0",
				Section:  "shipped",
				Kind:     "dependency",
				Relation: "integrated",
				License:  "MIT",
				Use:      "example",
				Paths:    []string{"package.json"},
				Packages: []string{"npm:x"},
			},
		},
	}
	mismatchManifest := []byte(`{
		"name": "fixture",
		"dependencies": {
			"x": "2.0.1"
		}
	}`)
	err := CheckCreditPins(credits, map[string][]byte{"package.json": mismatchManifest})
	if err == nil {
		t.Fatal("expected failure on version mismatch, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "x") || !strings.Contains(msg, "2.0.1") || !strings.Contains(msg, "2.0.0") {
		t.Fatalf("error %q does not name package x and both versions (2.0.1 and 2.0.0)", msg)
	}

	matchingManifest := []byte(`{
		"name": "fixture",
		"dependencies": {
			"x": "2.0.0"
		}
	}`)
	if err := CheckCreditPins(credits, map[string][]byte{"package.json": matchingManifest}); err != nil {
		t.Fatalf("matching version failed: %v", err)
	}
}

// Boundary cases: prefix stripping, scoped subpackages, entries without versions, missing manifests.
func TestCheckCreditPinsBoundary(t *testing.T) {
	credits := Credits{
		Entries: []CreditEntry{
			{
				Name:     "tool",
				URL:      "https://example.test/tool",
				Detail:   "(`tool`)",
				Section:  "development",
				Kind:     "tool",
				Relation: "used by CI",
				License:  "MIT",
				Use:      "tooling",
				Paths:    []string{"package.json"},
				Packages: []string{"npm:tool"},
			},
			{
				Name:     "scoped",
				URL:      "https://example.test/scoped",
				Detail:   "core 1.2.3, extra 4.5.6",
				Section:  "shipped",
				Kind:     "dependency",
				Relation: "integrated",
				License:  "MIT",
				Use:      "scoped package",
				Paths:    []string{"package.json"},
				Packages: []string{"npm:@scope/core", "npm:@scope/extra"},
			},
		},
	}
	manifest := []byte(`{
		"name": "boundary-fixture",
		"dependencies": {
			"tool": "9.9.9",
			"@scope/core": "^1.2.3",
			"@scope/extra": "4.5.6"
		}
	}`)
	if err := CheckCreditPins(credits, map[string][]byte{"package.json": manifest}); err != nil {
		t.Fatalf("boundary case failed: %v", err)
	}

	mismatchExtra := []byte(`{
		"name": "boundary-fixture",
		"dependencies": {
			"@scope/core": "1.2.3",
			"@scope/extra": "4.5.7"
		}
	}`)
	err := CheckCreditPins(credits, map[string][]byte{"package.json": mismatchExtra})
	if err == nil || !strings.Contains(err.Error(), "4.5.7") || !strings.Contains(err.Error(), "4.5.6") {
		t.Fatalf("scoped mismatch not detected: %v", err)
	}
}

// Positive / checkout check: all package.json pins in the repository match docs/credits.yaml.
func TestCreditPinsMatchManifests(t *testing.T) {
	root := filepath.Join("..", "..")
	credits, err := ReadCredits(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	manifests, err := ReadCreditManifests(context.Background(), root, credits)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) == 0 {
		t.Fatal("read 0 manifests; want at least 1")
	}
	if err := CheckCreditPins(credits, manifests); err != nil {
		t.Fatal(err)
	}
}

// Every dependency group is checked even when later groups repeat a matching pin.
func TestCheckCreditPinsDependencyGroups(t *testing.T) {
	credits := Credits{Entries: []CreditEntry{{
		Name: "x", Detail: "2.0.0", Paths: []string{"package.json"}, Packages: []string{"npm:x"},
	}}}
	groups := []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies"}
	for _, group := range groups {
		t.Run(group, func(t *testing.T) {
			manifest := make(map[string]map[string]string)
			for _, name := range groups {
				manifest[name] = map[string]string{"x": "2.0.0"}
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckCreditPins(credits, map[string][]byte{"package.json": data}); err != nil {
				t.Fatalf("matching declarations: %v", err)
			}
			manifest[group]["x"] = "2.0.1"
			data, err = json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			err = CheckCreditPins(credits, map[string][]byte{"package.json": data})
			requireCreditPinError(t, err, "package.json", "x", "2.0.1", "2.0.0")
		})
	}
}

// Boundary: missing packages, empty groups, matching prefixes, and absent manifests are accepted.
func TestCheckCreditPinsDeclarationBoundary(t *testing.T) {
	credits := Credits{Entries: []CreditEntry{{
		Name: "x", Detail: "2.0.0", Paths: []string{"package.json"}, Packages: []string{"npm:x"},
	}}}
	for _, text := range []string{
		`{}`,
		`{"dependencies":{},"devDependencies":{"other":"2.0.1"}}`,
		`{"dependencies":{"x":"^2.0.0"},"peerDependencies":{"x":"~2.0.0"}}`,
	} {
		if err := CheckCreditPins(credits, map[string][]byte{"package.json": []byte(text)}); err != nil {
			t.Errorf("manifest %s: %v", text, err)
		}
	}
	if err := CheckCreditPins(credits, nil); err != nil {
		t.Fatalf("absent manifest: %v", err)
	}
}

// Multiple packages need resolvable versions; versionless and shared-version details remain valid.
func TestCheckCreditPinsVersionAssociations(t *testing.T) {
	for _, tc := range []struct {
		name, detail, core, adapter, want string
	}{
		{"named", "core 1.2.3, adapter-static 4.5.6", "1.2.3", "4.5.6", ""},
		{"misspelled adapter", "core 1.2.3, adapter 4.5.6", "1.2.3", "4.5.6", "@scope/adapter-static"},
		{"unchecked adapter", "core 1.2.3, adapter 4.5.6", "1.2.3", "99.0.0", "@scope/adapter-static"},
		{"misspelled core", "cr 1.2.3, adapter-static 4.5.6", "1.2.3", "4.5.6", "@scope/core"},
		{"shared version", "1.2.3", "1.2.3", "1.2.3", ""},
		{"equal versions", "core 1.2.3, adapter 1.2.3", "1.2.3", "1.2.3", ""},
		{"versionless", "development tools", "9.9.9", "9.9.9", ""},
		{"empty detail", "", "9.9.9", "9.9.9", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credits := Credits{Entries: []CreditEntry{{
				Name: "toolset", Detail: tc.detail, Paths: []string{"package.json"},
				Packages: []string{"npm:@scope/core", "npm:@scope/adapter-static"},
			}}}
			data, err := json.Marshal(nodemanifest.Manifest{Dependencies: map[string]string{
				"@scope/core": tc.core, "@scope/adapter-static": tc.adapter,
			}})
			if err != nil {
				t.Fatal(err)
			}
			err = CheckCreditPins(credits, map[string][]byte{"package.json": data})
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			requireCreditPinError(t, err, "ambiguous", "package.json", tc.want, AcknowledgementsList)
		})
	}
}

// requireCreditPinError requires a failure naming every supplied context term.
func requireCreditPinError(t *testing.T, err error, terms ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected credit pin error, got nil")
	}
	for _, term := range terms {
		if !strings.Contains(err.Error(), term) {
			t.Errorf("error %q does not name %q", err, term)
		}
	}
}

// briefPinnedVersion extracts one package's pinned version from the brief text.
func briefPinnedVersion(brief, pkg string) string {
	pattern := "`" + regexp.QuoteMeta(pkg) + "`\\s+(" + semverPattern.String() + ")"
	matches := regexp.MustCompile(pattern).FindStringSubmatch(brief)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// TestConsoleBriefPinsMatchManifest verifies console build instructions name the component pins and UI tag in the committed manifest.
func TestConsoleBriefPinsMatchManifest(t *testing.T) {
	root := filepath.Join("..", "..")
	data, _, err := contextopt.ObserveSnapshotIn(t.Context(), root, "console/package.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := nodemanifest.ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	brief, exists, err := contextopt.ObserveSnapshotIn(t.Context(), root, "console/BRIEF.md")
	if err != nil || !exists {
		t.Fatalf("read console/BRIEF.md: exists=%v, err=%v", exists, err)
	}
	cases := []struct {
		pkg  string
		pins map[string]string
	}{
		{"@sveltesentio/ui", manifest.Dependencies},
		{"@sveltesentio/realtime", manifest.Dependencies},
		{"@sveltejs/kit", manifest.DevDependencies},
		{"svelte", manifest.DevDependencies},
		{"@sveltejs/adapter-static", manifest.DevDependencies},
	}
	for _, tc := range cases {
		version, ok := tc.pins[tc.pkg]
		if !ok || version == "" {
			t.Fatalf("console/package.json has no pin for %s", tc.pkg)
		}
		if got := briefPinnedVersion(string(brief), tc.pkg); got != version {
			t.Errorf("console/BRIEF.md does not name %s at manifest version %s", tc.pkg, version)
		}
	}
	if tag := "`ui-v" + manifest.Dependencies["@sveltesentio/ui"] + "`"; !strings.Contains(string(brief), tag) {
		t.Errorf("console/BRIEF.md does not name manifest UI tag %s", tag)
	}
}

// Positive: named regular and empty manifests are returned once; unrelated paths are ignored.
func TestReadCreditManifestsPositive(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "package.json", "{}\n")
	writeRepoFile(t, root, "empty/package.json", "")
	credits := Credits{Entries: []CreditEntry{
		{Paths: []string{"package.json", "empty/package.json", "README.md", "third_party/package.json"}},
		{Paths: []string{"package.json"}},
	}}
	manifests, err := ReadCreditManifests(t.Context(), root, credits)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 2 || string(manifests["package.json"]) != "{}\n" {
		t.Fatalf("manifests = %q, want exactly the two named files", manifests)
	}
	if data, ok := manifests["empty/package.json"]; !ok || len(data) != 0 {
		t.Fatalf("empty manifest = %q, present=%v", data, ok)
	}
}

// Boundary: absent leaves, absent parent directories, and an empty list yield an empty map.
func TestReadCreditManifestsBoundary(t *testing.T) {
	for _, paths := range [][]string{nil, {"package.json"}, {"absent/package.json"}} {
		root := t.TempDir()
		credits := Credits{Entries: []CreditEntry{{Paths: paths}}}
		manifests, err := ReadCreditManifests(t.Context(), root, credits)
		if err != nil || manifests == nil || len(manifests) != 0 {
			t.Fatalf("paths %v: manifests=%q, err=%v; want empty map", paths, manifests, err)
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatalf("absent read created entries %v, err=%v", entries, err)
		}
	}
}

// Negative: a directory named package.json is refused with the manifest and credits paths.
func TestReadCreditManifestsRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "package.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	credits := Credits{Entries: []CreditEntry{{Paths: []string{"package.json"}}}}
	manifests, err := ReadCreditManifests(t.Context(), root, credits)
	if err == nil || manifests != nil {
		t.Fatalf("rejected read: manifests=%q, err=%v; want nil map and error", manifests, err)
	}
	for _, want := range []string{"read package.json", AcknowledgementsList} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// Boundary: a cancelled read preserves context cancellation in the wrapped error.
func TestReadCreditManifestsCancelled(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "package.json", "{}")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	credits := Credits{Entries: []CreditEntry{{Paths: []string{"package.json"}}}}
	manifests, err := ReadCreditManifests(ctx, root, credits)
	if !errors.Is(err, context.Canceled) || manifests != nil {
		t.Fatalf("cancelled read: manifests=%q, err=%v; want nil map and cancellation", manifests, err)
	}
}

// Positive, negative, and boundary cases for isManifestPath: only exact package.json
// outside third_party directories is an npm manifest.
func TestIsManifestPath(t *testing.T) {
	cases := []struct {
		name string
		rel  string
		want bool
	}{
		{name: "console/package.json", rel: "console/package.json", want: true},
		{name: "root package.json", rel: "package.json", want: true},
		{name: "docs/my-package.json", rel: "docs/my-package.json", want: false},
		{name: "package.json.bak", rel: "package.json.bak", want: false},
		{name: "third_party/x/package.json", rel: "third_party/x/package.json", want: false},
		{name: "empty", rel: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isManifestPath(tc.rel); got != tc.want {
				t.Errorf("isManifestPath(%q) = %v, want %v", tc.rel, got, tc.want)
			}
		})
	}
}
