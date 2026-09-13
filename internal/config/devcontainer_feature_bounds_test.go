package config

import (
	"context"
	"strings"
	"testing"
)

func TestDevContainerFeaturesRejectAmbiguousCatalogData(t *testing.T) {
	for name, raw := range map[string]string{
		"versions":        "devcontainer_features: [registry.test/features/node:1, registry.test/features/node:2]",
		"tag-digest":      "devcontainer_features: [registry.test/features/node:1, registry.test/features/node@sha256:abc]",
		"non-json-option": "devcontainer_features: [{feature: {value: .nan}}]",
		"duplicate":       "devcontainer_features: []\ndevcontainer_features: []",
		"alias":           "devcontainer_features: [&feature node:1, *feature]",
	} {
		t.Run(name, func(t *testing.T) {
			policy := &EffectivePolicy{CatalogArtifacts: []PolicyArtifact{{Content: []byte(raw)}}}
			if _, err := ResolveDevContainerFeatures(t.Context(), policy); err == nil {
				t.Fatal("ambiguous feature selection accepted")
			}
		})
	}
}

func TestDevContainerFeaturesEnforceAggregateBound(t *testing.T) {
	batch := []byte("devcontainer_features:\n" + strings.Repeat("  - registry.test/features/node:1\n", maxDevContainerFeatures/2))
	policy := &EffectivePolicy{CatalogArtifacts: []PolicyArtifact{{Content: batch}, {Content: batch}}}
	features, err := ResolveDevContainerFeatures(t.Context(), policy)
	if err != nil || len(features) != 1 {
		t.Fatalf("bounded repeated requirements did not deduplicate: %v", err)
	}
	policy.CatalogArtifacts = append(policy.CatalogArtifacts, PolicyArtifact{Content: []byte("devcontainer_features: [registry.test/features/go:1]")})
	if _, err := ResolveDevContainerFeatures(t.Context(), policy); err == nil {
		t.Fatal("aggregate declaration bound was ignored")
	}
}

func TestDevContainerEmptySelectionAndCancellation(t *testing.T) {
	features, err := ResolveDevContainerFeatures(t.Context(), &EffectivePolicy{})
	if err != nil || features == nil || len(features) != 0 {
		t.Fatalf("empty catalog must resolve to an explicit empty set: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ResolveDevContainerFeatures(ctx, &EffectivePolicy{}); err == nil {
		t.Fatal("empty catalog ignored cancellation")
	}
}
