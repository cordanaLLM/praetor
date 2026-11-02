// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestStatusCheckRenameAllowance(t *testing.T) {
	rename := StatusContextRename{Old: "Old", New: "New"}
	for _, tc := range []struct {
		name     string
		declared []string
		live     map[string][]string
		renames  []StatusContextRename
		want     ProtectionVerdict
	}{
		{"pending", []string{"New"}, map[string][]string{"Old": {"ruleset"}}, []StatusContextRename{rename}, ProtectionPending},
		{"applied", []string{"New"}, map[string][]string{"New": {"ruleset"}}, []StatusContextRename{rename}, ProtectionEnforced},
		{"no predecessor", []string{"New"}, nil, []StatusContextRename{rename}, ProtectionDrift},
		{"no proof", []string{"New"}, map[string][]string{"Old": {"ruleset"}}, nil, ProtectionDrift},
		{"old still declared", []string{"Old", "New"}, map[string][]string{"Old": {"ruleset"}}, []StatusContextRename{rename}, ProtectionDrift},
		{"another missing", []string{"New", "Missing"}, map[string][]string{"Old": {"ruleset"}}, []StatusContextRename{rename}, ProtectionDrift},
		{"multiple windows", []string{"New"}, map[string][]string{"Old": {"ruleset"}}, []StatusContextRename{rename, {Old: "Other", New: "Another"}}, ProtectionDrift},
	} {
		t.Run(tc.name, func(t *testing.T) {
			finding := checksFindingWithRenames(tc.declared, tc.live, tc.renames)
			if finding.Verdict != tc.want {
				t.Fatalf("finding = %+v, want %s", finding, tc.want)
			}
			if tc.live != nil && tc.name == "pending" && len(tc.live["New"]) != 0 {
				t.Fatal("comparison mutated live state")
			}
		})
	}
}

func TestRequiredStatusContextRenamesInvalidInputs(t *testing.T) {
	var missing context.Context
	if _, err := RequiredStatusContextRenames(missing, t.TempDir(), "HEAD", "acme/widgets"); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := RequiredStatusContextRenames(t.Context(), t.TempDir(), "HEAD", "invalid"); err == nil {
		t.Fatal("invalid repository accepted")
	}
}

func TestSingleContextRenameRejectsSeveralChanges(t *testing.T) {
	old := map[string]contextRenameJob{"ci.yml/build": {names: []string{"Build"}}, "ci.yml/test": {names: []string{"Test"}}}
	newJobs := map[string]contextRenameJob{"ci.yml/build": {names: []string{"Compile"}}, "ci.yml/test": {names: []string{"Tests"}}}
	if renames := singleContextRename(old, newJobs); len(renames) != 0 {
		t.Fatalf("several renames granted a window: %+v", renames)
	}
	if renames := singleContextRename(nil, newJobs); len(renames) != 0 {
		t.Fatalf("new jobs granted a window: %+v", renames)
	}
}

func TestRequiredStatusContextRenames_ReusableCallerProof(t *testing.T) {
	inline := strings.Replace(prWorkflow("Licence / REUSE lint"), "  job:", "  other:", 1)
	caller := "on: pull_request\njobs:\n  licence:\n    name: Licence\n    uses: " +
		PluranoHoristesLicenceAction + "@" + PluranoHoristesLicenceWorkflowSHA + "\n"
	for _, tc := range []struct {
		name, before, after string
		want                int
	}{
		{"positive reusable caller", prWorkflow("REUSE lint"), caller, 1},
		{"negative inline suffix", prWorkflow("REUSE lint"), inline, 0},
		{"negative inline unwrapping", caller, prWorkflow("REUSE lint"), 0},
		{"boundary stable inline job", prWorkflow("REUSE lint"), prWorkflow("Licence / REUSE lint"), 1},
		{"boundary unrelated inline job", prWorkflow("REUSE lint"), strings.Replace(inline, "Licence / REUSE lint", "Other", 1), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, base := committedWorkflowRepo(t, map[string]string{".github/workflows/ci.yml": tc.before})
			writeWorkflowFixture(t, root, "ci.yml", tc.after)
			got, err := RequiredStatusContextRenames(t.Context(), root, base, "acme/widgets")
			if err != nil || len(got) != tc.want {
				t.Fatalf("renames = %+v, %v; want %d", got, err, tc.want)
			}
		})
	}
}

func TestRequiredStatusContextRenames_FileChangeBoundary(t *testing.T) {
	old := prWorkflow("Build") + "  reuse:\n    name: REUSE lint\n"
	for _, tc := range []struct {
		name, before, after string
		want                int
	}{
		{"positive one rename", old, strings.Replace(old, "Build", "Compile", 1), 1},
		{"negative two renames with changed ID", old, prWorkflow("Compile") + "  licence:\n    name: Licence / REUSE lint\n", 0},
		{"negative rename plus addition", prWorkflow("Build"), prWorkflow("Compile") + "  extra:\n    name: Extra\n", 0},
		{"negative rename plus removal", old, prWorkflow("Compile"), 0},
		{"boundary unchanged", old, old, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, base := committedWorkflowRepo(t, map[string]string{".github/workflows/ci.yml": tc.before})
			writeWorkflowFixture(t, root, "ci.yml", tc.after)
			got, err := RequiredStatusContextRenames(t.Context(), root, base, "acme/widgets")
			if err != nil || len(got) != tc.want {
				t.Fatalf("renames = %+v, %v; want %d", got, err, tc.want)
			}
		})
	}
}

// TestUnreportedStatusContexts pins discovery of unreported status checks from a base commit or workflow run report.
func TestUnreportedStatusContexts(t *testing.T) {
	var missing context.Context
	if _, err := UnreportedStatusContexts(missing, t.TempDir(), "HEAD", "acme/widgets", nil); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := UnreportedStatusContexts(t.Context(), t.TempDir(), "HEAD", "invalid", nil); err == nil {
		t.Fatal("invalid repository accepted")
	}
	if got, err := UnreportedStatusContexts(t.Context(), t.TempDir(), "", "acme/widgets", nil); err != nil || len(got) != 0 {
		t.Fatalf("empty commit and nil report = %v, %v; want empty", got, err)
	}

	root, base := committedWorkflowRepo(t, map[string]string{".github/workflows/ci.yml": prWorkflow("CI")})
	writeWorkflowFixture(t, root, "codeql.yml", prWorkflow("analyze (python)"))
	got, err := UnreportedStatusContexts(t.Context(), root, base, "acme/widgets", nil)
	if err != nil || !slices.Equal(got, []string{"analyze (python)"}) {
		t.Fatalf("unreported from commit = %v, %v; want [analyze (python)]", got, err)
	}

	report := &WorkflowRunReport{
		Branch: "main",
		Findings: []WorkflowRunFinding{
			{Workflow: "ci.yml", Health: WorkflowHealthy},
			{Workflow: "codeql.yml", Health: WorkflowUnknown},
		},
	}
	got, err = UnreportedStatusContexts(t.Context(), root, "", "acme/widgets", report)
	if err != nil || !slices.Equal(got, []string{"analyze (python)"}) {
		t.Fatalf("unreported from runs = %v, %v; want [analyze (python)]", got, err)
	}

	healthy := &WorkflowRunReport{
		Branch: "main",
		Findings: []WorkflowRunFinding{
			{Workflow: "ci.yml", Health: WorkflowHealthy},
			{Workflow: "codeql.yml", Health: WorkflowHealthy},
		},
	}
	if got, err = UnreportedStatusContexts(t.Context(), root, "", "acme/widgets", healthy); err != nil || len(got) != 0 {
		t.Fatalf("healthy runs = %v, %v; want empty", got, err)
	}
}

// TestUnreportedStatusContexts_AbsentObject pins error handling when a committed workflow git object is absent.
func TestUnreportedStatusContexts_AbsentObject(t *testing.T) {
	root, base := committedWorkflowRepo(t, map[string]string{".github/workflows/ci.yml": prWorkflow("CI")})
	removeLooseObject(t, root, base, ".github/workflows/ci.yml")
	if got, err := UnreportedStatusContexts(t.Context(), root, base, "acme/widgets", nil); !errors.Is(err, ErrCommittedWorkflowAbsent) || got != nil {
		t.Fatalf("absent committed workflow = %v, %v; want ErrCommittedWorkflowAbsent", got, err)
	}
}
