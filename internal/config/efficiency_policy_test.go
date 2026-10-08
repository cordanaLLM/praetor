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
	if p.Sources.Forge.Path != "fixtures/prs.json" {
		t.Errorf("expected forge path fixtures/prs.json, got %q", p.Sources.Forge.Path)
	}
	if p.Sources.Transcripts.Directory() != ".claude/transcripts" {
		t.Errorf("expected transcripts dir .claude/transcripts, got %q", p.Sources.Transcripts.Directory())
	}
	if p.Sources.SpendLog.Path != "fixtures/spend.jsonl" {
		t.Errorf("expected spend_log path fixtures/spend.jsonl, got %q", p.Sources.SpendLog.Path)
	}

	frontier := p.EffectiveFrontierModels()
	if len(frontier) != 2 || frontier[0] != "custom-frontier-1" {
		t.Errorf("unexpected effective frontier models: %v", frontier)
	}
	local := p.EffectiveLocalModels()
	if len(local) != 1 || local[0] != "custom-local-1" {
		t.Errorf("unexpected effective local models: %v", local)
	}

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
