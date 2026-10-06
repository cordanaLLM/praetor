// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

const goToolsAutoUpdate = "go.toolsManagement.autoUpdate"

// selectedGo is a pinned catalog's Go feature, with the versions an adopter pins.
var selectedGo = config.DevContainerFeature{Ref: GoFeatureRef, Options: map[string]interface{}{"version": "1.27.1", "golangciLintVersion": "2.13.2"}}

// synthesizeSelected synthesizes a framework container from selected catalog features.
func synthesizeSelected(t *testing.T, selected ...config.DevContainerFeature) *DevContainer {
	t.Helper()
	dc, err := SynthesizeWithFeatures(&config.Manifest{Profiles: []string{"framework"}}, selected)
	if err != nil {
		t.Fatal(err)
	}
	return dc
}

// Positive: a container carrying Go tooling sets tool auto-update to false explicitly, on the
// pinned-catalog path and on the legacy profile path, so a pinned Go feature stays pinned (#332).
func TestGoToolsAutoUpdateIsOff(t *testing.T) {
	legacy, err := SynthesizeFromProfiles("app", []string{"framework"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, dc := range map[string]*DevContainer{"selected": synthesizeSelected(t, selectedGo), "legacy": legacy} {
		value, present := dc.Customizations.VSCode.Settings[goToolsAutoUpdate]
		if !present || value != false {
			t.Fatalf("%s: %s = %#v (present %v); want an explicit false", name, goToolsAutoUpdate, value, present)
		}
	}
}

// Boundary: a container without a Go toolchain carries no Go tool setting at all.
func TestGoToolsAutoUpdateAbsentWithoutGo(t *testing.T) {
	native, err := SynthesizeFromProfiles("gpu", []string{NativeGPUProfile}, nil)
	if err != nil {
		t.Fatal(err)
	}
	commonUtils := config.DevContainerFeature{Ref: CommonUtilsFeature, Options: map[string]interface{}{}}
	for name, dc := range map[string]*DevContainer{"native": native, "no Go feature": synthesizeSelected(t, commonUtils)} {
		if value, present := dc.Customizations.VSCode.Settings[goToolsAutoUpdate]; present {
			t.Fatalf("%s: %s = %#v without a Go toolchain", name, goToolsAutoUpdate, value)
		}
	}
}

// Negative: the generated bundle verifies with auto-update off, and verification refuses it once
// the value is flipped back to true, so the pin is held by the verifier as well as the generator.
func TestVerifyRefusesGoToolsAutoUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".devcontainer", "devcontainer.json")
	if err := WriteBundle(t.Context(), path, preparedBootstrap(t), false); err != nil {
		t.Fatal(err)
	}
	expected := mustBaseContainer(t)
	if err := Verify(t.Context(), path, expected); err != nil {
		t.Fatalf("generated bundle does not verify: %v", err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	off := []byte(`"` + goToolsAutoUpdate + `": false`)
	if bytes.Count(original, off) != 1 {
		t.Fatalf("generated config lacks %s:\n%s", off, original)
	}
	if err := os.WriteFile(path, bytes.Replace(original, off, []byte(`"`+goToolsAutoUpdate+`": true`), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), path, expected); err == nil {
		t.Fatalf("verification accepted %s flipped to true", goToolsAutoUpdate)
	}
}

// Positive: a selected common-utils feature keeps every hardening default it does not set, and a
// key it does set wins, for the shipped reference, a digest pin and a mirror alike (#334).
func TestSelectedCommonUtilsMergesOverHardeningDefaults(t *testing.T) {
	digest := "@sha256:" + strings.Repeat("c", 64)
	for _, tc := range []struct {
		name, ref string
		options   map[string]interface{}
		want      map[string]interface{}
	}{
		{"no options", CommonUtilsFeature, map[string]interface{}{}, map[string]interface{}{"installZsh": false, "upgradePackages": true}},
		{"nil options", CommonUtilsFeature, nil, map[string]interface{}{"installZsh": false, "upgradePackages": true}},
		{"explicit upgrade off wins", CommonUtilsFeature, map[string]interface{}{"upgradePackages": false}, map[string]interface{}{"installZsh": false, "upgradePackages": false}},
		{"other keys kept beside defaults", CommonUtilsFeature, map[string]interface{}{"installZsh": true, "username": "dev"}, map[string]interface{}{"installZsh": true, "upgradePackages": true, "username": "dev"}},
		{"digest pin", "ghcr.io/devcontainers/features/common-utils" + digest, map[string]interface{}{}, map[string]interface{}{"installZsh": false, "upgradePackages": true}},
		{"mirror", "registry.test:5000/features/common-utils:2", map[string]interface{}{}, map[string]interface{}{"installZsh": false, "upgradePackages": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := maps.Clone(tc.options)
			dc := synthesizeSelected(t, config.DevContainerFeature{Ref: tc.ref, Options: tc.options})
			if got := dc.Features[tc.ref]; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("common-utils options = %#v; want %#v", got, tc.want)
			}
			if !reflect.DeepEqual(tc.options, before) {
				t.Fatalf("synthesis modified the catalog's options: %#v, was %#v", tc.options, before)
			}
		})
	}
}

// Boundary: the defaults reach common-utils only. Another feature, one whose name merely starts
// with common-utils included, keeps its options as given, and a selection without common-utils
// gains no common-utils feature.
func TestCommonUtilsDefaultsStayOnCommonUtils(t *testing.T) {
	lookalike := config.DevContainerFeature{Ref: "ghcr.io/example/features/common-utils-extra:1", Options: map[string]interface{}{}}
	node := config.DevContainerFeature{Ref: "ghcr.io/devcontainers/features/node:2", Options: map[string]interface{}{"version": "24"}}
	dc := synthesizeSelected(t, lookalike, node, selectedGo)
	if len(dc.Features) != 3 {
		t.Fatalf("selection of three features synthesized %#v", dc.Features)
	}
	for _, feature := range []config.DevContainerFeature{lookalike, node, selectedGo} {
		if got := dc.Features[feature.Ref]; !reflect.DeepEqual(got, feature.Options) {
			t.Fatalf("%s options = %#v; want them as selected, %#v", feature.Ref, got, feature.Options)
		}
	}
}

// Positive, end to end: this repository declares the framework profile and the security:high
// facet, whose shipped catalogs select common-utils with no options, the reproduction of #334.
// The synthesized feature upgrades the OS packages.
func TestShippedCatalogCommonUtilsUpgradesPackages(t *testing.T) {
	policy, err := config.LoadEffectivePolicyContext(t.Context(), config.EffectiveOptions{Root: filepath.Join("..", "..")})
	if err != nil {
		t.Fatal(err)
	}
	features, err := config.ResolveDevContainerFeatures(t.Context(), policy)
	if err != nil {
		t.Fatal(err)
	}
	dc, err := SynthesizeWithFeatures(policy.Manifest, features)
	if err != nil {
		t.Fatal(err)
	}
	options, ok := dc.Features[CommonUtilsFeature].(map[string]interface{})
	if !ok || options["upgradePackages"] != true || options["installZsh"] != false {
		t.Fatalf("shipped catalog common-utils options = %#v; want upgradePackages true and installZsh false", dc.Features[CommonUtilsFeature])
	}
}
