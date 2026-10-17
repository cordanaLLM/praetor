// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLibraryClientDevContainerFeaturesFollowLanguage(t *testing.T) {
	const source = `id: library-client
devcontainer_features_by_language:
  go:
    - ghcr.io/devcontainers/features/go:1
  rust:
    - ghcr.io/devcontainers/features/rust:1
`
	for _, tc := range []struct {
		name, manifest, want string
	}{
		{"Go adopter", "go.mod", "ghcr.io/devcontainers/features/go:1"},
		{"Rust adopter", "Cargo.toml", "ghcr.io/devcontainers/features/rust:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tc.manifest), []byte("\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			policy := &EffectivePolicy{Root: root, CatalogArtifacts: []PolicyArtifact{{
				RelativePath: "library-client.yaml", Content: []byte(source),
			}}}
			features, err := ResolveDevContainerFeatures(t.Context(), policy)
			if err != nil {
				t.Fatal(err)
			}
			if len(features) != 1 || features[0].Ref != tc.want {
				t.Fatalf("features = %v, want only %q", features, tc.want)
			}
		})
	}
}

func TestLibraryClientCommonAndLanguageFeaturesJoin(t *testing.T) {
	root := t.TempDir()
	for _, manifest := range []string{"Cargo.toml", "go.mod"} {
		if err := os.WriteFile(filepath.Join(root, manifest), []byte("\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policy := &EffectivePolicy{Root: root, CatalogArtifacts: []PolicyArtifact{{
		RelativePath: "library-client.yaml",
		Content:      []byte("id: library-client\ndevcontainer_features:\n  - common-utils\ndevcontainer_features_by_language:\n  go: [go-feature]\n  rust: [rust-feature]\n"),
	}}}
	features, err := ResolveDevContainerFeatures(t.Context(), policy)
	if err != nil {
		t.Fatal(err)
	}
	refs := []string{features[0].Ref, features[1].Ref, features[2].Ref}
	slices.Sort(refs)
	if !slices.Equal(refs, []string{"common-utils", "go-feature", "rust-feature"}) {
		t.Fatalf("mixed repository features = %v", refs)
	}
}

func TestLibraryClientLanguageFeatureMapRejectsUnsupportedLanguage(t *testing.T) {
	const source = `id: library-client
devcontainer_features_by_language:
  go: []
  rust: []
  python: []
`
	if _, err := decodeArchetype(t.Context(), "library-client.yaml", []byte(source)); err == nil {
		t.Fatal("unsupported language feature entry was accepted")
	}
}
