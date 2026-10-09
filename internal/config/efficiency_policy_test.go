// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ----------------------------------------------------------------------------
// 1. Positive Tests
// ----------------------------------------------------------------------------

func TestEfficiencyPolicy_Positive_LoadAndDefaults(t *testing.T) {
	manifestBody := `version: 1
repository:
  owner: "exampleOrg"
  name: "example"
efficiency:
  sources:
    forge:
      path: "fixtures/prs.json"
    transcripts:
      dir: ".claude/transcripts"
    spend_log:
      path: "fixtures/spend.jsonl"
  frontier_models:
    - "custom-frontier-1"
    - "custom-frontier-2"
  local_models:
    - "custom-local-1"
`
	manifestPath := writeManifest(t, manifestBody)
	m, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("unexpected error loading manifest: %v", err)
	}
	if m.Efficiency == nil {
		t.Fatal("expected Efficiency section to be populated")
	}

	p := m.Efficiency
	assertLoadedSources(t, p)
	assertLoadedModels(t, p)

	// RepositoryEfficiencyPolicy on temp directory
	rootDir := filepath.Dir(manifestPath)
	loadedPolicy, err := RepositoryEfficiencyPolicy(rootDir)
	if err != nil {
		t.Fatalf("unexpected error from RepositoryEfficiencyPolicy: %v", err)
	}
	if loadedPolicy == nil || loadedPolicy.Sources.Forge.Path != "fixtures/prs.json" {
		t.Errorf("unexpected policy returned by RepositoryEfficiencyPolicy: %+v", loadedPolicy)
	}
}

func assertLoadedSources(t *testing.T, p *EfficiencyPolicy) {
	t.Helper()
	if p.Sources.Forge.Path != "fixtures/prs.json" {
		t.Errorf("expected forge path fixtures/prs.json, got %q", p.Sources.Forge.Path)
	}
	if p.Sources.Transcripts.Directory() != ".claude/transcripts" {
		t.Errorf("expected transcripts dir .claude/transcripts, got %q", p.Sources.Transcripts.Directory())
	}
	if p.Sources.SpendLog.Path != "fixtures/spend.jsonl" {
		t.Errorf("expected spend_log path fixtures/spend.jsonl, got %q", p.Sources.SpendLog.Path)
	}
}

func assertLoadedModels(t *testing.T, p *EfficiencyPolicy) {
	t.Helper()
	frontier := p.EffectiveFrontierModels()
	if len(frontier) != 2 || frontier[0] != "custom-frontier-1" {
		t.Errorf("unexpected effective frontier models: %v", frontier)
	}
	local := p.EffectiveLocalModels()
	if len(local) != 1 || local[0] != "custom-local-1" {
		t.Errorf("unexpected effective local models: %v", local)
	}
}

func TestEfficiencyPolicy_Positive_TranscriptsFallbackPath(t *testing.T) {
	var src TranscriptsSource
	src.Path = "custom/path"
	if src.Directory() != "custom/path" {
		t.Errorf("expected custom/path fallback, got %q", src.Directory())
	}
	src.Dir = "custom/dir"
	if src.Directory() != "custom/dir" {
		t.Errorf("expected dir to take precedence over path, got %q", src.Directory())
	}
}

func TestEfficiencyPolicy_Positive_EmptyDefaults(t *testing.T) {
	var p *EfficiencyPolicy
	frontier := p.EffectiveFrontierModels()
	if len(frontier) == 0 {
		t.Fatal("expected default frontier models on nil policy")
	}
	local := p.EffectiveLocalModels()
	if len(local) == 0 {
		t.Fatal("expected default local models on nil policy")
	}

	emptyP := &EfficiencyPolicy{}
	if len(emptyP.EffectiveFrontierModels()) != len(DefaultFrontierModels()) {
		t.Errorf("expected default frontier models on empty policy")
	}
	if len(emptyP.EffectiveLocalModels()) != len(DefaultLocalModels()) {
		t.Errorf("expected default local models on empty policy")
	}
}

func TestRepositoryEfficiencyPolicy_Positive_NoManifest(t *testing.T) {
	emptyDir := t.TempDir()
	policy, err := RepositoryEfficiencyPolicy(emptyDir)
	if err != nil {
		t.Fatalf("unexpected error when no manifest exists: %v", err)
	}
	if policy != nil {
		t.Errorf("expected nil policy when no manifest exists, got %+v", policy)
	}
}

// ----------------------------------------------------------------------------
// 2. Negative Tests
// ----------------------------------------------------------------------------

func TestEfficiencyPolicy_Negative_InvalidPaths(t *testing.T) {
	invalidCases := []struct {
		name string
		yaml string
	}{
		{
			name: "absolute forge path",
			yaml: `version: 1
repository:
  owner: "exampleOrg"
  name: "example"
efficiency:
  sources:
    forge:
      path: "/absolute/path/prs.json"
`,
		},
		{
			name: "escaping transcripts dir",
			yaml: `version: 1
repository:
  owner: "exampleOrg"
  name: "example"
efficiency:
  sources:
    transcripts:
      dir: "../escaping/transcripts"
`,
		},
		{
			name: "absolute spend log path",
			yaml: `version: 1
repository:
  owner: "exampleOrg"
  name: "example"
efficiency:
  sources:
    spend_log:
      path: "/var/log/spend.jsonl"
`,
		},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeManifest(t, tc.yaml)
			_, err := LoadManifest(path)
			if err == nil {
				t.Fatalf("expected error for %s, but loaded successfully", tc.name)
			}
			if !strings.Contains(err.Error(), "efficiency.sources") {
				t.Errorf("expected error mentioning efficiency.sources, got: %v", err)
			}
		})
	}
}

func TestRepositoryEfficiencyPolicy_Negative_CorruptManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManifestFileName)
	if err := os.WriteFile(path, []byte("version: invalid: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := RepositoryEfficiencyPolicy(dir)
	if err == nil {
		t.Fatal("expected error on corrupt manifest")
	}
}

// ----------------------------------------------------------------------------
// 3. Boundary Tests
// ----------------------------------------------------------------------------

func TestEfficiencyPolicy_Boundary_ModelLimits(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("version: 1\nrepository:\n  owner: \"exampleOrg\"\n  name: \"example\"\nefficiency:\n  frontier_models:\n")
	for i := 0; i < 105; i++ {
		fmt.Fprintf(&sb, "    - \"model-%d\"\n", i)
	}

	path := writeManifest(t, sb.String())
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("expected error when frontier_models exceeds 100 entries")
	}
	if !strings.Contains(err.Error(), "frontier_models exceeds 100 entries limit") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestEfficiencyPolicy_Positive_ClassesAndDefaults(t *testing.T) {
	path := writeManifest(t, "version: 1\nrepository:\n  owner: \"exampleOrg\"\n  name: \"example\"\nefficiency:\n  frontier_classes: [\"deep\"]\n  light_classes: [\"tiny\"]\n")
	manifest, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	p := manifest.Efficiency
	if got := p.EffectiveFrontierClasses(); len(got) != 1 || got[0] != "deep" {
		t.Errorf("frontier classes: %v", got)
	}
	if got := p.EffectiveLightClasses(); len(got) != 1 || got[0] != "tiny" {
		t.Errorf("light classes: %v", got)
	}
	var none *EfficiencyPolicy
	if len(none.EffectiveFrontierClasses()) == 0 || len(none.EffectiveLightClasses()) == 0 {
		t.Error("a nil policy selects the default classes")
	}
}

func TestEfficiencyPolicy_Negative_TranscriptsDirAndPathDisagree(t *testing.T) {
	path := writeManifest(t, "version: 1\nrepository:\n  owner: \"exampleOrg\"\n  name: \"example\"\nefficiency:\n  sources:\n    transcripts:\n      dir: \"a\"\n      path: \"b\"\n")
	if _, err := LoadManifest(path); err == nil || !strings.Contains(err.Error(), "sets both dir") {
		t.Fatalf("want a conflict error, got %v", err)
	}
	same := writeManifest(t, "version: 1\nrepository:\n  owner: \"exampleOrg\"\n  name: \"example\"\nefficiency:\n  sources:\n    transcripts:\n      dir: \"a\"\n      path: \"a\"\n")
	if _, err := LoadManifest(same); err != nil {
		t.Fatalf("equal values are one setting: %v", err)
	}
}

func TestEfficiencyPolicy_Boundary_ClassLimit(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("version: 1\nrepository:\n  owner: \"exampleOrg\"\n  name: \"example\"\nefficiency:\n  light_classes:\n")
	for i := 0; i < 101; i++ {
		fmt.Fprintf(&sb, "    - \"c%d\"\n", i)
	}
	if _, err := LoadManifest(writeManifest(t, sb.String())); err == nil || !strings.Contains(err.Error(), "limited to 100") {
		t.Fatalf("want class limit error, got %v", err)
	}
}

func TestDefaultFrontierModels_Negative_NoCheapFamilyPrefixes(t *testing.T) {
	for _, entry := range DefaultFrontierModels() {
		for _, token := range strings.FieldsFunc(entry, func(r rune) bool { return r == '-' || r == '.' }) {
			for _, marker := range CheapTierMarkers() {
				if token == marker {
					t.Errorf("default %q names cheap tier %q", entry, marker)
				}
			}
		}
	}
}

func TestEfficiencyPolicy_Positive_TranscriptsViaGateway(t *testing.T) {
	manifestYAML := `version: 1
repository:
  owner: "exampleOrg"
  name: "example"
efficiency:
  sources:
    transcripts_via_gateway: true
`
	m, err := LoadManifest(writeManifest(t, manifestYAML))
	if err != nil {
		t.Fatalf("unexpected error loading manifest: %v", err)
	}
	if m.Efficiency == nil || !m.Efficiency.Sources.TranscriptsViaGateway {
		t.Errorf("expected TranscriptsViaGateway to be true, got %+v", m.Efficiency)
	}
}

func TestEfficiencyPolicy_Boundary_TranscriptsViaGatewayDefault(t *testing.T) {
	manifestYAML := `version: 1
repository:
  owner: "exampleOrg"
  name: "example"
efficiency:
  sources:
    forge:
      path: "fixtures/prs.json"
`
	m, err := LoadManifest(writeManifest(t, manifestYAML))
	if err != nil {
		t.Fatalf("unexpected error loading manifest: %v", err)
	}
	if m.Efficiency == nil || m.Efficiency.Sources.TranscriptsViaGateway {
		t.Errorf("expected default TranscriptsViaGateway to be false, got %+v", m.Efficiency)
	}
}

func TestEfficiencyPolicy_Negative_TranscriptsViaGatewayInvalidType(t *testing.T) {
	manifestYAML := `version: 1
repository:
  owner: "exampleOrg"
  name: "example"
efficiency:
  sources:
    transcripts_via_gateway: "not-a-bool"
`
	_, err := LoadManifest(writeManifest(t, manifestYAML))
	if err == nil {
		t.Fatal("expected error for non-boolean transcripts_via_gateway")
	}
}
