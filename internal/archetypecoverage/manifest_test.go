// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package archetypecoverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAcceptsEveryState(t *testing.T) {
	manifest, err := Parse([]byte(fixtureManifest))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != "schema" || len(manifest.Fields) != 4 || manifest.Fields[2].RefusedBy != "schema.validate" {
		t.Fatalf("manifest = %+v", manifest)
	}
}

// Each state holds its contract: an entry whose consumers contradict its state is refused
// before any source is read.
func TestParseRefusesContradictoryEntries(t *testing.T) {
	cases := map[string]string{
		"unknown key":              strings.Replace(fixtureManifest, "version: 1", "version: 1\nextra: true", 1),
		"version":                  strings.Replace(fixtureManifest, "version: 1", "version: 2", 1),
		"no schema":                strings.Replace(fixtureManifest, "schema: schema", "schema: \"\"", 1),
		"unconsumed with consumer": strings.Replace(fixtureManifest, "state: reported", "state: unconsumed\n    reason: r", 1),
		"reported that acts":       strings.Replace(fixtureManifest, "kind: reports", "kind: acts", 1),
		"consumed without acts":    strings.Replace(fixtureManifest, "state: reported", "state: consumed", 1),
		"unknown kind":             strings.Replace(fixtureManifest, "kind: reports", "kind: prints", 1),
		"unknown state":            strings.Replace(fixtureManifest, "state: reported", "state: partial", 1),
		"refused without reason":   strings.Replace(fixtureManifest, "reason: repository-only", "reason: \"\"", 1),
		"refused_by elsewhere":     strings.Replace(fixtureManifest, "state: reported", "state: reported\n    refused_by: schema.validate", 1),
		"bad Go field":             strings.Replace(fixtureManifest, "go: [Layer.Level]", "go: [Level]", 1),
		"bad consumer name":        strings.Replace(fixtureManifest, "func: use.Report", "func: Report", 1),
		"consumer without effect":  strings.Replace(fixtureManifest, "effect: returns the level", "effect: \"\"", 1),
		"plumbing without role":    strings.Replace(fixtureManifest, "role: joins two layers", "role: \"\"", 1),
		"bad container":            strings.Replace(fixtureManifest, "containers: [Layer.Box]", "containers: [Box]", 1),
		"two documents":            fixtureManifest + "---\nversion: 1\n",
	}
	for name, manifest := range cases {
		if _, err := Parse([]byte(manifest)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Boundary: a manifest at the byte bound is read; one byte over is refused unread.
func TestParseBoundsTheManifestSize(t *testing.T) {
	padded := fixtureManifest + "#" + strings.Repeat("x", maxManifestBytes-len(fixtureManifest)-2) + "\n"
	if len(padded) != maxManifestBytes {
		t.Fatalf("padding produced %d bytes", len(padded))
	}
	if _, err := Parse([]byte(padded)); err != nil {
		t.Fatalf("manifest at the bound refused: %v", err)
	}
	if _, err := Parse([]byte(padded + "\n")); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("manifest over the bound accepted: %v", err)
	}
}

func TestLoadReadsTheManifestUnderRoot(t *testing.T) {
	root := t.TempDir()
	if _, err := Load(t.Context(), root); err == nil {
		t.Fatal("a missing manifest loaded")
	}
	path := filepath.Join(root, filepath.FromSlash(ManifestFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fixtureManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Load(t.Context(), root)
	if err != nil || len(manifest.Plumbing) != 2 {
		t.Fatalf("manifest = %+v, err = %v", manifest, err)
	}
}
