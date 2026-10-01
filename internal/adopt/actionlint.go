// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// actionlint rejects a runs-on label missing from its built-in table of GitHub-hosted runners
// unless the repository's configuration declares it under self-hosted-runner.labels, the one
// declaration it accepts for such a label (measured with actionlint v1.7.12, #593). Adoption
// reads the runs-on labels of every workflow it writes, the documentation gate audit locks and
// the detected flavor's CI workflows alike (adoptedWorkflowFiles), and declares each one
// actionlint does not know (actionlintUnknownLabels), so a repository that lints its workflows
// with actionlint stays green without editing what adoption wrote (#622).
//
// actionlint reads .github/actionlint.yaml and, only while that file is absent,
// .github/actionlint.yml (measured with v1.7.12: with both present, a label declared only in
// the .yml file is still rejected). Adoption edits the file actionlint reads, and creates
// .github/actionlint.yaml when there is none. It only adds a missing label. A label counts as
// declared when the list holds it or a pattern of the adopter's matches it (path.Match), the
// matching actionlint applies too: measured with v1.7.12, `ubuntu-2?.04` declares ubuntu-26.04
// and the brace pattern `ubuntu-{26,27}.04` does not, since path.Match expands no braces.
// Adoption never removes a label, one it added included: the adopter's own
// workflows may run on it too. Once actionlint ships a label and actionlintUnknownLabels stops
// naming it, adoption stops adding it, and the header of a created file says when a label can go.
//
// The edit is a text patch that keeps every other line as written (manifestText). It extends a
// block-style configuration: an empty one, one without self-hosted-runner, a null or block
// self-hosted-runner mapping, and a labels value that is null, a block sequence or a one-line
// flow sequence such as the `labels: []` actionlint -init-config writes. The patch is kept only
// when the patched text decodes to exactly the original document with the missing labels
// appended. Any other file (mixed line endings, several documents, anchors, aliases, duplicate
// keys, a flow-style mapping, a labels value of another kind, a multi-line flow sequence) is
// reported with the labels to declare by hand and left untouched; so is one adoption may not
// read safely.
const (
	// actionlintConfigFile is the configuration actionlint reads first, and the one adoption
	// creates.
	actionlintConfigFile = ".github/actionlint.yaml"
	// actionlintRunnerKey and actionlintLabelsKey spell self-hosted-runner.labels.
	actionlintRunnerKey = "self-hosted-runner"
	actionlintLabelsKey = "labels"
	// actionlintHeader opens a configuration adoption creates: a yamllint document start and
	// why the file exists, within yamllint's default 80 columns.
	actionlintHeader = "---\n" +
		"# praetorctl adopt wrote this file for actionlint, which rejects a runs-on label\n" +
		"# missing from its built-in table of GitHub-hosted runners unless it is declared\n" +
		"# under self-hosted-runner.labels. Each label below is one a Praetor-managed\n" +
		"# workflow runs on that actionlint v1.7.12 does not know. Adoption only adds a\n" +
		"# missing label and never removes one, so keep your own settings here, and\n" +
		"# delete a label once your actionlint accepts .github/workflows without it.\n"
)

// maxActionlintLabels bounds the labels adoption declares to actionlint in one run (HISS-02).
const maxActionlintLabels = 8

// actionlintUnknownLabels are the runner labels Praetor-emitted workflows run on that
// actionlint's built-in table of GitHub-hosted runner labels (rule_runner_label.go) lacks: the
// table stops at ubuntu-24.04 in v1.7.12. The list records what actionlint does not know, never
// which workflow runs on what: adoption reads that from each workflow it writes
// (actionlintManagedLabels), so a template moving to another runner needs no edit here unless
// actionlint does not know that runner either. Once actionlint ships a label, remove it here and
// adoption stops adding it. Wherever actionlint is on PATH,
// TestActionlintStillRejectsTheDeclaredLabels fails as soon as it accepts a label listed here,
// and TestActionlintAcceptsEveryEmittedWorkflow as soon as an emitted workflow runs on a label
// it does not know that is missing here.
var actionlintUnknownLabels = [...]string{"ubuntu-26.04"}

// actionlintConfigFiles are actionlint's repository configuration files in its own search
// order; it reads the first that exists.
var actionlintConfigFiles = [...]string{actionlintConfigFile, ".github/actionlint.yml"}

// reconcileActionlintLabels declares to actionlint the runner labels of the workflows this run
// writes that it does not know.
func reconcileActionlintLabels(ctx context.Context, s *adoptSession) error {
	labels, err := actionlintManagedLabels(ctx, s)
	if err != nil {
		return err
	}
	if len(labels) == 0 {
		s.report.recordNotApplicable(actionlintConfigFile, "No Praetor-managed workflow runs on a label actionlint does not know")
		return nil
	}
	rel, data, exists, err := findActionlintConfig(ctx, s, labels)
	if err != nil || rel == "" {
		return err
	}
	if !exists {
		_, err := s.scaffoldFile(ctx, scaffold{
			rel: rel, perm: filePerm, content: renderActionlintConfig(labels),
			created:  "Declared to actionlint the runner labels of the Praetor-managed workflows it does not know: " + strings.Join(labels, ", "),
			verified: "actionlint already accepts the runner labels of the Praetor-managed workflows",
		})
		return err
	}
	merged, added, err := mergeActionlintLabels(ctx, data, labels)
	var refusal actionlintRefusal
	switch {
	case errors.As(err, &refusal):
		return reportActionlintUnsafe(s, rel, labels, string(refusal))
	case err != nil:
		return err
	case len(added) == 0:
		s.report.recordReconciled(rel, "actionlint already accepts the runner labels of the Praetor-managed workflows")
		return nil
	}
	return publishActionlintConfig(ctx, s, rel, data, merged, added)
}

// actionlintManagedLabels returns the runner labels actionlint does not know that the workflows
// this run leaves as adoption's rendering run on (adoptedWorkflowFiles): the hosted workflow of
// every managed asset family enabled for the repository (enabledManagedFamiliesForSession), the
// Go API compatibility gate's only where git tracks a go.mod, and the detected flavor's CI
// workflows unless the flavor step is declined. The step runs before the steps that write them,
// so it reads what they will write.
func actionlintManagedLabels(ctx context.Context, s *adoptSession) ([]string, error) {
	families, err := enabledManagedFamiliesForSession(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("resolve the managed gates for actionlint: %w", err)
	}
	files, err := s.adoptedWorkflowFiles(ctx, families)
	if err != nil {
		return nil, err
	}
	return actionlintLabelsOf(files)
}

// actionlintLabelsOf returns the labels of actionlintUnknownLabels, in its order, that any of
// files runs a job on (forge.WorkflowRunnerLabels).
func actionlintLabelsOf(files []flavor.PlannedTemplate) ([]string, error) {
	var runners []string
	for index := 0; index < len(files) && index < maxScaffoldedWorkflows; index++ {
		labels, err := forge.WorkflowRunnerLabels([]byte(files[index].Content))
		if err != nil {
			return nil, fmt.Errorf("read the runner labels of %s: %w", files[index].Path, err)
		}
		runners = append(runners, labels...)
	}
	unknown := make([]string, 0, len(actionlintUnknownLabels))
	for index := 0; index < len(actionlintUnknownLabels) && index < maxActionlintLabels; index++ {
		if slices.Contains(runners, actionlintUnknownLabels[index]) {
			unknown = append(unknown, actionlintUnknownLabels[index])
		}
	}
	return unknown, nil
}

// findActionlintConfig returns the configuration actionlint reads, its bytes, and whether it
// exists; without one, the path adoption creates. A candidate that exists but cannot be read
// under the adoption read contract is reported and stops the search with an empty path: it is
// the file actionlint would read, so no later one is edited.
func findActionlintConfig(ctx context.Context, s *adoptSession, labels []string) (string, []byte, bool, error) {
	for index := 0; index < len(actionlintConfigFiles); index++ {
		rel := actionlintConfigFiles[index]
		data, exists, err := observeAdoptionInput(ctx, s, rel)
		if err != nil {
			reason, err := uninspectableReason(ctx, err)
			if err != nil {
				return "", nil, false, err
			}
			return "", nil, false, reportActionlintUnsafe(s, rel, labels, reason)
		}
		if exists {
			return rel, data, true, nil
		}
	}
	return actionlintConfigFile, nil, false, nil
}

// reportActionlintUnsafe records a configuration left untouched, with the labels to declare by
// hand. Such a file never fails adoption, so it returns no error.
func reportActionlintUnsafe(s *adoptSession, rel string, labels []string, reason string) error {
	s.report.recordSkipped(rel, "actionlint configuration left untouched: "+reason+"; declare "+
		strings.Join(labels, ", ")+" under its self-hosted-runner.labels so actionlint accepts the Praetor-managed workflows")
	return nil
}

// publishActionlintConfig writes merged over data, bound to the observed bytes, and reports the
// labels it added. A dry run reports the write it would make.
func publishActionlintConfig(ctx context.Context, s *adoptSession, rel string, data, merged []byte, added []string) error {
	if !s.opts.DryRun {
		full, err := repoFile(s.repoPath, rel)
		if err != nil {
			return err
		}
		options := contextopt.ReplaceOptions{Expected: data, Exists: true, Mode: filePerm}
		if err := contextopt.ReplaceSnapshot(ctx, full, merged, options); err != nil {
			return fmt.Errorf("update %s: %w", rel, err)
		}
	}
	s.planDryRunWrite(rel, merged)
	s.report.recordReconciledAs(rel, actionMerge, "Declared "+strings.Join(added, ", ")+
		" under self-hosted-runner.labels, runner labels of the Praetor-managed workflows actionlint does not know; every other line is kept")
	return nil
}

// renderActionlintConfig returns the configuration adoption creates.
func renderActionlintConfig(labels []string) []byte {
	return []byte(actionlintHeader + actionlintRunnerKey + ":\n  " + actionlintLabelsKey + ":\n" +
		actionlintItems(labels, "    ", "\n"))
}

// actionlintItems renders labels as block sequence items at indent, one line each.
func actionlintItems(labels []string, indent, eol string) string {
	var out strings.Builder
	for index := 0; index < len(labels) && index < maxActionlintLabels; index++ {
		out.WriteString(indent)
		out.WriteString("- ")
		out.WriteString(labels[index])
		out.WriteString(eol)
	}
	return out.String()
}

// actionlintRefusal is why mergeActionlintLabels leaves a configuration alone. The step reports
// it with the labels to declare by hand; it never fails adoption.
type actionlintRefusal string

func (r actionlintRefusal) Error() string { return string(r) }

// actionlintLayout locates self-hosted-runner.labels in a decoded configuration. root is the
// top-level block mapping, nil for a file without content; the other nodes are nil where the
// file lacks them.
type actionlintLayout struct {
	root, runnerKey, runner, labelsKey, labels *yaml.Node
	runnerAt, labelsAt                         int
}

// mergeActionlintLabels returns data with every label it does not declare yet appended to
// self-hosted-runner.labels, in data's line-ending style, and those labels; no bytes and no
// labels when every label is declared. An actionlintRefusal names a file it will not patch;
// any other error is the adoption context ending.
func mergeActionlintLabels(ctx context.Context, data []byte, labels []string) ([]byte, []string, error) {
	text, crlf, err := util.NormalizeLineEndingsStrict(string(data))
	if err != nil {
		return nil, nil, actionlintRefusal("its line endings are mixed (" + err.Error() + ")")
	}
	layout, err := readActionlintLayout(ctx, text)
	if err != nil {
		return nil, nil, err
	}
	missing := missingActionlintLabels(layout.labels, labels)
	if len(missing) == 0 {
		return nil, nil, nil
	}
	patched, ok := patchActionlintLabels(newManifestText([]byte(text)), layout, missing)
	if !ok || !actionlintPatchExact(text, patched, missing) {
		return nil, nil, actionlintRefusal("its layout is not one adoption extends in place")
	}
	return []byte(util.RestoreLineEndings(patched, crlf)), missing, nil
}

// readActionlintLayout decodes text, one YAML document or none, and locates the labels list.
func readActionlintLayout(ctx context.Context, text string) (actionlintLayout, error) {
	var document yaml.Node
	if err := util.DecodeYAMLDocument([]byte(text), &document, util.YAMLDocumentOptions{AllowEmpty: true}); err != nil {
		return actionlintLayout{}, actionlintRefusal("it is not one YAML document (" + err.Error() + ")")
	}
	if document.Kind == 0 || len(document.Content) != 1 || nullYAMLNode(document.Content[0]) {
		return actionlintLayout{}, nil
	}
	if err := validateActionlintNodes(ctx, &document); err != nil {
		return actionlintLayout{}, err
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode || root.Style&yaml.FlowStyle != 0 || root.Column != 1 {
		return actionlintLayout{}, actionlintRefusal("its top level is not a block mapping")
	}
	layout := actionlintLayout{root: root, runnerAt: mappingKeyIndex(root, actionlintRunnerKey)}
	if layout.runnerAt < 0 {
		return layout, nil
	}
	layout.runnerKey, layout.runner = root.Content[layout.runnerAt], root.Content[layout.runnerAt+1]
	return locateActionlintLabels(layout)
}

// validateActionlintNodes holds document to the adoption YAML node contract. It returns the
// context error when the adoption context ended, because then nothing was observed, and an
// actionlintRefusal for a document that uses anchors, aliases or keys that are not unique strings.
func validateActionlintNodes(ctx context.Context, document *yaml.Node) error {
	err := config.ValidateYAMLNodes(ctx, document)
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return actionlintRefusal("it uses anchors, aliases or keys that are not unique strings (" + err.Error() + ")")
}

// locateActionlintLabels fills the labels key and value of a layout whose self-hosted-runner
// value it has, refusing a value that is neither null nor a block mapping and a labels value
// that is neither null nor a sequence.
func locateActionlintLabels(layout actionlintLayout) (actionlintLayout, error) {
	if nullYAMLNode(layout.runner) {
		return layout, nil
	}
	if layout.runner.Kind != yaml.MappingNode || layout.runner.Style&yaml.FlowStyle != 0 {
		return layout, actionlintRefusal("its self-hosted-runner is not a block mapping")
	}
	layout.labelsAt = mappingKeyIndex(layout.runner, actionlintLabelsKey)
	if layout.labelsAt < 0 {
		return layout, nil
	}
	layout.labelsKey, layout.labels = layout.runner.Content[layout.labelsAt], layout.runner.Content[layout.labelsAt+1]
	if !nullYAMLNode(layout.labels) && layout.labels.Kind != yaml.SequenceNode {
		return layout, actionlintRefusal("its self-hosted-runner.labels is not a list")
	}
	return layout, nil
}

// missingActionlintLabels returns, in order, the labels no entry of declared, a labels sequence
// or nil, names or matches as a pattern.
func missingActionlintLabels(declared *yaml.Node, labels []string) []string {
	var entries []*yaml.Node
	if declared != nil && declared.Kind == yaml.SequenceNode {
		entries = declared.Content
	}
	missing := make([]string, 0, len(labels))
	for index := 0; index < len(labels) && index < maxActionlintLabels; index++ {
		covered := slices.ContainsFunc(entries, func(entry *yaml.Node) bool {
			matched, err := path.Match(entry.Value, labels[index])
			return entry.Kind == yaml.ScalarNode && (entry.Value == labels[index] || err == nil && matched)
		})
		if !covered {
			missing = append(missing, labels[index])
		}
	}
	return missing
}

// patchActionlintLabels inserts missing into text at the place layout names, indented the way
// the file indents its nested mappings, and reports false for a layout it does not patch.
func patchActionlintLabels(text manifestText, layout actionlintLayout, missing []string) (string, bool) {
	unit := 2
	if layout.root != nil {
		unit = manifestIndentUnit(layout.root)
	}
	switch {
	case layout.runnerKey == nil:
		block := actionlintRunnerKey + ":\n" + strings.Repeat(" ", unit) + actionlintLabelsKey + ":\n" +
			actionlintItems(missing, strings.Repeat(" ", 2*unit), "\n")
		return string(text.splice(len(text.lines), len(text.lines), block)), true
	case nullYAMLNode(layout.runner):
		indent := strings.Repeat(" ", layout.runnerKey.Column-1+unit)
		block := indent + actionlintLabelsKey + ":\n" + actionlintItems(missing, indent+strings.Repeat(" ", unit), "\n")
		return replaceNullValueLine(text, layout.runnerKey, layout.runner, block)
	case layout.labelsKey == nil:
		return insertActionlintLabelsKey(text, layout, missing, unit)
	case nullYAMLNode(layout.labels):
		block := actionlintItems(missing, strings.Repeat(" ", layout.labelsKey.Column-1+unit), "\n")
		return replaceNullValueLine(text, layout.labelsKey, layout.labels, block)
	case layout.labels.Style&yaml.FlowStyle != 0:
		return extendFlowLabels(text, layout, missing)
	}
	boundary := nextKeyLine(layout.root, layout.runnerAt, len(text.lines)+1)
	end := text.blockEnd(nextKeyLine(layout.runner, layout.labelsAt, boundary))
	block := actionlintItems(missing, strings.Repeat(" ", layout.labels.Column-1), "\n")
	return string(text.splice(end, end, block)), true
}

// insertActionlintLabelsKey appends a labels key holding missing to the end of the block
// self-hosted-runner mapping, at the indentation of its first key.
func insertActionlintLabelsKey(text manifestText, layout actionlintLayout, missing []string, unit int) (string, bool) {
	runner := layout.runner
	if len(runner.Content) == 0 || runner.Content[0].Line <= layout.runnerKey.Line {
		return "", false
	}
	indent := strings.Repeat(" ", runner.Content[0].Column-1)
	block := indent + actionlintLabelsKey + ":\n" + actionlintItems(missing, indent+strings.Repeat(" ", unit), "\n")
	end := text.blockEnd(nextKeyLine(layout.root, layout.runnerAt, len(text.lines)+1))
	return string(text.splice(end, end, block)), true
}

// replaceNullValueLine drops the null token from the line of key, keeping its comment, and
// inserts block right below it (nullRegisterKeyLine). A null written on a line of its own is
// not patched.
func replaceNullValueLine(text manifestText, key, value *yaml.Node, block string) (string, bool) {
	at := key.Line - 1
	if value.Line != key.Line || at < 0 || at >= len(text.lines) {
		return "", false
	}
	keyLine, ok := nullRegisterKeyLine(strings.TrimRight(text.lines[at], "\n"), value)
	if !ok {
		return "", false
	}
	return string(text.splice(at, at+1, keyLine+"\n"+block)), true
}

// extendFlowLabels appends missing inside a flow sequence written on one line, before its
// closing bracket and any trailing comment.
func extendFlowLabels(text manifestText, layout actionlintLayout, missing []string) (string, bool) {
	labels := layout.labels
	at := labels.Line - 1
	if at < 0 || at >= len(text.lines) || slices.ContainsFunc(labels.Content, func(entry *yaml.Node) bool { return entry.Line != labels.Line }) {
		return "", false
	}
	line := strings.TrimRight(text.lines[at], "\n")
	body, comment := line, ""
	if labels.LineComment != "" {
		cut := strings.LastIndex(line, labels.LineComment)
		if cut < 0 {
			return "", false
		}
		body, comment = line[:cut], line[cut:]
	}
	closing := strings.LastIndex(body, "]")
	if closing < labels.Column || strings.TrimSpace(body[closing+1:]) != "" {
		return "", false
	}
	separator := ", "
	if len(labels.Content) == 0 {
		separator = ""
	}
	patched := body[:closing] + separator + strings.Join(missing, ", ") + body[closing:] + comment + "\n"
	return string(text.splice(at, at+1, patched)), true
}

// actionlintPatchExact reports whether patched decodes to exactly the document text decodes
// to with missing appended to self-hosted-runner.labels, so a patch changes layout only where
// it adds the labels, never anything else.
func actionlintPatchExact(text, patched string, missing []string) bool {
	var before, after any
	options := util.YAMLDocumentOptions{AllowEmpty: true}
	if util.DecodeYAMLDocument([]byte(text), &before, options) != nil || util.DecodeYAMLDocument([]byte(patched), &after, options) != nil {
		return false
	}
	expected, ok := withActionlintLabels(before, missing)
	return ok && reflect.DeepEqual(expected, after)
}

// withActionlintLabels returns document, a decoded configuration, with missing appended to
// self-hosted-runner.labels, and false for a document of another shape.
func withActionlintLabels(document any, missing []string) (any, bool) {
	root, ok := document.(map[string]any)
	if document == nil {
		root, ok = map[string]any{}, true
	}
	if !ok {
		return nil, false
	}
	runner, ok := root[actionlintRunnerKey].(map[string]any)
	if root[actionlintRunnerKey] == nil {
		runner, ok = map[string]any{}, true
	}
	if !ok {
		return nil, false
	}
	labels, ok := runner[actionlintLabelsKey].([]any)
	if runner[actionlintLabelsKey] == nil {
		labels, ok = nil, true
	}
	if !ok {
		return nil, false
	}
	for index := 0; index < len(missing) && index < maxActionlintLabels; index++ {
		labels = append(labels, missing[index])
	}
	runner[actionlintLabelsKey] = labels
	root[actionlintRunnerKey] = runner
	return root, true
}
