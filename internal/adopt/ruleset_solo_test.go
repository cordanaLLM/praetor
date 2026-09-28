// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// soloPathFilteredCI is the CI shape of a single-maintainer repository whose lanes are
// path-filtered: a planner job computes which lanes a diff needs, each lane runs only when the
// planner says so, and an aggregate merge gate that needs all of them runs on every run.
const soloPathFilteredCI = `name: CI
on:
  pull_request:
  push:
    branches: [main]
jobs:
  impact-plan:
    name: CI impact plan
    runs-on: ubuntu-latest
    outputs:
      go: ${{ steps.plan.outputs.go }}
    steps:
      - id: plan
        run: echo "go=true" >> "$GITHUB_OUTPUT"
  go:
    name: Go lane
    needs: impact-plan
    if: needs.impact-plan.outputs.go == 'true'
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
  merge-gate:
    name: Merge gate
    needs: [impact-plan, go]
    if: always()
    runs-on: ubuntu-latest
    steps:
      - run: test "${{ needs.go.result }}" != failure
`

// soloRuleset is the part of an adopted ruleset a single maintainer's merge depends on.
type soloRuleset struct {
	approvals int
	codeOwner bool
	contexts  []string
	hasPRRule bool
}

func parseSoloRuleset(t *testing.T, data string) soloRuleset {
	t.Helper()
	var doc struct {
		Rules []struct {
			Type       string `json:"type"`
			Parameters struct {
				Approvals            int  `json:"required_approving_review_count"`
				CodeOwner            bool `json:"require_code_owner_review"`
				RequiredStatusChecks []struct {
					Context string `json:"context"`
				} `json:"required_status_checks"`
			} `json:"parameters"`
		} `json:"rules"`
	}
	if err := json.Unmarshal([]byte(data), &doc); err != nil {
		t.Fatalf("parse the adopted ruleset: %v\n%s", err, data)
	}
	var got soloRuleset
	for _, rule := range doc.Rules {
		switch rule.Type {
		case "pull_request":
			got.hasPRRule, got.approvals, got.codeOwner = true, rule.Parameters.Approvals, rule.Parameters.CodeOwner
		case "required_status_checks":
			for _, check := range rule.Parameters.RequiredStatusChecks {
				got.contexts = append(got.contexts, check.Context)
			}
		}
	}
	return got
}

// adoptSoloRepository adopts a single-maintainer repository with the path-filtered CI above
// whose manifest declares reviewMode, and returns the ruleset adoption wrote.
func adoptSoloRepository(t *testing.T, name, reviewMode string) (string, string) {
	t.Helper()
	repo := newTestRepo(t, name)
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/solo\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(repo, "cmd/solo/main.go"), "package main\n\nfunc main() {}\n")
	mustWrite(t, filepath.Join(repo, ".github/workflows/ci.yml"), soloPathFilteredCI)
	manifest := "version: 1\nrepository:\n  owner: acme\n  name: " + name + "\nprofiles: [app-service]\nfacets: []\n"
	if reviewMode != "" {
		manifest += "overrides:\n  branch_protection:\n    review_mode: " + reviewMode + "\n"
	}
	mustWrite(t, filepath.Join(repo, manifestFile), manifest)
	lockSource := newAdoptLockSource(t)
	preview, err := Adopt(t.Context(), AdoptOptions{Path: repo, DryRun: true, SkipGitValidation: true, LockSourceRoot: lockSource})
	if err != nil || len(preview.Previews) != 1 {
		t.Fatalf("dry-run adopt: %v, previews %+v", err, preview)
	}
	if _, err := Adopt(t.Context(), AdoptOptions{Path: repo, SkipGitValidation: true, LockSourceRoot: lockSource}); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	written := readRuleset(t, repo)
	if preview.Previews[0].Content != written {
		t.Fatalf("the dry run previewed a ruleset the run does not write:\npreview:\n%s\nwritten:\n%s", preview.Previews[0].Content, written)
	}
	return repo, written
}

// Positive: a single-maintainer repository with path-filtered CI adopts a ruleset its one
// maintainer can merge under: no approval, no code-owner review, and the aggregate merge gate
// required beside the unconditional planner. The lane the planner gates is not required, and the
// audit accepts the file adoption wrote, because both render it the same way.
func TestAdopt_Positive_SoloPathFilteredRulesetIsMergeable(t *testing.T) {
	repo, written := adoptSoloRepository(t, "solo-path-filtered", "single_maintainer")
	got := parseSoloRuleset(t, written)
	if !got.hasPRRule || got.approvals != 0 || got.codeOwner {
		t.Fatalf("single_maintainer must render 0 approvals without code-owner review, got %+v\n%s", got, written)
	}
	if !slices.Contains(got.contexts, "Merge gate") || !slices.Contains(got.contexts, "CI impact plan") {
		t.Fatalf("the always() merge gate and the planner must be required, got %v", got.contexts)
	}
	if slices.Contains(got.contexts, "Go lane") {
		t.Fatalf("a lane gated on the planner's output must not be required, got %v", got.contexts)
	}
	if summary, err := auditAdoptedRuleset(t, repo); err != nil || !strings.Contains(summary, "verified") {
		t.Fatalf("the audit must compare and accept the ruleset adoption wrote: %q, %v", summary, err)
	}
	// The audit renders the same required checks: dropping the merge gate by hand is drift.
	withoutGate := strings.Replace(written, `"context": "Merge gate"`, `"context": "Go lane"`, 1)
	mustWrite(t, filepath.Join(repo, rulesetFile), withoutGate)
	if _, err := auditAdoptedRuleset(t, repo); err == nil {
		t.Fatal("the audit accepted a ruleset that swaps the merge gate for a gated lane")
	}
}

// Negative: without the declared review mode the archetype's independent review stays, so the
// repository does not silently lose its approval requirement; the merge gate is required alike.
func TestAdopt_Negative_IndependentReviewModeKeepsApprovals(t *testing.T) {
	repo, written := adoptSoloRepository(t, "solo-independent", "")
	got := parseSoloRuleset(t, written)
	if got.approvals < 1 || !got.codeOwner {
		t.Fatalf("independent review must keep approvals and code-owner review, got %+v", got)
	}
	if !slices.Contains(got.contexts, "Merge gate") {
		t.Fatalf("the merge gate is required whatever the review mode, got %v", got.contexts)
	}
	if _, err := auditAdoptedRuleset(t, repo); err != nil {
		t.Fatalf("the audit must accept the ruleset adoption wrote: %v", err)
	}
}
