// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

// codeRabbitFile is managed at two keys; adoption.decline: [coderabbit] keeps the entire file.
const codeRabbitFile = ".coderabbit.yaml"

// codeRabbitConfig is the v2 configuration created where the repository has none.
const codeRabbitConfig = "# yaml-language-server: $schema=https://coderabbit.ai/integrations/schema.v2.json\n" +
	"---\nreviews:\n  auto_review:\n    drafts: true\n    base_branches:\n      - .*\n"

// reconcileCodeRabbit enables draft and stacked-base review without replacing other settings.
func reconcileCodeRabbit(ctx context.Context, s *adoptSession) error {
	data, exists, err := observeAdoptionInput(ctx, s, codeRabbitFile)
	if err != nil {
		return fmt.Errorf("read CodeRabbit configuration: %w", err)
	}
	if !exists {
		_, err := s.scaffoldFile(ctx, scaffold{rel: codeRabbitFile, perm: filePerm, content: []byte(codeRabbitConfig),
			created:  "Enabled CodeRabbit auto-review of draft pull requests on every base branch",
			verified: "CodeRabbit draft and stacked-base review configured"})
		return err
	}
	merged, err := mergeCodeRabbit(ctx, data)
	if err != nil {
		return fmt.Errorf("manage %s: %w", codeRabbitFile, err)
	}
	if bytes.Equal(data, merged) {
		s.report.recordReconciled(codeRabbitFile, "CodeRabbit draft and stacked-base review already configured")
		return nil
	}
	return publishAdoptionMerge(ctx, s, codeRabbitFile, data, merged,
		"Managed reviews.auto_review.drafts=true and base_branches containing .*; other keys and comments kept; decline coderabbit in .standards.yaml to opt out")
}

// mergeCodeRabbit edits only the two owned keys through the shared node and text editors.
func mergeCodeRabbit(ctx context.Context, data []byte) ([]byte, error) {
	text, crlf, err := util.NormalizeLineEndingsStrict(string(data))
	if err != nil {
		return nil, fmt.Errorf("read CodeRabbit line endings: %w", err)
	}
	output := []byte(text)
	for _, key := range [2]string{"drafts", "base_branches"} {
		output, err = setCodeRabbitKey(ctx, output, key)
		if err != nil {
			return nil, err
		}
	}
	return []byte(util.RestoreLineEndings(string(output), crlf)), nil
}

// setCodeRabbitKey preserves correct values byte for byte and patches or encodes one changed key.
func setCodeRabbitKey(ctx context.Context, data []byte, key string) ([]byte, error) {
	var document yaml.Node
	if err := util.DecodeYAMLDocument(data, &document, util.YAMLDocumentOptions{AllowEmpty: true}); err != nil {
		return nil, fmt.Errorf("decode CodeRabbit YAML: %w", err)
	}
	if err := config.ValidateYAMLNodes(ctx, &document); err != nil {
		return nil, fmt.Errorf("validate CodeRabbit YAML: %w", err)
	}
	if len(document.Content) == 0 {
		document = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := document.Content[0]
	auto := util.YAMLMappingValue(util.YAMLMappingValue(root, "reviews"), "auto_review")
	old := util.YAMLMappingValue(auto, key)
	value, changed, err := codeRabbitKeyValue(key, old)
	if err != nil || !changed {
		return data, err
	}
	patched, ok, err := patchYAMLPath(data, root, []string{"reviews", "auto_review", key}, value)
	if err != nil {
		return nil, err
	}
	for _, name := range [2]string{"reviews", "auto_review"} {
		auto, err = ensureAdoptYAMLMapping(root, name)
		if err != nil {
			return nil, fmt.Errorf("read CodeRabbit %s: %w", name, err)
		}
		root = auto
	}
	if at := mappingKeyIndex(auto, key); at >= 0 {
		auto.Content[at+1] = value
	} else {
		appendAdoptYAMLMapping(auto, key, value)
	}
	return manifestOutput(ctx, &document, patched, ok)
}

// codeRabbitKeyValue returns the desired value while retaining all node comments and branches.
func codeRabbitKeyValue(key string, old *yaml.Node) (*yaml.Node, bool, error) {
	value := &yaml.Node{}
	if old != nil {
		*value = *old
	}
	if key == "drafts" {
		return codeRabbitDraftValue(old, value)
	}
	return codeRabbitBranchesValue(old, value)
}

// codeRabbitBranchesValue adds the all-bases pattern without dropping an existing pattern.
func codeRabbitBranchesValue(old, value *yaml.Node) (*yaml.Node, bool, error) {
	if err := validateCodeRabbitBranches(old); err != nil {
		return nil, false, err
	}
	if slices.ContainsFunc(value.Content, func(entry *yaml.Node) bool { return entry.Value == ".*" }) {
		return value, false, nil
	}
	if len(value.Content) == maxAdoptManifestMappingNodes {
		return nil, false, fmt.Errorf("CodeRabbit base_branches has no room for .* within %d entries", maxAdoptManifestMappingNodes)
	}
	value.Kind, value.Tag, value.Value = yaml.SequenceNode, "!!seq", ""
	value.Content = append(append([]*yaml.Node(nil), value.Content...), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: ".*"})
	return value, true, nil
}

// validateCodeRabbitBranches checks every entry before an already-present wildcard can short-circuit.
func validateCodeRabbitBranches(old *yaml.Node) error {
	if old == nil {
		return nil
	}
	if !nullYAMLNode(old) && old.Kind != yaml.SequenceNode {
		return fmt.Errorf("reviews.auto_review.base_branches must be a sequence")
	}
	if len(old.Content) > maxAdoptManifestMappingNodes {
		return fmt.Errorf("CodeRabbit base_branches exceeds %d entries", maxAdoptManifestMappingNodes)
	}
	for i := 0; i < len(old.Content) && i < maxAdoptManifestMappingNodes; i++ {
		if old.Content[i].Kind != yaml.ScalarNode || old.Content[i].Tag != "!!str" {
			return fmt.Errorf("CodeRabbit base_branches entry %d must be a string", i)
		}
	}
	return nil
}

// codeRabbitDraftValue enables the boolean while retaining its comments.
func codeRabbitDraftValue(old, value *yaml.Node) (*yaml.Node, bool, error) {
	if old != nil && !nullYAMLNode(old) && (old.Kind != yaml.ScalarNode || old.Tag != "!!bool") {
		return nil, false, fmt.Errorf("reviews.auto_review.drafts must be a boolean")
	}
	changed := old == nil || old.Tag != "!!bool" || old.Value != "true"
	value.Kind, value.Tag, value.Value = yaml.ScalarNode, "!!bool", "true"
	value.Content = nil
	return value, changed, nil
}
