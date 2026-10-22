// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

package devcontainer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func writeModFixture(t testing.TB, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Positive: a catalog Go feature with no options gets installGoTools false and
// the version read from go.mod (both on catalog-selected and profile-derived paths).
func TestSelectedGoFeature_Positive(t *testing.T) {
	root := t.TempDir()
	writeModFixture(t, root, "module example.com/pos\n\ngo 1.25\n\ntoolchain go1.27.3\n")

	selected := []config.DevContainerFeature{
		{Ref: GoFeatureRef, Options: map[string]interface{}{}},
	}
	dc, err := SynthesizeWithFeatures(&config.Manifest{Profiles: []string{"framework"}}, selected, root)
	if err != nil {
		t.Fatalf("SynthesizeWithFeatures failed: %v", err)
	}
	opts := goFeatureOptions(t, dc.Features)
	if opts["installGoTools"] != false || opts["version"] != "1.27.3" {
		t.Fatalf("catalog options = %#v, want installGoTools false and version 1.27.3", opts)
	}

	legacy, err := SynthesizeFromProfiles("test", []string{"framework"}, nil, root)
	if err != nil {
		t.Fatalf("SynthesizeFromProfiles failed: %v", err)
	}
	legOpts, ok := legacy.Features[GoFeatureRef].(map[string]interface{})
	if !ok {
		t.Fatalf("profile-derived Go feature missing or not a map: %#v", legacy.Features[GoFeatureRef])
	}
	if legOpts["installGoTools"] != false || legOpts["version"] != "1.27.3" {
		t.Fatalf("profile-derived options = %#v, want installGoTools false and version 1.27.3", legOpts)
	}
}

// Negative: a pinned catalog selecting the Go feature with no options must not drop
// installGoTools false or the version resolved from go.mod. Without the fix, options
// are dropped as empty/unpinned defaults.
func TestSelectedGoFeature_Negative_OptionDroppedFails(t *testing.T) {
	root := t.TempDir()
	writeModFixture(t, root, "module example.com/neg\n\ngo 1.25\n\ntoolchain go1.27.3\n")

	selected := []config.DevContainerFeature{
		{Ref: GoFeatureRef, Options: map[string]interface{}{}},
	}
	dc, err := SynthesizeWithFeatures(&config.Manifest{Profiles: []string{"framework"}}, selected, root)
	if err != nil {
		t.Fatalf("SynthesizeWithFeatures failed: %v", err)
	}

	opts := goFeatureOptions(t, dc.Features)
	val, exists := opts["installGoTools"]
	if !exists || val != false {
		t.Fatalf("installGoTools option dropped or not false: got %#v, want false", val)
	}
	ver, exists := opts["version"]
	if !exists || ver != "1.27.3" {
		t.Fatalf("version option dropped or not read from go.mod: got %#v, want 1.27.3", ver)
	}
}

// Boundary: catalog sets installGoTools true explicitly (kept); no go.mod falls back
// to DefaultGoVersion with installGoTools false.
func TestSelectedGoFeature_Boundary_ExplicitCatalogOptionsAndMissingMod(t *testing.T) {
	root := t.TempDir()
	writeModFixture(t, root, "module example.com/b1\n\ngo 1.27\n\ntoolchain go1.27.3\n")
	selected := []config.DevContainerFeature{
		{Ref: GoFeatureRef, Options: map[string]interface{}{"installGoTools": true}},
	}
	dc, err := SynthesizeWithFeatures(&config.Manifest{Profiles: []string{"framework"}}, selected, root)
	if err != nil {
		t.Fatalf("SynthesizeWithFeatures: %v", err)
	}
	opts := goFeatureOptions(t, dc.Features)
	if opts["installGoTools"] != true || opts["version"] != "1.27.3" {
		t.Fatalf("explicit installGoTools true not kept: %#v", opts)
	}

	emptyDir := t.TempDir()
	dcEmpty, err := SynthesizeWithFeatures(&config.Manifest{Profiles: []string{"framework"}}, []config.DevContainerFeature{{Ref: GoFeatureRef}}, emptyDir)
	if err != nil {
		t.Fatalf("SynthesizeWithFeatures emptyDir: %v", err)
	}
	optsEmpty := goFeatureOptions(t, dcEmpty.Features)
	if optsEmpty["version"] != DefaultGoVersion || optsEmpty["installGoTools"] != false {
		t.Fatalf("empty dir options = %#v, want version %s and installGoTools false", optsEmpty, DefaultGoVersion)
	}
}

// Boundary: toolchain line beats go line; go line used when toolchain line is absent.
func TestSelectedGoFeature_Boundary_ToolchainBeatsGoLine(t *testing.T) {
	tcBeatDir := t.TempDir()
	writeModFixture(t, tcBeatDir, "module example.com/b3\n\ngo 1.22\n\ntoolchain go1.27.4\n")
	dcTc, err := SynthesizeWithFeatures(&config.Manifest{Profiles: []string{"framework"}}, []config.DevContainerFeature{{Ref: GoFeatureRef}}, tcBeatDir)
	if err != nil {
		t.Fatalf("SynthesizeWithFeatures tcBeat: %v", err)
	}
	optsTc := goFeatureOptions(t, dcTc.Features)
	if optsTc["version"] != "1.27.4" {
		t.Fatalf("toolchain line did not beat go line: got %#v, want 1.27.4", optsTc["version"])
	}

	goOnlyDir := t.TempDir()
	writeModFixture(t, goOnlyDir, "module example.com/b4\n\ngo 1.26\n")
	dcGo, err := SynthesizeWithFeatures(&config.Manifest{Profiles: []string{"framework"}}, []config.DevContainerFeature{{Ref: GoFeatureRef}}, goOnlyDir)
	if err != nil {
		t.Fatalf("SynthesizeWithFeatures goOnly: %v", err)
	}
	optsGo := goFeatureOptions(t, dcGo.Features)
	if optsGo["version"] != "1.26" {
		t.Fatalf("go line version = %#v, want 1.26", optsGo["version"])
	}
}

// goFeatureOptions returns the Go feature's option map, failing the test when the feature is missing or not a map.
func goFeatureOptions(t testing.TB, features map[string]interface{}) map[string]interface{} {
	t.Helper()
	opts, ok := features[GoFeatureRef].(map[string]interface{})
	if !ok {
		t.Fatalf("Go feature missing or not a map: %#v", features[GoFeatureRef])
	}
	return opts
}
