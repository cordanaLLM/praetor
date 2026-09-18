// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

func manifestFixture(t *testing.T, fleet, workstation *InstalledDocument) InstallManifest {
	t.Helper()
	return InstallManifest{
		Version: 1, EngineCommit: testCommit, InstalledAt: "2026-09-18T10:00:00Z", BinDir: t.TempDir(),
		Binaries: map[string]InstalledBinary{"praetorctl": {SHA256: strings.Repeat("a", 64)}},
		Previous: &InstalledPrevious{EngineCommit: testCommit, Backup: t.TempDir()},
		Settings: InstalledSettings{Fleet: fleet, Workstation: workstation},
		LastUpdate: &InstallAttempt{At: "2026-09-18T10:05:00+02:00", Target: testCommit, Result: "held",
			Reason: "target is not signed"},
	}
}

func writeInstallManifest(t *testing.T, manifest any) string {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return writePolicyFile(t, t.TempDir(), "install.json", string(data))
}

func recorded(t *testing.T, body string) *InstalledDocument {
	t.Helper()
	path := writePolicyFile(t, t.TempDir(), "settings.yaml", body)
	return &InstalledDocument{Path: path, SHA256: policyDigest([]byte(body))}
}

func TestReadInstallManifestRoundTrip(t *testing.T) {
	want := manifestFixture(t, recorded(t, "clients: {}\n"), nil)
	got, err := ReadInstallManifest(t.Context(), writeInstallManifest(t, want))
	if err != nil {
		t.Fatal(err)
	}
	if got.EngineCommit != want.EngineCommit || got.Settings.Fleet.Path != want.Settings.Fleet.Path || got.Settings.Workstation != nil {
		t.Fatalf("round trip: %+v", got)
	}
	minimal := manifestFixture(t, nil, nil)
	minimal.Previous, minimal.LastUpdate = nil, nil
	if _, err := ReadInstallManifest(t.Context(), writeInstallManifest(t, minimal)); err != nil {
		t.Fatalf("first install without previous or last update: %v", err)
	}
}

func TestReadInstallManifestRejectsInvalid(t *testing.T) {
	for name, change := range map[string]func(*InstallManifest){
		"version":      func(m *InstallManifest) { m.Version = 2 },
		"commit":       func(m *InstallManifest) { m.EngineCommit = strings.ToUpper(testCommit) },
		"time":         func(m *InstallManifest) { m.InstalledAt = "yesterday" },
		"relative bin": func(m *InstallManifest) { m.BinDir = "bin" },
		"no binaries":  func(m *InstallManifest) { m.Binaries = nil },
		"unknown binary": func(m *InstallManifest) {
			m.Binaries["praetor-evil"] = InstalledBinary{SHA256: strings.Repeat("a", 64)}
		},
		"binary digest":   func(m *InstallManifest) { m.Binaries["praetorctl"] = InstalledBinary{SHA256: "abc"} },
		"previous backup": func(m *InstallManifest) { m.Previous.Backup = "backup" },
		"settings digest": func(m *InstallManifest) { m.Settings.Fleet = &InstalledDocument{Path: m.BinDir, SHA256: "x"} },
		"result":          func(m *InstallManifest) { m.LastUpdate.Result = "maybe" },
		"reason":          func(m *InstallManifest) { m.LastUpdate.Reason = "$(id)" },
	} {
		manifest := manifestFixture(t, nil, nil)
		change(&manifest)
		if _, err := ReadInstallManifest(t.Context(), writeInstallManifest(t, manifest)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	valid, err := json.Marshal(manifestFixture(t, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeInstallManifest(valid); err != nil {
		t.Fatalf("valid manifest refused: %v", err)
	}
	for name, body := range map[string]string{
		"unknown member":   `{"extra":true,` + string(valid[1:]),
		"duplicate member": `{"version":1,` + string(valid[1:]),
		"not an object":    `[]`,
	} {
		if _, err := DecodeInstallManifest([]byte(body)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSelectOperatorSettingsOrder(t *testing.T) {
	fleet, workstation := recorded(t, "clients: {}\n"), recorded(t, "update: {}\n")
	manifest := writeInstallManifest(t, manifestFixture(t, fleet, workstation))
	env := map[string]string{FleetConfigEnv: "/env/fleet.yaml"}
	request := SettingsRequest{WorkstationFlag: "/flag/workstation.yaml", Getenv: func(name string) string { return env[name] },
		ManifestPath: manifest}
	selection, err := SelectOperatorSettings(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Fleet != (SettingsDocument{"/env/fleet.yaml", SettingsFromEnvironment}) ||
		selection.Workstation != (SettingsDocument{"/flag/workstation.yaml", SettingsFromFlag}) {
		t.Fatalf("flags and environment must win: %+v", selection)
	}
	request.WorkstationFlag, request.Getenv = "", nil
	selection, err = SelectOperatorSettings(t.Context(), request)
	if err != nil || selection.Fleet != (SettingsDocument{fleet.Path, SettingsFromManifest}) ||
		selection.Workstation != (SettingsDocument{workstation.Path, SettingsFromManifest}) {
		t.Fatalf("manifest paths: %+v, %v", selection, err)
	}
}

func TestSelectOperatorSettingsNotConfigured(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "install.json")
	for _, path := range []string{"", missing, writeInstallManifest(t, manifestFixture(t, nil, nil))} {
		selection, err := SelectOperatorSettings(t.Context(), SettingsRequest{ManifestPath: path})
		if err != nil || selection.Fleet.Origin != SettingsNotConfigured || selection.Workstation.Path != "" {
			t.Fatalf("manifest %q: %+v, %v", path, selection, err)
		}
	}
}

func TestSelectOperatorSettingsRejectsStaleOrInvalidManifest(t *testing.T) {
	fleet := recorded(t, "clients: {}\n")
	manifest := writeInstallManifest(t, manifestFixture(t, fleet, nil))
	if err := os.WriteFile(fleet.Path, []byte("clients: {mode: strict}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SelectOperatorSettings(t.Context(), SettingsRequest{ManifestPath: manifest}); err == nil ||
		!strings.Contains(err.Error(), "changed since the install manifest recorded them") {
		t.Fatalf("stale digest accepted: %v", err)
	}
	if err := os.Remove(fleet.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := SelectOperatorSettings(t.Context(), SettingsRequest{ManifestPath: manifest}); err == nil {
		t.Fatal("a missing recorded document was accepted")
	}
	broken := writePolicyFile(t, t.TempDir(), "install.json", `{"version":1}`)
	if _, err := SelectOperatorSettings(t.Context(), SettingsRequest{ManifestPath: broken}); err == nil {
		t.Fatal("an invalid manifest was accepted")
	}
	var noContext context.Context
	if _, err := SelectOperatorSettings(noContext, SettingsRequest{}); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestDefaultInstallManifestPath(t *testing.T) {
	path, err := DefaultInstallManifestPath()
	if err != nil {
		t.Skipf("no user configuration directory on this host: %v", err)
	}
	if !filepath.IsAbs(path) || filepath.Base(path) != "install.json" || filepath.Base(filepath.Dir(path)) != "praetor" {
		t.Fatalf("unexpected manifest path %q", path)
	}
}
