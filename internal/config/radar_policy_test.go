// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// radarManifest is a manifest declaring radar.registry as registry.
func radarManifest(registry string) string {
	return "version: 1\nradar:\n  registry: \"" + registry + "\"\n"
}

// Positive: a declared radar.registry loads, and RepositoryRadarRegistry returns it and reports
// the radar declared.
func TestRadarPolicy_Positive_DeclaredRegistry(t *testing.T) {
	path := writeManifest(t, radarManifest("ops/radar.yaml"))
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if got := m.Radar.RegistryPath(); got != "ops/radar.yaml" {
		t.Fatalf("RegistryPath = %q", got)
	}
	got, declared, err := RepositoryRadarRegistry(filepath.Dir(path))
	if err != nil || !declared || got != "ops/radar.yaml" {
		t.Fatalf("RepositoryRadarRegistry = %q, %v, %v", got, declared, err)
	}
}

// Negative: a registry path that is absolute, escapes the repository, uses a backslash or is not
// clean is refused with the key named; an unknown key under radar, an archetype key included,
// fails the strict decode; RepositoryRadarRegistry reports a manifest that does not load instead
// of answering "no radar".
func TestRadarPolicy_Negative_BadDeclarationsFail(t *testing.T) {
	// The manifest double-quotes the value, so `\\` decodes to the one backslash under test.
	for _, registry := range []string{"/etc/radar.yaml", "../radar.yaml", `ops\\radar.yaml`, "ops/./radar.yaml", "."} {
		if _, err := LoadManifest(writeManifest(t, radarManifest(registry))); err == nil ||
			!strings.Contains(err.Error(), "radar.registry") {
			t.Errorf("registry %q: err = %v, want radar.registry refused", registry, err)
		}
	}
	for _, body := range []string{
		"version: 1\nradar:\n  sources: []\n",
		"version: 1\nradar:\n  archetype: research\n",
	} {
		unknown := writeManifest(t, body)
		if _, err := LoadManifest(unknown); err == nil {
			t.Errorf("%q loaded", body)
		}
		if got, declared, err := RepositoryRadarRegistry(filepath.Dir(unknown)); err == nil || declared || got != "" {
			t.Errorf("RepositoryRadarRegistry of %q = %q, %v, %v; want an error", body, got, declared, err)
		}
	}
}

// Boundary: no manifest and a manifest without the section declare no radar; an empty section
// and an empty registry declare one at DefaultRadarRegistryPath, as a nil policy names; a path of
// exactly maxRepositoryPath bytes loads and one byte more is refused.
func TestRadarPolicy_Boundary_DefaultsAndLengthLimit(t *testing.T) {
	var none *RadarPolicy
	if none.RegistryPath() != DefaultRadarRegistryPath {
		t.Errorf("nil policy names %q", none.RegistryPath())
	}
	if got, declared, err := RepositoryRadarRegistry(t.TempDir()); err != nil || declared || got != "" {
		t.Errorf("no manifest: %q, %v, %v", got, declared, err)
	}
	if got, declared, err := RepositoryRadarRegistry(filepath.Dir(writeManifest(t, "version: 1\n"))); err != nil || declared || got != "" {
		t.Errorf("no section: %q, %v, %v", got, declared, err)
	}
	for _, body := range []string{"version: 1\nradar: {}\n", radarManifest("")} {
		got, declared, err := RepositoryRadarRegistry(filepath.Dir(writeManifest(t, body)))
		if err != nil || !declared || got != DefaultRadarRegistryPath {
			t.Errorf("%q: %q, %v, %v; want the default declared", body, got, declared, err)
		}
	}
	longest := "ops/" + strings.Repeat("a", maxRepositoryPath-len("ops/.yaml")) + ".yaml"
	if _, err := LoadManifest(writeManifest(t, radarManifest(longest))); err != nil {
		t.Errorf("%d-byte path refused: %v", len(longest), err)
	}
	if _, err := LoadManifest(writeManifest(t, radarManifest("a"+longest))); err == nil {
		t.Errorf("%d-byte path loaded", len(longest)+1)
	}
}
