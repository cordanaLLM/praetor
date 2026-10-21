// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
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
	old := map[string][]string{"ci.yml/build": {"Build"}, "ci.yml/test": {"Test"}}
	newJobs := map[string][]string{"ci.yml/build": {"Compile"}, "ci.yml/test": {"Tests"}}
	if renames := singleContextRename(old, newJobs); len(renames) != 0 {
		t.Fatalf("several renames granted a window: %+v", renames)
	}
	if renames := singleContextRename(nil, newJobs); len(renames) != 0 {
		t.Fatalf("new jobs granted a window: %+v", renames)
	}
}
