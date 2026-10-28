// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestMergeHelper_Negative_DirectMerge(t *testing.T) {
	policy, err := BuildPolicy(config.HookSettings{Merge: config.MergeSettings{Helper: "/abs/squash_merge.sh"}})
	if err != nil {
		t.Fatal(err)
	}
	for name, command := range map[string]string{
		"plain": "gh pr merge 12 --squash", "chained": "cd x && gh pr merge 12",
		"global option": "gh -R o/r pr merge 12", "continued": "gh pr \\\nmerge 12",
		"quoted mention": `echo 'gh pr merge'`, "empty helper line": "\ngh pr merge 12",
		"helper on earlier line": "squash_merge.sh 12 sha\ngh pr merge 12",
		"similar basename":       `other_squash_merge.sh 'gh pr merge 12'`,
		"literal basename":       `squash_mergeXsh 'gh pr merge 12'`,
	} {
		t.Run(name, func(t *testing.T) {
			requireDeniedContaining(t, command, policy.Command(command), "/abs/squash_merge.sh", "hooks.merge.helper", "red or moved head")
		})
	}
}

func TestMergeHelper_Positive_HelperAndReads(t *testing.T) {
	policy, err := BuildPolicy(config.HookSettings{Merge: config.MergeSettings{Helper: "/abs/squash_merge.sh"}})
	if err != nil {
		t.Fatal(err)
	}
	for name, command := range map[string]string{
		"helper": "squash_merge.sh 12 sha --delete-branch", "bash helper": "bash /abs/squash_merge.sh 12 sha",
		"basename exemption": `squash_merge.sh 12 sha 'gh pr merge 12'`,
		"path exemption":     `/other/squash_merge.sh 12 sha 'gh pr merge 12'`,
		"leading blanks":     `  squash_merge.sh 'gh pr merge 12'`,
		"read":               "gh pr view 12", "other command": "gh issue view 12", "empty": "",
	} {
		t.Run(name, func(t *testing.T) {
			if got := policy.Command(command); got.Outcome != Allow {
				t.Errorf("%q: %+v", command, got)
			}
		})
	}
}

func TestMergeHelper_Boundary_Unset(t *testing.T) {
	policy, err := BuildPolicy(config.HookSettings{})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"gh pr merge 12 --squash", "cd x && gh pr merge 12", "gh -R o/r pr merge 12", `echo 'gh pr merge'`} {
		if got := policy.Command(command); got.Outcome != Allow {
			t.Errorf("unset helper %q: %+v", command, got)
		}
	}
}

func TestMergeHelper_Boundary_OtherRulesStillApply(t *testing.T) {
	policy, err := BuildPolicy(config.HookSettings{Merge: config.MergeSettings{Helper: "squash_merge.sh"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{`squash_merge.sh 'gh pr merge 12 --admin'`, "gh api repos/o/r/pulls/12/merge"} {
		if got := policy.Command(command); got.Outcome != Deny {
			t.Errorf("built-in rule bypassed by helper setting: %q: %+v", command, got)
		}
	}
}
