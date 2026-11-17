// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/hiss"
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
	codeRabbitEvidence + "AGENTS.md and CLAUDE.md\n" +
	"        turn-end rules bind coding agents working in this repository,\n" +
	"        not the CodeRabbit reviewer.\n"

// codeRabbitEvidence is the managed guidance that requires a workflow running the target.
const codeRabbitEvidence = "        Use the CI verification job as evidence for `make verify-all`.\n" +
	"        Find it in .github/workflows by its step running `make verify-all`.\n" +
	"        A green run of that job on the pull request head satisfies a request\n" +
	"        to run that target only when that step ran and passed; a run in\n" +
	"        which the step was skipped is no evidence. "

// codeRabbitConfiguration renders evidence guidance only for an observed verification step.
func codeRabbitConfiguration(evidence bool) string {
	if evidence {
		return codeRabbitConfig
	}
	return strings.Replace(codeRabbitConfig, codeRabbitEvidence, "        ", 1)
}

// codeRabbitVerifyStep recognises literal make invocations at shell command boundaries.
var codeRabbitVerifyStep = regexp.MustCompile(`(?m)(?:^|[;&|])[ \t]*(?:[A-Za-z_][A-Za-z0-9_]*=[^ \t]+[ \t]+)*make[ \t]+(?:-[^ \t;|&]+[ \t]+)*verify-all(?:[ \t;|&]|$)`)

// codeRabbitWorkflowEvidence reads the existing workflow model and bounds before claiming CI evidence.
func codeRabbitWorkflowEvidence(ctx context.Context, s *adoptSession) (bool, error) {
	dir, err := repoFile(s.repoPath, ghworkflow.Dir)
	if err != nil {
		return false, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read verification workflows: %w", err)
	}
	paths, err := workflowEnginePinPaths(entries)
	if err != nil {
		return false, err
	}
	found := false
	for i := 0; i < len(paths) && i < ghworkflow.MaxFiles; i++ {
		data, _, err := observeAdoptionInput(ctx, s, paths[i])
		if err != nil {
			return false, err
		}
		match, err := codeRabbitWorkflowStep(data)
		if err != nil {
			return false, fmt.Errorf("read %s: %w", paths[i], err)
		}
		found = found || match
	}
	return found, nil
}

// codeRabbitWorkflowStep checks parsed run steps rather than workflow comments or job names.
func codeRabbitWorkflowStep(data []byte) (bool, error) {
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		return false, err
	}
	steps, err := ghworkflow.RunSteps(&spec)
	if err != nil {
		return false, err
	}
	for i := 0; i < len(steps) && i < ghworkflow.MaxJobsPerFile*ghworkflow.MaxStepsPerJob; i++ {
		if codeRabbitRunVerifyAll(steps[i].Step.Run) {
			return true, nil
		}
	}
	return false, nil
}

// codeRabbitRunVerifyAll excludes quoted data, comments and document bodies through the shared lexer.
func codeRabbitRunVerifyAll(script string) bool {
	lines, ok := hiss.ShellCode(script, ghworkflow.MaxStepsPerJob)
	return ok && codeRabbitVerifyStep.MatchString(strings.Join(lines, "\n"))
}

// reconcileCodeRabbit enables auto-review and CI evidence guidance while retaining user policy.
func reconcileCodeRabbit(ctx context.Context, s *adoptSession) error {
	evidence, err := codeRabbitWorkflowEvidence(ctx, s)
	if err != nil {
		return err
	}
	data, exists, err := observeAdoptionInput(ctx, s, codeRabbitFile)
	if err != nil {
		return fmt.Errorf("read CodeRabbit configuration: %w", err)
	}
	if !exists {
		_, err := s.scaffoldFile(ctx, scaffold{rel: codeRabbitFile, perm: filePerm, content: []byte(codeRabbitConfiguration(evidence)),
			created:  "Enabled CodeRabbit auto-review of drafts on every base branch and CI evidence guidance",
			verified: "CodeRabbit auto-review and CI evidence guidance configured"})
		return err
	}
	merged, err := mergeCodeRabbit(ctx, data, evidence)
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
func mergeCodeRabbit(ctx context.Context, data []byte, evidence bool) ([]byte, error) {
	text, crlf, err := util.NormalizeLineEndingsStrict(string(data))
	if err != nil {
		return nil, fmt.Errorf("read CodeRabbit line endings: %w", err)
	}
	output := []byte(text)
	for _, key := range [3]string{"drafts", "base_branches", "path_instructions"} {
		output, err = setCodeRabbitKey(ctx, output, key, evidence)
		if err != nil {
			return nil, err
		}
	}
	return []byte(util.RestoreLineEndings(string(output), crlf)), nil
}

// setCodeRabbitKey preserves correct values byte for byte and patches or encodes one changed key.
func setCodeRabbitKey(ctx context.Context, data []byte, key string, evidence bool) ([]byte, error) {
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
	value, changed, err := codeRabbitKeyValue(key, old, evidence)
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
func codeRabbitKeyValue(key string, old *yaml.Node, evidence bool) (*yaml.Node, bool, error) {
	value := &yaml.Node{}
	if old != nil {
		*value = *old
	}
	if key == "drafts" {
		return codeRabbitDraftValue(old, value)
	}
	if key == "path_instructions" {
		return codeRabbitInstructionsValue(old, value, evidence)
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
func codeRabbitInstructionsValue(old, value *yaml.Node, evidence bool) (*yaml.Node, bool, error) {
	global, err := validateCodeRabbitInstructions(old)
	if err != nil {
		return value, false, err
	}
	if global {
		return refreshCodeRabbitEvidence(old, value, evidence)
	}
	if len(value.Content) == maxAdoptManifestMappingNodes {
		return nil, false, fmt.Errorf("CodeRabbit path_instructions has no room within %d entries", maxAdoptManifestMappingNodes)
	}
	var defaults yaml.Node
	if err := util.DecodeYAMLDocument([]byte(codeRabbitConfiguration(evidence)), &defaults, util.YAMLDocumentOptions{}); err != nil {
		return nil, false, fmt.Errorf("decode CodeRabbit default instructions: %w", err)
	}
	instructions := util.YAMLMappingValue(util.YAMLMappingValue(defaults.Content[0], "reviews"), "path_instructions")
	value.Kind, value.Tag, value.Value = yaml.SequenceNode, "!!seq", ""
	value.Content = append(append([]*yaml.Node(nil), value.Content...), instructions.Content[0])
	return value, true, nil
}

// refreshCodeRabbitEvidence updates only the exact managed global instruction, retaining user policy.
func refreshCodeRabbitEvidence(old, value *yaml.Node, evidence bool) (*yaml.Node, bool, error) {
	managed := make(map[string]bool, 2)
	desired := ""
	for _, hasStep := range [2]bool{false, true} {
		var defaults yaml.Node
		if err := util.DecodeYAMLDocument([]byte(codeRabbitConfiguration(hasStep)), &defaults, util.YAMLDocumentOptions{}); err != nil {
			return nil, false, err
		}
		instructions := util.YAMLMappingValue(util.YAMLMappingValue(defaults.Content[0], "reviews"), "path_instructions")
		text := util.YAMLMappingValue(instructions.Content[0], "instructions").Value
		managed[text] = true
		if hasStep == evidence {
			desired = text
		}
	}
	for i := 0; i < len(old.Content) && i < maxAdoptManifestMappingNodes; i++ {
		entry := old.Content[i]
		text := util.YAMLMappingValue(entry, "instructions")
		if util.YAMLMappingValue(entry, "path").Value == "**" && managed[text.Value] && text.Value != desired {
			text.Value = desired
			return value, true, nil
		}
	}
	return value, false, nil
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

// maxCodeRabbitInstructions is the schema v2 maxLength of reviews.path_instructions[].instructions, in characters.
const maxCodeRabbitInstructions = 20000

// codeRabbitInstructionPath validates the schema's required string fields and length and returns the glob.
func codeRabbitInstructionPath(entry *yaml.Node) (string, error) {
	for _, key := range [2]string{"path", "instructions"} {
		field := util.YAMLMappingValue(entry, key)
		if field == nil || field.Kind != yaml.ScalarNode || field.Tag != "!!str" {
			return "", fmt.Errorf("%s must be a string", key)
		}
	}
	if count := utf8.RuneCountInString(util.YAMLMappingValue(entry, "instructions").Value); count > maxCodeRabbitInstructions {
		return "", fmt.Errorf("instructions have %d characters; the CodeRabbit schema allows at most %d", count, maxCodeRabbitInstructions)
	}
	return util.YAMLMappingValue(entry, "path").Value, nil
}
