package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanningArtifactsProfileIsBoundedAndRuntimeNeutral(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".config", "archetypes", "planning-artifacts.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	node, err := decodePolicyDocument(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		ID      string `yaml:"id"`
		Runtime string `yaml:"runtime"`
	}
	if err := node.Decode(&header); err != nil {
		t.Fatal(err)
	}
	if header.ID != "planning-artifacts" || header.Runtime != "planning" {
		t.Fatalf("unexpected profile identity: %+v", header)
	}
	complexity, err := decodeComplexity(policyMember(node, "complexity"))
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]*int{
		"max_cyclomatic": complexity.MaxCyclomatic,
		"max_cognitive":  complexity.MaxCognitive,
		"max_func_loc":   complexity.MaxFuncLOC,
		"max_statements": complexity.MaxStatements,
	} {
		if got == nil || *got <= 0 {
			t.Fatalf("missing positive %s", name)
		}
	}
	if *complexity.MaxCyclomatic != 10 || *complexity.MaxCognitive != 15 || *complexity.MaxFuncLOC != 75 || *complexity.MaxStatements != 50 {
		t.Fatalf("profile limits drifted: %+v", complexity)
	}
	features, err := ResolveDevContainerFeatures(t.Context(), &EffectivePolicy{CatalogArtifacts: []PolicyArtifact{{RelativePath: "planning-artifacts.yaml", Content: data}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(features) != 1 || features[0].Ref != "ghcr.io/devcontainers/features/common-utils:2" {
		t.Fatalf("planning profile selected executable features: %+v", features)
	}
}

func TestPlanningArtifactsProfileRejectsDuplicateKeys(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".config", "archetypes", "planning-artifacts.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePolicyDocument(t.Context(), append(data, []byte("\nid: duplicate\n")...)); err == nil {
		t.Fatal("duplicate profile key accepted")
	}
}
