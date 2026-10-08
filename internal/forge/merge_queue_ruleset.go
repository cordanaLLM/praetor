// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

// MergeQueueRulesetName names the Praetor-managed ruleset that carries the merge_queue rule and
// nothing else. The rule lives apart from the main ruleset (RepositoryRulesetName) because that
// one includes the lts-* wildcard, and GitHub documents that "a merge queue cannot be enabled
// with branch protection rules that use wildcard characters (*) in the branch name pattern"
// (https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue,
// read 2026-10-08). This ruleset targets the default branch alone, so it never holds a wildcard.
const MergeQueueRulesetName = RepositoryRulesetName + " merge queue"

// MergeQueueRulesetRefs returns the refs the merge queue ruleset includes for a repository whose
// default branch is branch: that branch alone.
func MergeQueueRulesetRefs(branch string) []string {
	return []string{"refs/heads/" + branch}
}

// RenderMergeQueueRuleset renders the merge queue ruleset for the default branch branch under
// policy: active, targeting MergeQueueRulesetRefs, with the merge_queue rule alone. sync --remote
// writes it when the policy declares a merge queue.
func RenderMergeQueueRuleset(branch string, policy config.BranchProtectionPolicy) (map[string]any, error) {
	if !config.ValidBranchName(branch) {
		return nil, fmt.Errorf("merge queue ruleset default branch %q is not a branch name", branch)
	}
	return normalizeRuleset(map[string]any{
		"name": MergeQueueRulesetName, "target": "branch", "enforcement": rulesetEnforcementActive,
		"conditions": map[string]any{"ref_name": map[string]any{"include": MergeQueueRulesetRefs(branch), "exclude": []string{}}},
		"rules":      []map[string]any{mergeQueueRuleOf(policy.EnforceLinearHistory)},
	})
}

// ReconcileMergeQueue converges the merge queue ruleset onto the policy, as
// ReconcileProtectionReport converges the main ruleset: created when absent, otherwise merged
// into the live one and updated in place, read back, and returned with the parameters the merge
// lowered. The union merge keeps every ref the live ruleset includes, so a wildcard an operator
// added stays and the audit reports it (MergeQueueRulesetFindings).
func (g *GitHubDriver) ReconcileMergeQueue(ctx context.Context, branch string, policy *config.BranchProtectionPolicy) ([]LoweredParameter, error) {
	if err := g.Authenticate(ctx); err != nil {
		return nil, err
	}
	if policy == nil {
		return nil, errors.New("reconcile merge queue: policy cannot be nil")
	}
	desired, err := RenderMergeQueueRuleset(branch, *policy)
	if err != nil {
		return nil, err
	}
	listPath, err := g.repoPath("rulesets")
	if err != nil {
		return nil, fmt.Errorf("reconcile merge queue for %s: %w", branch, err)
	}
	return g.convergeRuleset(ctx, branch, listPath, MergeQueueRulesetName, desired)
}

// LiveQueueRuleset is the merge queue ruleset (MergeQueueRulesetName) as GitHub reports it.
type LiveQueueRuleset struct {
	ID          int
	Enforcement string
	// Includes are the ref patterns the ruleset includes.
	Includes []string
}

// Wildcards returns the includes that are patterns instead of one branch: GitHub does not
// enable a merge queue under them.
func (r *LiveQueueRuleset) Wildcards() []string {
	var wild []string
	for i := 0; i < len(r.Includes) && i < maxRulesetRefs; i++ {
		if strings.ContainsAny(r.Includes[i], "*?[") || r.Includes[i] == "~ALL" {
			wild = append(wild, r.Includes[i])
		}
	}
	return wild
}

// readQueueRuleset reads the merge queue ruleset named in the listing (names), taking the lowest
// id of two with the name, or returns nil when the repository has none.
func (g *GitHubDriver) readQueueRuleset(ctx context.Context, names, enforcement map[int]string) (*LiveQueueRuleset, error) {
	id := 0
	for rulesetID, name := range names {
		if name == MergeQueueRulesetName && (id == 0 || rulesetID < id) {
			id = rulesetID
		}
	}
	if id == 0 {
		return nil, nil
	}
	listPath, err := g.repoPath("rulesets")
	if err != nil {
		return nil, err
	}
	ruleset, err := g.getRuleset(ctx, listPath, id)
	if err != nil {
		return nil, fmt.Errorf("read ruleset %q: %w", MergeQueueRulesetName, err)
	}
	includes, err := rulesetIncludes(ruleset)
	if err != nil {
		return nil, err
	}
	return &LiveQueueRuleset{ID: id, Enforcement: enforcement[id], Includes: includes}, nil
}

// MergeQueueRulesetFindings compares the live merge queue with a policy that declares one: an
// active merge_queue rule must apply to the branch, and the merge queue ruleset must hold no
// wildcard include. It is empty for a policy without a merge queue: the audit fails an
// undeclared live queue itself (UndeclaredMergeQueueFailure), and sync --remote writes nothing
// for it.
func MergeQueueRulesetFindings(policy config.BranchProtectionPolicy, live *LiveBranchProtection) []ProtectionFinding {
	if !policy.MergeQueue {
		return nil
	}
	var by []string
	for i := 0; i < len(live.Rules) && i < maxBranchRules; i++ {
		if live.Rules[i].Type == MergeQueueRule {
			by = appendMissing(by, []string{live.RulesetLabel(live.Rules[i])})
		}
	}
	findings := []ProtectionFinding{flagFinding("Merge queue", true, liveFlag{on: len(by) > 0, by: by})}
	if live.QueueRuleset == nil {
		return findings
	}
	wild := live.QueueRuleset.Wildcards()
	finding := ProtectionFinding{Property: "Merge queue ruleset refs", Declared: "no wildcard (GitHub enables no merge queue under one)",
		Live: "no wildcard", Verdict: ProtectionEnforced}
	if len(wild) > 0 {
		finding.Live, finding.Verdict = "wildcard "+strings.Join(wild, ", "), ProtectionDrift
	}
	return append(findings, finding)
}

// UndeclaredQueueRulesetNote says, for a policy that declares no merge queue, that the merge
// queue ruleset is still on GitHub: sync --remote never deletes it.
func UndeclaredQueueRulesetNote(branch string) string {
	return fmt.Sprintf("ruleset %q still exists on GitHub while the policy declares no merge queue; sync --remote never "+
		"removes it, so declare `overrides.branch_protection.merge_queue: true` or delete it by hand (the audit fails %s until then)",
		MergeQueueRulesetName, branch)
}

// rulesetIncludes returns the conditions.ref_name.include patterns of a ruleset document.
func rulesetIncludes(ruleset map[string]any) ([]string, error) {
	conditions, err := optionalObject(ruleset["conditions"], "conditions")
	if err != nil {
		return nil, err
	}
	ref, err := optionalObject(conditions["ref_name"], "conditions.ref_name")
	if err != nil {
		return nil, err
	}
	includes, _, err := refPatterns(ref)
	return includes, err
}
