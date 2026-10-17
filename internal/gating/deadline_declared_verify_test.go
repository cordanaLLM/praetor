// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

func manifestRepoWithTimeout(t *testing.T, timeout string) string {
	t.Helper()
	dir := committedAdmitRepo(t, "pyproject.toml")
	content := fmt.Sprintf("version: 1\nrepository:\n  owner: fixture\n  name: fixture\nprofiles:\n  - pages-site\ngate:\n  declared_verify_timeout: %s\n", timeout)
	writeFile(t, filepath.Join(dir, ".standards.yaml"), content)
	treeGit(t, dir, "add", "--", ".standards.yaml")
	treeGit(t, dir, "commit", "-q", "-m", "declare verify timeout")
	return dir
}

// Positive: declared timeout 20m sets the stage bound to 20m and the run deadline covers it.
func TestResolveRepoRunBudget_Positive_DeclaredVerifyTimeout(t *testing.T) {
	dir := manifestRepoWithTimeout(t, "20m")
	budget, err := ResolveRepoRunBudget("", dir)
	if err != nil {
		t.Fatalf("ResolveRepoRunBudget returned error: %v", err)
	}

	wantBound := 20 * time.Minute
	if budget.StageBound != wantBound {
		t.Fatalf("budget.StageBound = %s, want %s", budget.StageBound, wantBound)
	}
	if budget.BoundOrigin != ".standards.yaml" {
		t.Errorf("budget.BoundOrigin = %q, want %q", budget.BoundOrigin, ".standards.yaml")
	}
	if budget.Timeout() < budget.StageBound {
		t.Fatalf("run deadline %s does not cover stage bound %s", budget.Timeout(), budget.StageBound)
	}
	wantTimeout := 20*time.Minute + OtherStagesAllowance
	if budget.Timeout() != wantTimeout {
		t.Errorf("budget.Timeout() = %s, want %s", budget.Timeout(), wantTimeout)
	}
	if !strings.Contains(budget.Note, ".standards.yaml") || !strings.Contains(budget.Note, "20m0s") {
		t.Errorf("budget.Note = %q, want mention of .standards.yaml and 20m0s", budget.Note)
	}
}

// Negative: 31m and 0s are refused naming the ceiling.
func TestResolveRepoRunBudget_Negative_RefusesExceededCeilingAndNonPositive(t *testing.T) {
	cases := []struct {
		name    string
		timeout string
		want    []string
	}{
		{"over ceiling 31m", "31m", []string{"declared_verify_timeout", "31m", config.MaxDeclaredVerifyTimeout.String()}},
		{"zero duration", "0s", []string{"declared_verify_timeout", "0s", "not positive", config.MaxDeclaredVerifyTimeout.String()}},
		{"negative duration", "-1m", []string{"declared_verify_timeout", "-1m", "not positive", config.MaxDeclaredVerifyTimeout.String()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := manifestRepoWithTimeout(t, tc.timeout)
			_, err := ResolveRepoRunBudget("", dir)
			if err == nil {
				t.Fatalf("ResolveRepoRunBudget with %s expected error, got nil", tc.timeout)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err.Error(), w)
				}
			}
		})
	}
}

// Boundary: the environment variable overrides the declared value.
func TestResolveRepoRunBudget_Boundary_EnvironmentOverridesDeclaredValue(t *testing.T) {
	dir := manifestRepoWithTimeout(t, "20m")
	budget, err := ResolveRepoRunBudget("10m", dir)
	if err != nil {
		t.Fatalf("ResolveRepoRunBudget returned error: %v", err)
	}

	wantBound := 10 * time.Minute
	if budget.StageBound != wantBound {
		t.Fatalf("budget.StageBound = %s, want %s (environment should override declared)", budget.StageBound, wantBound)
	}
	if budget.BoundOrigin != "environment" {
		t.Errorf("budget.BoundOrigin = %q, want %q", budget.BoundOrigin, "environment")
	}
}
