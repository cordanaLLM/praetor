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
// planner says so, and an aggregate merge gate that needs all of them runs on every run and fails
// when any of them failed or was cancelled.
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
      - if: contains(needs.*.result, 'failure') || contains(needs.*.result, 'cancelled')
        run: exit 1
`

// soloRuleset is the part of an adopted ruleset a single maintainer's merge depends on.
type soloRuleset struct {
	approvals int
	codeOwner bool
	contexts  []string
	hasPRRule bool
	bypass    []soloBypassActor
}

// soloBypassActor is one bypass_actors entry of an adopted ruleset.
type soloBypassActor struct {
	ActorID    int    `json:"actor_id"`
	ActorType  string `json:"actor_type"`
	BypassMode string `json:"bypass_mode"`
}

func parseSoloRuleset(t *testing.T, data string) soloRuleset {
	t.Helper()
	var doc struct {
		BypassActors []soloBypassActor `json:"bypass_actors"`
		Rules        []struct {
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
	got := soloRuleset{bypass: doc.BypassActors}
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
// maintainer can merge under: no approval, no code-owner review, the repository admin role as a
// pull-request bypass actor, and the aggregate merge gate as the only required check (#76). The
// planner and the lane it gates are covered by the gate, which fails when either failed or was
// cancelled. The audit accepts the file adoption wrote, because both render it the same way.
func TestAdopt_Positive_SoloPathFilteredRulesetIsMergeable(t *testing.T) {
	repo, written := adoptSoloRepository(t, "solo-path-filtered", "single_maintainer")
	got := parseSoloRuleset(t, written)
	if !got.hasPRRule || got.approvals != 0 || got.codeOwner {
		t.Fatalf("single_maintainer must render 0 approvals without code-owner review, got %+v\n%s", got, written)
	}
	if want := []string{"Merge gate"}; !slices.Equal(got.contexts, want) {
		t.Fatalf("the proven merge gate must be the only required check, got %v, want %v", got.contexts, want)
	}
	if want := []soloBypassActor{{ActorID: 5, ActorType: "RepositoryRole", BypassMode: "pull_request"}}; !slices.Equal(got.bypass, want) {
		t.Fatalf("single_maintainer must let the admin role bypass on pull requests, got %+v, want %+v", got.bypass, want)
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
// repository does not silently lose its approval requirement, and no actor may bypass the rules;
// the merge gate is required alike.
func TestAdopt_Negative_IndependentReviewModeKeepsApprovals(t *testing.T) {
	repo, written := adoptSoloRepository(t, "solo-independent", "")
	got := parseSoloRuleset(t, written)
	if got.approvals < 1 || !got.codeOwner {
		t.Fatalf("independent review must keep approvals and code-owner review, got %+v", got)
	}
	if len(got.bypass) != 0 || strings.Contains(written, "bypass_actors") {
		t.Fatalf("independent review must render no bypass actor, got %+v", got.bypass)
	}
	if want := []string{"Merge gate"}; !slices.Equal(got.contexts, want) {
		t.Fatalf("the merge gate is required whatever the review mode, got %v, want %v", got.contexts, want)
	}
	if _, err := auditAdoptedRuleset(t, repo); err != nil {
		t.Fatalf("the audit must accept the ruleset adoption wrote: %v", err)
	}
}
