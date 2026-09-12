package config

import (
	"context"
	"strings"
	"testing"
)

func TestResolveDevContainerFeaturesSelectedUnionAndOptions(t *testing.T) {
	policy := &EffectivePolicy{CatalogArtifacts: []PolicyArtifact{
		{RelativePath: ".config/archetypes/node.yaml", Content: []byte("devcontainer_features:\n  - ghcr.io/devcontainers/features/node:1\n  - ghcr.io/devcontainers/features/common-utils:2\n")},
		{RelativePath: ".config/archetypes/facets/security.yaml", Content: []byte("devcontainer_features:\n  - ghcr.io/devcontainers/features/common-utils:2\n")},
	}}
	features, err := ResolveDevContainerFeatures(context.Background(), policy)
	if err != nil || len(features) != 2 {
		t.Fatalf("resolve features: %v (%d)", err, len(features))
	}
	if features[0].Ref != "ghcr.io/devcontainers/features/common-utils:2" {
		t.Fatalf("features must be deterministic and sorted: %+v", features)
	}

	policy.CatalogArtifacts[0].Content = []byte("devcontainer_features:\n  - ghcr.io/devcontainers/features/node:1:\n      version: 20\n")
	features, err = ResolveDevContainerFeatures(context.Background(), policy)
	var nodeFeature DevContainerFeature
	for _, feature := range features {
		if strings.Contains(feature.Ref, "/node:") {
			nodeFeature = feature
		}
	}
	if err != nil || len(nodeFeature.Options) != 1 || nodeFeature.Options["version"] != 20 {
		t.Fatalf("feature options were not preserved: %+v (%v)", features, err)
	}
}

func TestResolveDevContainerFeaturesRejectsConflictsAndMalformedEntries(t *testing.T) {
	for name, content := range map[string]string{
		"conflict": "devcontainer_features: [{feature: {version: 1}}, {feature: {version: 2}}]\n",
		"version":  "devcontainer_features: [ghcr.io/devcontainers/features/node:1, ghcr.io/devcontainers/features/node:2]\n",
		"empty":    "devcontainer_features: ['']\n",
		"shape":    "devcontainer_features: {feature: true}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveDevContainerFeatures(context.Background(), &EffectivePolicy{CatalogArtifacts: []PolicyArtifact{{Content: []byte(content)}}})
			if err == nil {
				t.Fatalf("expected feature validation error, got %v", err)
			}
		})
	}
}
