// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func writeStandardManifest(t *testing.T, dir, extra string) string {
	t.Helper()
	manifest := "version: 1\nrepository:\n  owner: example\n  name: demo\n" + extra
	return writePolicyFile(t, dir, ".standards.yaml", manifest)
}

func TestRepeatedSettings_Positive_FleetComplexity(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeStandardManifest(t, root, "overrides:\n  complexity:\n    max_func_loc: 60\n")
	fleetPath := writePolicyFile(t, root, "fleet.yaml", "complexity:\n  max_func_loc: 60\n")

	opts := EffectiveOptions{
		Root:         root,
		ManifestPath: manifestPath,
		FleetPath:    fleetPath,
	}

	findings, err := RepeatedSettings(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Path != "complexity.max_func_loc" {
		t.Errorf("Path = %q, want complexity.max_func_loc", f.Path)
	}
	if f.Value != "60" {
		t.Errorf("Value = %q, want 60", f.Value)
	}
	if f.RepoFile != manifestPath {
		t.Errorf("RepoFile = %q, want %q", f.RepoFile, manifestPath)
	}
	if !strings.Contains(f.Source, "fleet") || !strings.Contains(f.Source, fleetPath) {
		t.Errorf("Source = %q, want fleet and %q", f.Source, fleetPath)
	}
	if f.Line != 7 {
		t.Errorf("Line = %d, want 7", f.Line)
	}
}

func TestRepeatedSettings_Negative_ComplexityOverrideDiffers(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeStandardManifest(t, root, "overrides:\n  complexity:\n    max_func_loc: 50\n")
	fleetPath := writePolicyFile(t, root, "fleet.yaml", "complexity:\n  max_func_loc: 60\n")

	opts := EffectiveOptions{
		Root:         root,
		ManifestPath: manifestPath,
		FleetPath:    fleetPath,
	}

	findings, err := RepeatedSettings(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("want 0 findings for deliberate override, got %d: %+v", len(findings), findings)
	}
}

func TestRepeatedSettings_Negative_OperatorSettingDiffers(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeStandardManifest(t, root, "clients:\n  mode: strict\n")
	fleetPath := writePolicyFile(t, root, "fleet.yaml", "clients:\n  mode: advisory\n")

	opts := EffectiveOptions{
		Root:         root,
		ManifestPath: manifestPath,
		FleetPath:    fleetPath,
	}

	findings, err := RepeatedSettings(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("want 0 findings for differing operator setting, got %d: %+v", len(findings), findings)
	}
}

func TestRepeatedSettings_Positive_OrganizationBacklogCap(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeStandardManifest(t, root, "backlog:\n  caps:\n    defects:\n      max: 20\n")
	orgPath := writePolicyFile(t, root, "org.yaml", "backlog:\n  caps:\n    defects:\n      max: 20\n")

	opts := EffectiveOptions{
		Root:             root,
		ManifestPath:     manifestPath,
		OrganizationPath: orgPath,
	}

	findings, err := RepeatedSettings(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Path != "backlog.caps.defects.max" {
		t.Errorf("Path = %q, want backlog.caps.defects.max", f.Path)
	}
	if f.Value != "20" {
		t.Errorf("Value = %q, want 20", f.Value)
	}
	if f.RepoFile != manifestPath {
		t.Errorf("RepoFile = %q, want %q", f.RepoFile, manifestPath)
	}
	if !strings.Contains(f.Source, "organization") || !strings.Contains(f.Source, orgPath) {
		t.Errorf("Source = %q, want organization and %q", f.Source, orgPath)
	}
	if f.Line != 8 {
		t.Errorf("Line = %d, want 8", f.Line)
	}
}

func TestRepeatedSettings_Negative_BacklogCapDiffers(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeStandardManifest(t, root, "backlog:\n  caps:\n    defects:\n      max: 10\n")
	orgPath := writePolicyFile(t, root, "org.yaml", "backlog:\n  caps:\n    defects:\n      max: 20\n")

	opts := EffectiveOptions{
		Root:             root,
		ManifestPath:     manifestPath,
		OrganizationPath: orgPath,
	}

	findings, err := RepeatedSettings(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("want 0 findings for tighter backlog cap, got %d: %+v", len(findings), findings)
	}
}

func TestRepeatedSettings_Positive_OperatorSettingEqualsFleet(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeStandardManifest(t, root, "hooks:\n  scope: governed\n")
	fleetPath := writePolicyFile(t, root, "fleet.yaml", "hooks:\n  scope: governed\n")

	opts := EffectiveOptions{
		Root:         root,
		ManifestPath: manifestPath,
		FleetPath:    fleetPath,
	}

	findings, err := RepeatedSettings(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Path != "hooks.scope" {
		t.Errorf("Path = %q, want hooks.scope", f.Path)
	}
	if f.Value != "governed" {
		t.Errorf("Value = %q, want governed", f.Value)
	}
	if f.RepoFile != manifestPath {
		t.Errorf("RepoFile = %q, want %q", f.RepoFile, manifestPath)
	}
	if !strings.Contains(f.Source, "fleet") || !strings.Contains(f.Source, fleetPath) {
		t.Errorf("Source = %q, want fleet and %q", f.Source, fleetPath)
	}
	if f.Line != 6 {
		t.Errorf("Line = %d, want 6", f.Line)
	}
}

func TestRepeatedSettings_Negative_KeyNoUpperLayerHas(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeStandardManifest(t, root, "overrides:\n  complexity:\n    max_statements: 40\n")
	fleetPath := writePolicyFile(t, root, "fleet.yaml", "complexity:\n  max_func_loc: 60\n")

	opts := EffectiveOptions{
		Root:         root,
		ManifestPath: manifestPath,
		FleetPath:    fleetPath,
	}

	findings, err := RepeatedSettings(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("want 0 findings for key no upper layer sets, got %d: %+v", len(findings), findings)
	}
}

func TestRepeatedSettings_Boundary_NoUpperLayers(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeStandardManifest(t, root, "overrides:\n  complexity:\n    max_func_loc: 60\n")

	opts := EffectiveOptions{
		Root:         root,
		ManifestPath: manifestPath,
	}

	findings, err := RepeatedSettings(opts)
	if len(findings) != 0 {
		t.Fatalf("want empty findings without upper layers, got %d", len(findings))
	}
	if err == nil || !errors.Is(err, ErrNoComparisonSource) {
		t.Fatalf("want ErrNoComparisonSource, got %v", err)
	}
	if err.Error() != NoComparisonSourceReason {
		t.Fatalf("err.Error() = %q, want %q", err.Error(), NoComparisonSourceReason)
	}
}

func TestRepeatedSettings_Negative_MalformedFleetFile(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeStandardManifest(t, root, "overrides:\n  complexity:\n    max_func_loc: 60\n")
	fleetPath := writePolicyFile(t, root, "fleet.yaml", "complexity: [malformed\n")

	opts := EffectiveOptions{
		Root:         root,
		ManifestPath: manifestPath,
		FleetPath:    fleetPath,
	}

	findings, err := RepeatedSettings(opts)
	if err == nil {
		t.Fatal("want error for malformed fleet file, got nil")
	}
	if errors.Is(err, ErrNoComparisonSource) {
		t.Fatalf("want loader error, got ErrNoComparisonSource")
	}
	if len(findings) != 0 {
		t.Fatalf("want 0 findings on error, got %d", len(findings))
	}
}

func TestRepeatedSettings_Boundary_OrganizationOverridesFleet(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeStandardManifest(t, root, "overrides:\n  complexity:\n    max_func_loc: 60\n")
	fleetPath := writePolicyFile(t, root, "fleet.yaml", "complexity:\n  max_func_loc: 70\n")
	orgPath := writePolicyFile(t, root, "org.yaml", "complexity:\n  max_func_loc: 60\n")

	opts := EffectiveOptions{
		Root:             root,
		ManifestPath:     manifestPath,
		FleetPath:        fleetPath,
		OrganizationPath: orgPath,
	}

	findings, err := RepeatedSettings(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(findings), findings)
	}
	if !strings.Contains(findings[0].Source, "organization") || !strings.Contains(findings[0].Source, orgPath) {
		t.Errorf("Source = %q, want organization (%q)", findings[0].Source, orgPath)
	}
}

func TestRepeatedSettings_Boundary_HostLayersIgnored(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeStandardManifest(t, root, "overrides:\n  complexity:\n    max_func_loc: 60\n")
	deploymentPath := writePolicyFile(t, root, "deployment.yaml", "complexity:\n  max_func_loc: 60\n")
	workstationPath := writePolicyFile(t, root, "workstation.yaml", "complexity:\n  max_func_loc: 60\n")

	opts := EffectiveOptions{
		Root:            root,
		ManifestPath:    manifestPath,
		DeploymentPath:  deploymentPath,
		WorkstationPath: workstationPath,
	}

	findings, err := RepeatedSettings(opts)
	if len(findings) != 0 {
		t.Fatalf("want 0 findings, host data must be ignored, got %d", len(findings))
	}
	if !errors.Is(err, ErrNoComparisonSource) {
		t.Fatalf("want ErrNoComparisonSource when only host layers provided, got %v", err)
	}
}

func TestRepeatedSettings_Boundary_NilContext(t *testing.T) {
	root := t.TempDir()
	opts := EffectiveOptions{Root: root}
	var noContext context.Context
	if _, err := RepeatedSettingsContext(noContext, opts); err == nil {
		t.Fatal("want error for nil context, got nil")
	}
}

func TestRepeatedSettings_Positive_BacklogActionRepeat(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeStandardManifest(t, root, "backlog:\n  caps:\n    defects:\n      max: 15\n      action: gate\n")
	orgPath := writePolicyFile(t, root, "org.yaml", "backlog:\n  caps:\n    defects:\n      max: 20\n      action: gate\n")

	opts := EffectiveOptions{
		Root:             root,
		ManifestPath:     manifestPath,
		OrganizationPath: orgPath,
	}

	findings, err := RepeatedSettings(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("want 1 finding (action matches, max differs), got %d: %+v", len(findings), findings)
	}
	if findings[0].Path != "backlog.caps.defects.action" || findings[0].Value != "gate" {
		t.Errorf("finding = %+v, want action gate", findings[0])
	}
	if findings[0].Line != 9 {
		t.Errorf("Line = %d, want 9", findings[0].Line)
	}
}

func TestRepeatedSettings_Negative_MissingManifest(t *testing.T) {
	root := t.TempDir()
	fleetPath := writePolicyFile(t, root, "fleet.yaml", "complexity:\n  max_func_loc: 60\n")

	opts := EffectiveOptions{
		Root:         root,
		ManifestPath: filepath.Join(root, "missing.yaml"),
		FleetPath:    fleetPath,
	}

	if _, err := RepeatedSettings(opts); err == nil {
		t.Fatal("want error for missing manifest, got nil")
	}
}
