// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

// DeclaredLinter names one linter and the profile or facet that declared it.
type DeclaredLinter struct {
	Name   string
	Source string
}

// DeclaredLinters returns every linter declared by the profiles and facets retained in
// p.CatalogArtifacts, and where each was declared (e.g. `facet "security:high"` or `profile "framework"`).
// If no artifacts were retained but p.Policy.Linters is populated, each is reported with source "policy".
func (p *EffectivePolicy) DeclaredLinters(ctx context.Context) ([]DeclaredLinter, error) {
	if ctx == nil {
		return nil, errors.New("declared linters discovery requires a context")
	}
	if p == nil {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(p.CatalogArtifacts) == 0 {
		return defaultDeclaredLinters(p.Policy.Linters), nil
	}
	if len(p.CatalogArtifacts) > maxPolicyLayers {
		return nil, fmt.Errorf("policy retains more than %d catalog texts", maxPolicyLayers)
	}

	facetDir := path.Join(archetypeDirName, facetDirName)
	sources := make(map[string][]string)

	for i := 0; i < len(p.CatalogArtifacts) && i < maxPolicyLayers; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		artifact := p.CatalogArtifacts[i]
		archetype, err := decodeArchetype(ctx, artifact.RelativePath, artifact.Content)
		if err != nil {
			return nil, err
		}
		source := formatArtifactSource(artifact.RelativePath, facetDir, archetype.ID)
		recordLinterSources(sources, archetype.Controls.Linters, source)
	}

	return formatDeclaredLinters(sources), nil
}

func formatArtifactSource(relPath, facetDir, id string) string {
	kind := "profile"
	if path.Dir(relPath) == facetDir {
		kind = "facet"
	}
	return fmt.Sprintf("%s %q", kind, id)
}

func recordLinterSources(sources map[string][]string, linters []string, source string) {
	for _, linter := range linters {
		if !slices.Contains(sources[linter], source) {
			sources[linter] = append(sources[linter], source)
		}
	}
}

func formatDeclaredLinters(sources map[string][]string) []DeclaredLinter {
	result := make([]DeclaredLinter, 0, len(sources))
	for name, srcList := range sources {
		result = append(result, DeclaredLinter{
			Name:   name,
			Source: strings.Join(srcList, ", "),
		})
	}
	slices.SortFunc(result, func(a, b DeclaredLinter) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return result
}

func defaultDeclaredLinters(linters []string) []DeclaredLinter {
	if len(linters) == 0 {
		return nil
	}
	result := make([]DeclaredLinter, 0, len(linters))
	for _, name := range linters {
		result = append(result, DeclaredLinter{
			Name:   name,
			Source: "policy",
		})
	}
	slices.SortFunc(result, func(a, b DeclaredLinter) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return result
}
