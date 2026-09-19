// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"path/filepath"
	"testing"
	"time"
)

// TestLoadOperatorSettingsMergesFleetAndWorkstation reuses the four-layer operator fixture
// (operator_sections_test.go) but loads only its fleet and workstation documents, the two
// LoadOperatorSettings reads: a hook call has no repository manifest to carry an
// organization or deployment layer.
func TestLoadOperatorSettingsMergesFleetAndWorkstation(t *testing.T) {
	opts, host := operatorFixture(t)
	selection := SettingsSelection{
		Fleet:       SettingsDocument{Path: opts.FleetPath},
		Workstation: SettingsDocument{Path: opts.WorkstationPath},
	}
	settings, err := LoadOperatorSettings(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := settings.Hooks.CommandPolicy.Deny, []string{`\bexample-org/`}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("fleet deny pattern lost: %v", got)
	}
	wantPython := [][]string{{filepath.Join(host, "python", "python3"), "-X", "utf8"}, {"python3"}}
	if got := settings.Hooks.Python; len(got) != 2 || got[0][0] != wantPython[0][0] || got[1][0] != "python3" {
		t.Errorf("workstation python candidates lost: %v", got)
	}
	if settings.Update.Interval != 30*time.Minute {
		t.Errorf("fleet update.interval lost: %v", settings.Update.Interval)
	}
}

// TestLoadOperatorSettingsRejectsAMalformedDocument mirrors LoadEffectivePolicyContext's own
// negative case for the same document family: a document that fails to decode fails the load
// rather than silently falling back to defaults.
func TestLoadOperatorSettingsRejectsAMalformedDocument(t *testing.T) {
	bad := writePolicyFile(t, t.TempDir(), "fleet.yaml", "hooks: [this is a list, not a mapping]\n")
	if _, err := LoadOperatorSettings(t.Context(), SettingsSelection{Fleet: SettingsDocument{Path: bad}}); err == nil {
		t.Error("malformed fleet document accepted")
	}
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	if _, err := LoadOperatorSettings(t.Context(), SettingsSelection{Workstation: SettingsDocument{Path: missing}}); err == nil {
		t.Error("missing workstation document accepted")
	}
	if _, err := LoadOperatorSettings(nil, SettingsSelection{}); err == nil { //nolint:staticcheck // deliberate nil context
		t.Error("nil context accepted")
	}
}

// TestLoadOperatorSettingsBoundaryOneOrNoDocument covers neither document configured (the
// built-in defaults, same shape a hook call gets before any settings document exists) and
// exactly one of the two configured.
func TestLoadOperatorSettingsBoundaryOneOrNoDocument(t *testing.T) {
	settings, err := LoadOperatorSettings(t.Context(), SettingsSelection{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := settings, DefaultOperatorSettings(); got.Hooks.Scope != want.Hooks.Scope || len(got.Hooks.Python) != len(want.Hooks.Python) {
		t.Errorf("unconfigured selection did not fall back to defaults: %+v", got)
	}
	opts, _ := operatorFixture(t)
	fleetOnly, err := LoadOperatorSettings(t.Context(), SettingsSelection{Fleet: SettingsDocument{Path: opts.FleetPath}})
	if err != nil || len(fleetOnly.Hooks.CommandPolicy.Deny) != 1 {
		t.Errorf("fleet-only selection lost the fleet layer: %+v %v", fleetOnly, err)
	}
	if len(fleetOnly.Hooks.Python) != 3 {
		t.Errorf("fleet-only selection did not fall back to the built-in interpreter candidates: %v", fleetOnly.Hooks.Python)
	}
}
