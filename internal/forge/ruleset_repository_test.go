// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// A repository's rendering is RenderRepositoryRuleset over the contexts its workflows name, the
// file ValidateRepositoryRuleset then accepts.
func TestRenderRulesetForRepository_Positive_ChecksComeFromTheWorkflows(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", "on: pull_request\njobs:\n  verify: {}\n")
	policy := config.DefaultPolicy().BranchProtection

	data, contexts, err := RenderRulesetForRepository(t.Context(), root, policy)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !slices.Equal(contexts, []string{"verify"}) {
		t.Fatalf("contexts = %v, want [verify]", contexts)
	}
	want, err := RenderRepositoryRuleset(policy, contexts)
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("rendering differs from RenderRepositoryRuleset: %v\n%s", err, data)
	}
	if err := ValidateRepositoryRuleset(data, policy, contexts); err != nil {
		t.Fatalf("the rendering does not validate: %v", err)
	}
}

// A workflow inventory that cannot be read, an invalid policy and a cancelled context render
// nothing.
func TestRenderRulesetForRepository_Negative_RefusesWhatItCannotRender(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", "jobs: [")
	if data, _, err := RenderRulesetForRepository(t.Context(), root, config.DefaultPolicy().BranchProtection); err == nil || data != nil {
		t.Fatalf("a malformed workflow rendered a ruleset: %s", data)
	}
	bad := config.DefaultPolicy().BranchProtection
	bad.RequiredApprovingReviewers = -1
	_, _, err := RenderRulesetForRepository(t.Context(), t.TempDir(), bad)
	if err == nil || !strings.Contains(err.Error(), RepositoryRulesetPath) {
		t.Fatalf("an invalid policy must fail naming the ruleset, got %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := RenderRulesetForRepository(ctx, t.TempDir(), config.DefaultPolicy().BranchProtection); err == nil {
		t.Fatal("a cancelled context rendered a ruleset")
	}
}

// A repository without workflows renders a ruleset with no status-check rule, and the policy
// alone decides signatures.
func TestRenderRulesetForRepository_Boundary_NoWorkflows(t *testing.T) {
	policy := config.DefaultPolicy().BranchProtection
	policy.RequireSignedCommits = true
	data, contexts, err := RenderRulesetForRepository(t.Context(), t.TempDir(), policy)
	if err != nil || len(contexts) != 0 {
		t.Fatalf("render: %v, contexts %v", err, contexts)
	}
	if strings.Contains(string(data), "required_status_checks") || !strings.Contains(string(data), "required_signatures") {
		t.Fatalf("unexpected ruleset without workflows:\n%s", data)
	}
}
