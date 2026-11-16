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

// codeRabbitFile has managed review defaults; adoption.decline: [coderabbit] keeps the entire file.
const codeRabbitFile = ".coderabbit.yaml"

// codeRabbitConfig is the v2 configuration created where the repository has none.
// Schema: https://coderabbit.ai/integrations/schema.v2.json defines
// reviews.path_instructions[].path and .instructions as required strings;
// instructions has maxLength 20000. The catch-all entry accepts CI evidence.
const codeRabbitConfig = "# yaml-language-server: $schema=https://coderabbit.ai/integrations/schema.v2.json\n" +
	"---\nreviews:\n  auto_review:\n    drafts: true\n    base_branches:\n      - .*\n" +
	"  path_instructions:\n    - path: \"**\"\n      instructions: |-\n" +
	"        Use the CI verification job as evidence for `make verify-all`.\n" +
	"        Find it in .github/workflows by its step running `make verify-all`.\n" +
	"        A green run of that job on the pull request head satisfies a request\n" +
	"        to run that target only when that step ran and passed; a run in\n" +
	"        which the step was skipped is no evidence. AGENTS.md and CLAUDE.md\n" +
	"        turn-end rules bind coding agents working in this repository,\n" +
	"        not the CodeRabbit reviewer.\n"

// reconcileCodeRabbit enables auto-review and CI evidence guidance while retaining user policy.
func reconcileCodeRabbit(ctx context.Context, s *adoptSession) error {
	data, exists, err := observeAdoptionInput(ctx, s, codeRabbitFile)
	if err != nil {
		return fmt.Errorf("read CodeRabbit configuration: %w", err)
	}
	if !exists {
		_, err := s.scaffoldFile(ctx, scaffold{rel: codeRabbitFile, perm: filePerm, content: []byte(codeRabbitConfig),
			created:  "Enabled CodeRabbit auto-review of drafts on every base branch and CI evidence guidance",
			verified: "CodeRabbit auto-review and CI evidence guidance configured"})
		return err
	}
	merged, err := mergeCodeRabbit(ctx, data)
	if err != nil {
		return fmt.Errorf("manage %s: %w", codeRabbitFile, err)
	}
	if bytes.Equal(data, merged) {
		s.report.recordReconciled(codeRabbitFile, "CodeRabbit auto-review and review guidance already configured")
		return nil
	}
	return publishAdoptionMerge(ctx, s, codeRabbitFile, data, merged,
		"Managed draft and all-base auto-review; added CI evidence guidance unless global instructions exist; user instructions and comments kept; decline coderabbit to opt out")
}

// mergeCodeRabbit manages auto-review defaults and adds global guidance only when absent.
func mergeCodeRabbit(ctx context.Context, data []byte) ([]byte, error) {
	text, crlf, err := util.NormalizeLineEndingsStrict(string(data))
	if err != nil {
		return nil, fmt.Errorf("read CodeRabbit line endings: %w", err)
	}
	output := []byte(text)
	for _, key := range [3]string{"drafts", "base_branches", "path_instructions"} {
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
	parent, path := codeRabbitKeyLocation(root, key)
	old := util.YAMLMappingValue(parent, key)
	value, changed, err := codeRabbitKeyValue(key, old)
	if err != nil || !changed {
		return data, err
	}
	patched, ok, err := patchYAMLPath(data, root, path, value)
	if err != nil {
		return nil, err
	}
	for i := 0; i < min(len(path)-1, 4); i++ {
		parent, err = ensureAdoptYAMLMapping(root, path[i])
		if err != nil {
			return nil, fmt.Errorf("read CodeRabbit %s: %w", path[i], err)
		}
		root = parent
	}
	if at := mappingKeyIndex(parent, key); at >= 0 {
		parent.Content[at+1] = value
	} else {
		appendAdoptYAMLMapping(parent, key, value)
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
	if key == "path_instructions" {
		return codeRabbitInstructionsValue(old, value)
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

// codeRabbitKeyLocation returns the parent node and managed path for one review setting.
func codeRabbitKeyLocation(root *yaml.Node, key string) (*yaml.Node, []string) {
	reviews := util.YAMLMappingValue(root, "reviews")
	if key == "path_instructions" {
		return reviews, []string{"reviews", key}
	}
	return util.YAMLMappingValue(reviews, "auto_review"), []string{"reviews", "auto_review", key}
}

// codeRabbitInstructionsValue appends the default unless a user already owns the global path.
func codeRabbitInstructionsValue(old, value *yaml.Node) (*yaml.Node, bool, error) {
	global, err := validateCodeRabbitInstructions(old)
	if err != nil || global {
		return value, false, err
	}
	if len(value.Content) == maxAdoptManifestMappingNodes {
		return nil, false, fmt.Errorf("CodeRabbit path_instructions has no room within %d entries", maxAdoptManifestMappingNodes)
	}
	var defaults yaml.Node
	if err := util.DecodeYAMLDocument([]byte(codeRabbitConfig), &defaults, util.YAMLDocumentOptions{}); err != nil {
		return nil, false, fmt.Errorf("decode CodeRabbit default instructions: %w", err)
	}
	instructions := util.YAMLMappingValue(util.YAMLMappingValue(defaults.Content[0], "reviews"), "path_instructions")
	value.Kind, value.Tag, value.Value = yaml.SequenceNode, "!!seq", ""
	value.Content = append(append([]*yaml.Node(nil), value.Content...), instructions.Content[0])
	return value, true, nil
}

// validateCodeRabbitInstructions checks bounded instruction objects before retaining a global entry.
func validateCodeRabbitInstructions(old *yaml.Node) (bool, error) {
	if old == nil || nullYAMLNode(old) {
		return false, nil
	}
	if old.Kind != yaml.SequenceNode || len(old.Content) > maxAdoptManifestMappingNodes {
		return false, fmt.Errorf("reviews.path_instructions must be a sequence of at most %d entries", maxAdoptManifestMappingNodes)
	}
	global := false
	for i := 0; i < len(old.Content) && i < maxAdoptManifestMappingNodes; i++ {
		path, err := codeRabbitInstructionPath(old.Content[i])
		if err != nil {
			return false, fmt.Errorf("reviews.path_instructions entry %d: %w", i, err)
		}
		global = global || path == "**"
	}
	return global, nil
}

// codeRabbitInstructionPath validates the schema's required string fields and returns the glob.
func codeRabbitInstructionPath(entry *yaml.Node) (string, error) {
	for _, key := range [2]string{"path", "instructions"} {
		field := util.YAMLMappingValue(entry, key)
		if field == nil || field.Kind != yaml.ScalarNode || field.Tag != "!!str" {
			return "", fmt.Errorf("%s must be a string", key)
		}
	}
	return util.YAMLMappingValue(entry, "path").Value, nil
}
