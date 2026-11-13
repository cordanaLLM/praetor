// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

// MaxPRTitleLimits bounds each layer and the merged repository title limits.
const MaxPRTitleLimits = 64

// PRTitleLimit caps explicit pull request titles in Unicode code points for Repo.
type PRTitleLimit struct {
	Repo string `json:"repo"`
	Max  int    `json:"max"`
}

// decodePRTitleLimits decodes a bounded sequence of strict repo/max mappings.
func decodePRTitleLimits(node *yaml.Node, path string) ([]PRTitleLimit, error) {
	if node.Kind != yaml.SequenceNode || len(node.Content) > MaxPRTitleLimits {
		return nil, fmt.Errorf("%s must be a list of at most %d records", path, MaxPRTitleLimits)
	}
	limits := make([]PRTitleLimit, 0, len(node.Content))
	for i := 0; i < len(node.Content) && i < MaxPRTitleLimits; i++ {
		limit, err := decodePRTitleLimit(node.Content[i], path)
		if err != nil {
			return nil, err
		}
		limits = append(limits, limit)
	}
	return limits, nil
}

// decodePRTitleLimit requires a repository string and integer max, refusing unknown keys.
func decodePRTitleLimit(node *yaml.Node, path string) (PRTitleLimit, error) {
	if node.Kind != yaml.MappingNode {
		return PRTitleLimit{}, fmt.Errorf("%s entry must be a mapping", path)
	}
	if err := requireKnownKeys(node, path, []string{"repo", "max"}); err != nil {
		return PRTitleLimit{}, err
	}
	repoNode, maxNode := policyMember(node, "repo"), policyMember(node, "max")
	if repoNode == nil || maxNode == nil {
		return PRTitleLimit{}, fmt.Errorf("%s entry requires repo and max", path)
	}
	repo, err := stringScalar(repoNode, path+".repo")
	if err != nil {
		return PRTitleLimit{}, err
	}
	maxText, err := intScalar(maxNode, path+".max")
	if err != nil {
		return PRTitleLimit{}, err
	}
	maximum, err := strconv.Atoi(maxText)
	if err != nil {
		return PRTitleLimit{}, fmt.Errorf("%s.max: %w", path, err)
	}
	return PRTitleLimit{Repo: repo, Max: maximum}, nil
}

// prTitleLimitsSetting validates decoded and programmatically supplied operator records.
func prTitleLimitsSetting(_ string, setting OperatorSetting) error {
	return ValidatePRTitleLimits(setting.PRTitleLimits)
}

// ValidatePRTitleLimits refuses malformed repositories, duplicate keys and invalid bounds.
func ValidatePRTitleLimits(limits []PRTitleLimit) error {
	if len(limits) > MaxPRTitleLimits {
		return fmt.Errorf("forge.pr_title_limits exceeds %d entries", MaxPRTitleLimits)
	}
	seen := make(map[string]bool, len(limits))
	for i := 0; i < len(limits) && i < MaxPRTitleLimits; i++ {
		limit := limits[i]
		if _, _, err := util.SplitGitHubRepository(limit.Repo); err != nil {
			return fmt.Errorf("forge.pr_title_limits repo: %w", err)
		}
		if limit.Max < 20 || limit.Max > 256 {
			return fmt.Errorf("forge.pr_title_limits max must be an integer from 20 to 256, got %d", limit.Max)
		}
		key := strings.ToLower(limit.Repo)
		if seen[key] {
			return fmt.Errorf("forge.pr_title_limits repeats repository %s", limit.Repo)
		}
		seen[key] = true
	}
	return nil
}

// mergePRTitleLimits merges by case-insensitive repository key, with later layers winning.
func (m *operatorMerge) mergePRTitleLimits(layer string, setting OperatorSetting) error {
	merged := slices.Clone(m.values[setting.Path].PRTitleLimits)
	indices := make(map[string]int, len(merged))
	for i := 0; i < len(merged) && i < MaxPRTitleLimits; i++ {
		indices[strings.ToLower(merged[i].Repo)] = i
	}
	for i := 0; i < len(setting.PRTitleLimits) && i < MaxPRTitleLimits; i++ {
		limit := setting.PRTitleLimits[i]
		key := strings.ToLower(limit.Repo)
		if index, ok := indices[key]; ok {
			merged[index] = limit
		} else {
			indices[key] = len(merged)
			merged = append(merged, limit)
		}
	}
	if err := ValidatePRTitleLimits(merged); err != nil {
		return fmt.Errorf("merged %s: %w", setting.Path, err)
	}
	setting.PRTitleLimits = merged
	m.values[setting.Path] = cloneSetting(setting)
	m.contribute(setting.Path, layer)
	return nil
}
