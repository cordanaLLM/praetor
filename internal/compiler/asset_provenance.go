// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// DerivedFromKey is the key, under the front matter's metadata map, with which a canonical
// persona or skill names the upstream it was taken from, as "<url> (<SPDX license>)"
// (docs/adr/0014-operator-neutral-defaults.md, section 8). The credits page answers each one
// (internal/supplychain, CheckUpstreamCredits).
const DerivedFromKey = "derived_from"

// maxFrontMatterLines bounds the front matter read of one persona or skill (HISS-02).
const maxFrontMatterLines = 512

// errTopLevelDerivedFrom refuses a derived_from key outside metadata. The Agent Skills
// front matter keeps custom keys under metadata, so a top-level one is a declaration no reader
// of metadata sees.
var errTopLevelDerivedFrom = errors.New("declares derived_from at the top level of its front matter; write it under metadata")

// errUnclosedFrontMatter refuses a front matter block that opens a file and never closes, so a
// declaration inside it is not mistaken for body text and skipped.
var errUnclosedFrontMatter = errors.New("opens a front matter block that does not close")

// AssetUpstream is one canonical persona or skill and the upstream it declares.
type AssetUpstream struct {
	// Rel is the slash path of the declaring file below the repository root.
	Rel string
	// DerivedFrom is the metadata.derived_from value, trimmed; empty when the file declares
	// none (CanonicalAssets alone returns such files).
	DerivedFrom string
}

// assetFrontMatter is the part of a persona or skill front matter the provenance read decodes.
type assetFrontMatter struct {
	Metadata struct {
		DerivedFrom string `yaml:"derived_from"`
	} `yaml:"metadata"`
	TopLevelDerivedFrom any `yaml:"derived_from"`
}

// CanonicalAssetUpstreams returns every canonical persona (.agents/agents) and skill
// (.agents/skills) below rootDir that declares metadata.derived_from, personas first, each in
// name order. The plugin copies are projections that compile-context --verify holds
// byte-identical to these files (VerifyPluginSkills, VerifyAgentProjections), so the canonical
// declaration is the one a credit answers for. A front matter that is not YAML, does not close,
// or names derived_from outside metadata is an error naming the file.
func CanonicalAssetUpstreams(ctx context.Context, rootDir string) ([]AssetUpstream, error) {
	assets, err := CanonicalAssets(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	upstreams := make([]AssetUpstream, 0, len(assets))
	for _, asset := range assets {
		if asset.DerivedFrom != "" {
			upstreams = append(upstreams, asset)
		}
	}
	return upstreams, nil
}

// CanonicalAssets returns every canonical persona and skill below rootDir, in the order and
// under the rules of CanonicalAssetUpstreams, each with the upstream it declares or an empty
// DerivedFrom when it declares none: the files the credits gate requires to declare an upstream
// or to be marked original.
func CanonicalAssets(ctx context.Context, rootDir string) ([]AssetUpstream, error) {
	personas, err := listCanonicalAgents(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	skills, err := listCanonicalSkills(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	assets, err := readAssets(ctx, rootDir, personas, readCanonicalAgent,
		func(name string) string { return CanonicalAgentsRel + "/" + name })
	if err != nil {
		return nil, err
	}
	skillAssets, err := readAssets(ctx, rootDir, skills, readCanonicalSkill,
		func(name string) string { return skillEntryRel(CanonicalSkillsRel, name) })
	if err != nil {
		return nil, err
	}
	return append(assets, skillAssets...), nil
}

// readAssets reads each of names below rootDir with read and returns one asset per name, under
// the slash path rel gives it, with the upstream it declares.
func readAssets(ctx context.Context, rootDir string, names []string,
	read func(context.Context, string, string) ([]byte, error), rel func(string) string,
) ([]AssetUpstream, error) {
	assets := make([]AssetUpstream, 0, len(names))
	for i := 0; i < len(names) && i < maxSkillProjections; i++ {
		data, err := read(ctx, rootDir, names[i])
		if err != nil {
			return nil, err
		}
		derivedFrom, err := AssetDerivedFrom(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel(names[i]), err)
		}
		assets = append(assets, AssetUpstream{Rel: rel(names[i]), DerivedFrom: derivedFrom})
	}
	return assets, nil
}

// AssetDerivedFrom returns the trimmed metadata.derived_from value of the YAML front matter
// that opens data, or "" when data opens none or it declares no upstream.
func AssetDerivedFrom(data []byte) (string, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	end, opened := util.FrontMatterEnd(lines, maxFrontMatterLines)
	if !opened {
		return "", nil
	}
	if end == 0 {
		return "", errUnclosedFrontMatter
	}
	var front assetFrontMatter
	block := strings.Join(lines[1:end-1], "\n")
	if err := util.DecodeYAMLDocument([]byte(block), &front, util.YAMLDocumentOptions{AllowEmpty: true}); err != nil {
		return "", fmt.Errorf("front matter: %w", err)
	}
	if front.TopLevelDerivedFrom != nil {
		return "", errTopLevelDerivedFrom
	}
	return strings.TrimSpace(front.Metadata.DerivedFrom), nil
}
