// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// RepeatedSetting records one setting declared in the repository layer (.standards.yaml)
// whose value equals the value already resolved by the fleet or organization policy layer.
type RepeatedSetting struct {
	Path     string
	Value    string
	RepoFile string
	Line     int
	Source   string
}

// NoComparisonSourceReason is the stated reason value returned when neither a fleet nor an
// organization policy layer is selected, so there is no source to compare against.
const NoComparisonSourceReason = "no fleet or organization policy layer selected to compare against"

// ErrNoComparisonSource is returned by RepeatedSettings when neither a fleet nor an
// organization policy layer is selected, so there is no source to compare against.
var ErrNoComparisonSource = errors.New(NoComparisonSourceReason)

// RepeatedSettings scans the repository layer (.standards.yaml) for settings that repeat
// identical values already resolved by the fleet or organization policy layer. Deliberate
// overrides (different or tighter limits) are not findings. Deployment and workstation
// layers are host data and are never consulted. If neither fleet nor organization layers
// are selected, it returns empty results with ErrNoComparisonSource.
func RepeatedSettings(opts EffectiveOptions) ([]RepeatedSetting, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return RepeatedSettingsContext(ctx, opts)
}

// RepeatedSettingsContext runs RepeatedSettings under the supplied context.
func RepeatedSettingsContext(ctx context.Context, opts EffectiveOptions) ([]RepeatedSetting, error) {
	if ctx == nil {
		return nil, errors.New("repeated settings scan requires context")
	}
	if opts.FleetPath == "" && opts.OrganizationPath == "" {
		return nil, ErrNoComparisonSource
	}
	paths, err := normalizeEffectiveOptions(opts)
	if err != nil {
		return nil, err
	}
	loader := effectiveLoader{ctx: ctx}
	upperPolicy, upperOperator, err := loadUpperComparisonLayers(&loader, opts)
	if err != nil {
		return nil, err
	}
	node, _, err := loader.document(paths.ManifestPath, "repository")
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := node.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode policy manifest: %w", err)
	}
	return scanRepeatedSettings(paths.ManifestPath, node, upperPolicy, upperOperator, opts)
}

func loadUpperComparisonLayers(l *effectiveLoader, opts EffectiveOptions) (*EffectivePolicy, *operatorMerge, error) {
	upperOpts := EffectiveOptions{
		FleetPath:        opts.FleetPath,
		OrganizationPath: opts.OrganizationPath,
	}
	upperLayers, err := l.externalLayers(upperOpts, nil)
	if err != nil {
		return nil, nil, err
	}
	upperPolicy, err := ResolvePolicy(l.ctx, upperLayers)
	if err != nil {
		return nil, nil, err
	}
	upperOperator := newOperatorMerge()
	for i := 0; i < len(upperLayers) && i < maxPolicyLayers; i++ {
		if err := upperOperator.apply(upperLayers[i].Source.ID, upperLayers[i].Settings); err != nil {
			return nil, nil, err
		}
	}
	return upperPolicy, upperOperator, nil
}

func scanRepeatedSettings(manifestPath string, node *yaml.Node, upperPolicy *EffectivePolicy, upperOperator *operatorMerge, opts EffectiveOptions) ([]RepeatedSetting, error) {
	complexityFindings, err := checkComplexityRepeats(manifestPath, node, upperPolicy, opts)
	if err != nil {
		return nil, err
	}
	backlogFindings, err := checkBacklogRepeats(manifestPath, node, upperPolicy, opts)
	if err != nil {
		return nil, err
	}
	operatorFindings, err := checkOperatorRepeats(manifestPath, node, upperOperator, opts)
	if err != nil {
		return nil, err
	}
	total := len(complexityFindings) + len(backlogFindings) + len(operatorFindings)
	findings := make([]RepeatedSetting, 0, min(total, maxOperatorSettings+16))
	findings = append(findings, complexityFindings...)
	findings = append(findings, backlogFindings...)
	findings = append(findings, operatorFindings...)
	return findings, nil
}

func checkComplexityRepeats(manifestPath string, node *yaml.Node, upperPolicy *EffectivePolicy, opts EffectiveOptions) ([]RepeatedSetting, error) {
	overrides := policyMember(node, "overrides")
	complexityNode := policyMember(overrides, "complexity")
	if complexityNode == nil || complexityNode.Kind != yaml.MappingNode {
		return nil, nil
	}
	var findings []RepeatedSetting
	for i := 0; i+1 < len(complexityNode.Content) && i < maxPolicyNodes; i += 2 {
		keyNode := complexityNode.Content[i]
		valueNode := complexityNode.Content[i+1]
		key := keyNode.Value
		limit, err := complexityLimit(key, valueNode, false)
		if err != nil {
			return nil, err
		}
		source := formatSource(upperPolicy.Fields[key], opts)
		if source == "" {
			continue
		}
		upperLimit := complexityLimitValue(&upperPolicy.Policy.Complexity, key)
		if limit == upperLimit {
			findings = append(findings, RepeatedSetting{
				Path:     "complexity." + key,
				Value:    strconv.Itoa(limit),
				RepoFile: manifestPath,
				Line:     keyNode.Line,
				Source:   source,
			})
		}
	}
	return findings, nil
}

func complexityLimitValue(policy *ComplexityPolicy, key string) int {
	names := complexityNames()
	ptrs := policy.pointers()
	for i := 0; i < len(names) && i < 4; i++ {
		if names[i] == key && ptrs[i] != nil {
			return *ptrs[i]
		}
	}
	return 0
}

func checkBacklogRepeats(manifestPath string, node *yaml.Node, upperPolicy *EffectivePolicy, opts EffectiveOptions) ([]RepeatedSetting, error) {
	backlogNode := policyMember(node, "backlog")
	capsNode := policyMember(backlogNode, "caps")
	if capsNode == nil || capsNode.Kind != yaml.MappingNode {
		return nil, nil
	}
	var findings []RepeatedSetting
	for i := 0; i+1 < len(capsNode.Content) && i < maxPolicyNodes; i += 2 {
		catKeyNode := capsNode.Content[i]
		catValueNode := capsNode.Content[i+1]
		if catValueNode.Kind != yaml.MappingNode {
			continue
		}
		catFindings, err := checkCategoryBacklogRepeats(manifestPath, catKeyNode.Value, catValueNode, upperPolicy, opts)
		if err != nil {
			return nil, err
		}
		findings = append(findings, catFindings...)
	}
	return findings, nil
}

func checkCategoryBacklogRepeats(manifestPath, category string, catValueNode *yaml.Node, upperPolicy *EffectivePolicy, opts EffectiveOptions) ([]RepeatedSetting, error) {
	var findings []RepeatedSetting
	for j := 0; j+1 < len(catValueNode.Content) && j < maxPolicyNodes; j += 2 {
		propKeyNode := catValueNode.Content[j]
		propValueNode := catValueNode.Content[j+1]
		prop := propKeyNode.Value
		path := "backlog.caps." + category + "." + prop
		source := formatSource(upperPolicy.BacklogFields[path], opts)
		if source == "" {
			continue
		}
		finding, err := checkOneBacklogRepeat(manifestPath, path, category, prop, propKeyNode, propValueNode, upperPolicy, source)
		if err != nil {
			return nil, err
		}
		if finding != nil {
			findings = append(findings, *finding)
		}
	}
	return findings, nil
}

func checkOneBacklogRepeat(manifestPath, path, category, prop string, propKeyNode, propValueNode *yaml.Node, upperPolicy *EffectivePolicy, source string) (*RepeatedSetting, error) {
	upperCap := upperCategoryCap(upperPolicy, category)
	switch prop {
	case "max":
		maxVal, err := decodeBacklogMax(path, propValueNode)
		if err != nil {
			return nil, err
		}
		if maxVal > 0 && maxVal == upperCap.Limit() {
			return &RepeatedSetting{
				Path:     path,
				Value:    strconv.Itoa(maxVal),
				RepoFile: manifestPath,
				Line:     propKeyNode.Line,
				Source:   source,
			}, nil
		}
	case "action":
		actionVal, err := decodeBacklogAction(path, propValueNode)
		if err != nil {
			return nil, err
		}
		if actionVal != "" && actionVal == upperCap.EffectiveAction() {
			return &RepeatedSetting{
				Path:     path,
				Value:    string(actionVal),
				RepoFile: manifestPath,
				Line:     propKeyNode.Line,
				Source:   source,
			}, nil
		}
	}
	return nil, nil
}

func upperCategoryCap(upperPolicy *EffectivePolicy, category string) BacklogCap {
	names := BacklogCategories()
	caps := upperPolicy.Policy.Backlog.Caps()
	for i := 0; i < len(names) && i < 4; i++ {
		if names[i] == category {
			return caps[i]
		}
	}
	return BacklogCap{}
}

func checkOperatorRepeats(manifestPath string, node *yaml.Node, upperOperator *operatorMerge, opts EffectiveOptions) ([]RepeatedSetting, error) {
	repoSettings, owned, err := decodeOperatorSections(node)
	if err != nil || !owned {
		return nil, err
	}
	var findings []RepeatedSetting
	for i := 0; i < len(repoSettings) && i < maxOperatorSettings; i++ {
		setting := repoSettings[i]
		source := formatSource(upperOperator.contributors[setting.Path], opts)
		if source == "" {
			continue
		}
		upperSetting, set := upperOperator.values[setting.Path]
		if set && sameSetting(setting, upperSetting) {
			findings = append(findings, RepeatedSetting{
				Path:     setting.Path,
				Value:    renderSettingValue(setting),
				RepoFile: manifestPath,
				Line:     setting.Line,
				Source:   source,
			})
		}
	}
	return findings, nil
}

func renderSettingValue(setting OperatorSetting) string {
	if setting.Value != "" {
		return setting.Value
	}
	if len(setting.List) > 0 {
		return strings.Join(setting.List, ", ")
	}
	if len(setting.Argv) > 0 {
		parts := make([]string, 0, len(setting.Argv))
		for i := 0; i < len(setting.Argv) && i < maxPolicyNodes; i++ {
			parts = append(parts, strings.Join(setting.Argv[i], " "))
		}
		return strings.Join(parts, "; ")
	}
	return ""
}

func formatSource(contributors []string, opts EffectiveOptions) string {
	for i := len(contributors) - 1; i >= 0 && i < maxPolicyLayers; i-- {
		switch contributors[i] {
		case "organization":
			return fmt.Sprintf("organization (%s)", opts.OrganizationPath)
		case "fleet":
			return fmt.Sprintf("fleet (%s)", opts.FleetPath)
		}
	}
	return ""
}
