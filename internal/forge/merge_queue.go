// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"gopkg.in/yaml.v3"
)

const (
	// mergeGroupEvent is the event GitHub raises for a merge group. A required check reports for
	// a group only when its workflow triggers on it (#893).
	mergeGroupEvent = ghworkflow.HostedGateMergeGroup
	// MergeQueueRule and codeScanningRule are the ruleset rule types a merge queue renders.
	MergeQueueRule   = "merge_queue"
	codeScanningRule = "code_scanning"
	// Parameters of the merge_queue rule. GitHub requires all seven (REST reference, schema
	// repository-rule-merge-queue, read 2026-10-08); these are its documented defaults, except
	// that the merge method keeps a linear history linear (queueMergeMethod).
	queueCheckTimeoutMinutes = 60
	queueMaxEntriesToBuild   = 5
	queueMaxEntriesToMerge   = 5
	queueMinEntriesToMerge   = 1
	queueMinEntriesWaitMins  = 5
)

// ContextOption changes how the required status contexts of the workflows are selected.
type ContextOption func(*contextOptions)

type contextOptions struct{ mergeQueue bool }

// ForMergeQueue selects, when on, only the contexts of workflows that trigger on merge_group:
// on a branch protected by a merge queue a context of any other workflow is never reported for
// a group, so every queued group would wait for it until the queue's check timeout and be
// removed. MergeQueueFindings names the workflows it leaves out.
func ForMergeQueue(on bool) ContextOption {
	return func(o *contextOptions) { o.mergeQueue = on }
}

func newContextOptions(opts []ContextOption) contextOptions {
	var resolved contextOptions
	for i := 0; i < len(opts) && i < maxJobsPerFile; i++ {
		opts[i](&resolved)
	}
	return resolved
}

// MergeQueueFinding is one workflow whose jobs are required checks on a pull request but that
// does not trigger on merge_group: Workflow is its repository-relative slash path, Contexts the
// check contexts it would require.
type MergeQueueFinding struct {
	Workflow string
	Contexts []string
}

// Trigger is the trigger the workflow lacks.
func (MergeQueueFinding) Trigger() string { return mergeGroupEvent }

// String names the workflow, the missing trigger and the contexts that never report.
func (f MergeQueueFinding) String() string {
	return fmt.Sprintf("%s: no %s trigger, so %s never report for a merge group", f.Workflow, mergeGroupEvent,
		strings.Join(f.Contexts, ", "))
}

// triggersOnMergeGroup reports whether an "on" node declares a merge_group trigger.
func triggersOnMergeGroup(on *yaml.Node) bool {
	_, declared := eventTrigger(on, mergeGroupEvent)
	return declared
}

// splitMergeGroupFiles partitions files into the workflows that trigger on merge_group and the
// findings for those that do not and would otherwise require a context in the repository named
// identity. A workflow that requires no context loses nothing in a queue and is no finding.
func splitMergeGroupFiles(files []workflowFile, identity string) (kept []workflowFile, findings []MergeQueueFinding, err error) {
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		var spec workflowSpec
		if err := yaml.Unmarshal(files[i].Data, &spec); err != nil {
			return nil, nil, fmt.Errorf("workflow %s: parse: %w", files[i].Name, err)
		}
		if triggersOnMergeGroup(&spec.On) {
			kept = append(kept, files[i])
			continue
		}
		contexts, err := workflowContextsIn(files[i].Data, identity)
		if err != nil {
			return nil, nil, fmt.Errorf("workflow %s: %w", files[i].Name, err)
		}
		if len(contexts) > 0 {
			findings = append(findings, MergeQueueFinding{Workflow: plannedWorkflowDir + files[i].Name, Contexts: contexts})
		}
	}
	return kept, findings, nil
}

// selectContexts collects the required contexts of files inside the repository named identity,
// leaving out the workflows a merge queue never runs when opts asks for a queue.
func selectContexts(files []workflowFile, identity string, opts contextOptions) ([]string, error) {
	if opts.mergeQueue {
		kept, _, err := splitMergeGroupFiles(files, identity)
		if err != nil {
			return nil, err
		}
		files = kept
	}
	return filesContextsIn(files, identity)
}

// MergeQueueFindings lists, for the repository at repoPath with planned applied over its
// workflows (RequiredStatusContextsPlanned), every workflow that requires a pull request check
// but lacks the merge_group trigger. A nil planned reads the workflows on disk alone.
func MergeQueueFindings(ctx context.Context, repoPath string, planned map[string][]byte) ([]MergeQueueFinding, error) {
	if ctx == nil {
		return nil, errors.New("merge queue findings require a context")
	}
	ctx, cancel := context.WithTimeout(ctx, workflowDiscoveryTimeout)
	defer cancel()
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	files, err = overlayWorkflowFiles(files, planned)
	if err != nil {
		return nil, err
	}
	identity, err := guardIdentity(files, repoPath)
	if err != nil {
		return nil, err
	}
	_, findings, err := splitMergeGroupFiles(files, identity)
	return findings, err
}

// QueueOmissions names, as report lines, the workflows a merge queue leaves out of the required
// status checks of the repository at repoPath (ForMergeQueue), with planned applied over its
// workflows: each has jobs that report on a pull request but no merge_group trigger, so its
// checks are not required and nothing gates a merge on them. It is empty when on is false, so a
// writer or sync prints it without asking whether the repository has a queue.
func QueueOmissions(ctx context.Context, repoPath string, planned map[string][]byte, on bool) ([]string, error) {
	if !on {
		return nil, nil
	}
	findings, err := MergeQueueFindings(ctx, repoPath, planned)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(findings))
	for i := 0; i < len(findings) && i < maxWorkflowFiles; i++ {
		lines = append(lines, fmt.Sprintf("%s: not required by the merge queue ruleset; add `%s:` to the workflow to gate merges on %s",
			findings[i].Workflow, mergeGroupEvent, strings.Join(findings[i].Contexts, ", ")))
	}
	return lines, nil
}

// RequiredWithoutMergeGroup narrows findings to the workflows one of whose contexts is in
// required, the contexts a branch protected by a merge queue requires, and to those contexts.
// The result is empty when the branch requires nothing a group cannot report.
func RequiredWithoutMergeGroup(findings []MergeQueueFinding, required []string) []MergeQueueFinding {
	var stuck []MergeQueueFinding
	for i := 0; i < len(findings) && i < maxWorkflowFiles; i++ {
		var contexts []string
		for _, check := range findings[i].Contexts {
			if slices.Contains(required, check) {
				contexts = append(contexts, check)
			}
		}
		if len(contexts) > 0 {
			stuck = append(stuck, MergeQueueFinding{Workflow: findings[i].Workflow, Contexts: contexts})
		}
	}
	return stuck
}

// queueMergeMethod is the merge method of the queue rule: merge commits break a branch that
// requires a linear history, so a linear history merges by squash.
func queueMergeMethod(linearHistory bool) string {
	if linearHistory {
		return "SQUASH"
	}
	return "MERGE"
}

// mergeQueueRuleOf is the merge_queue rule: all seven parameters GitHub requires, set to their
// documented defaults.
func mergeQueueRuleOf(linearHistory bool) map[string]any {
	return map[string]any{"type": MergeQueueRule, "parameters": map[string]any{
		"check_response_timeout_minutes":    queueCheckTimeoutMinutes,
		"grouping_strategy":                 "ALLGREEN",
		"max_entries_to_build":              queueMaxEntriesToBuild,
		"max_entries_to_merge":              queueMaxEntriesToMerge,
		"merge_method":                      queueMergeMethod(linearHistory),
		"min_entries_to_merge":              queueMinEntriesToMerge,
		"min_entries_to_merge_wait_minutes": queueMinEntriesWaitMins,
	}}
}

// codeScanningRuleOf gates pull requests on the CodeQL results of default setup. Default setup
// never runs for a merge group and has no workflow, so it can be no required status context;
// this rule is how a queue-protected branch requires its results.
func codeScanningRuleOf() map[string]any {
	return map[string]any{"type": codeScanningRule, "parameters": map[string]any{
		"code_scanning_tools": []map[string]string{{
			"tool": "CodeQL", "alerts_threshold": "errors", "security_alerts_threshold": "high_or_higher",
		}},
	}}
}

// RulesetHasRule reports whether a ruleset document carries a rule of type ruleType.
func RulesetHasRule(data []byte, ruleType string) (bool, error) {
	ruleset, err := parseStatusRuleset(data)
	if err != nil {
		return false, err
	}
	for i := 0; i < len(ruleset.Rules) && i < maxRulesetRules; i++ {
		if ruleset.Rules[i].Type == ruleType {
			return true, nil
		}
	}
	return false, nil
}

// RulesetStatusContexts lists the status contexts a ruleset document requires.
func RulesetStatusContexts(data []byte) ([]string, error) {
	ruleset, err := parseStatusRuleset(data)
	if err != nil {
		return nil, err
	}
	var contexts []string
	for i := 0; i < len(ruleset.Rules) && i < maxRulesetRules; i++ {
		checks := ruleset.Rules[i].Parameters.RequiredStatusChecks
		if ruleset.Rules[i].Type != "required_status_checks" || len(checks) > maxRulesetContexts {
			continue
		}
		for j := 0; j < len(checks) && j < maxRulesetContexts; j++ {
			contexts = append(contexts, checks[j].Context)
		}
	}
	return contexts, nil
}

// HasActiveRule reports whether an active rule of type ruleType applies to the branch live
// describes, from any ruleset.
func (l *LiveBranchProtection) HasActiveRule(ruleType string) bool {
	for i := 0; i < len(l.Rules) && i < maxBranchRules; i++ {
		if l.Rules[i].Type == ruleType {
			return true
		}
	}
	return false
}

// LiveMergeQueueFindings lists, for a branch protected live by a merge queue, the workflows of
// the repository at repoPath that lack merge_group while one of their contexts is a status
// check the branch requires: no group ever reports it, so every queued group waits for it until
// the queue's check timeout. It is empty for a branch without an active merge_queue rule.
func LiveMergeQueueFindings(ctx context.Context, repoPath string, live *LiveBranchProtection) ([]MergeQueueFinding, error) {
	if live == nil || !live.HasActiveRule(MergeQueueRule) {
		return nil, nil
	}
	enforcement, err := collectEnforcement(live)
	if err != nil {
		return nil, err
	}
	required := make([]string, 0, len(enforcement.checks))
	for check := range enforcement.checks {
		required = append(required, check)
	}
	slices.Sort(required)
	findings, err := MergeQueueFindings(ctx, repoPath, nil)
	if err != nil {
		return nil, err
	}
	return RequiredWithoutMergeGroup(findings, required), nil
}

// MergeQueueFailure is the audit failure for findings on a branch protected by a merge queue,
// or "" when there are none. It names each workflow path and the trigger it lacks. subject
// says what requires the contexts, such as "the committed ruleset".
func MergeQueueFailure(subject string, findings []MergeQueueFinding) string {
	if len(findings) == 0 {
		return ""
	}
	lines := make([]string, 0, len(findings))
	for i := 0; i < len(findings) && i < maxWorkflowFiles; i++ {
		lines = append(lines, "  "+findings[i].String())
	}
	return fmt.Sprintf("%s requires status checks on a branch protected by a merge queue whose workflows lack the "+
		"%s trigger; add `%s:` to each workflow or drop the check:\n%s", subject, mergeGroupEvent, mergeGroupEvent,
		strings.Join(lines, "\n"))
}

// UndeclaredMergeQueueFailure is the audit failure for a branch on which an active merge_queue
// rule applies while the policy does not declare one: the declared key
// branch_protection.merge_queue alone selects the merge_group contexts, so a sync would still
// require checks no merge group reports. It names the branch and the remedy.
func UndeclaredMergeQueueFailure(branch string) string {
	return fmt.Sprintf("Live branch protection of %s carries an active merge_queue rule that the policy does not declare; "+
		"declare `overrides.branch_protection.merge_queue: true` in .standards.yaml and run 'praetorctl sync --remote' "+
		"so the required status checks are those of the workflows that trigger on %s", branch, mergeGroupEvent)
}
