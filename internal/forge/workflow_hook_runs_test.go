// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"slices"
	"testing"
)

// The hook-runner command lines a CI step can hold (#1313). Positive: the run subcommand with
// its stage and hook selection, through a wrapper. Negative: other subcommands and a Lefthook
// run limited by tag. Boundary: a flag value is never read as a hook ID.
func TestScriptHookRuns(t *testing.T) {
	cases := []struct {
		script string
		want   []HookRun
	}{
		{"pre-commit run --all-files", []HookRun{{Runner: HookRunnerPreCommit, Stage: "pre-commit"}}},
		{"pre-commit run --hook-stage=manual gitleaks yamllint", []HookRun{{Runner: HookRunnerPreCommit, Stage: "manual", Only: []string{"gitleaks", "yamllint"}}}},
		{"pre-commit run --files a.yml b.yml --hook-stage pre-push", []HookRun{{Runner: HookRunnerPreCommit, Stage: "pre-push"}}},
		{"env SKIP=x pre-commit run --all-files", []HookRun{{Runner: HookRunnerPreCommit, Stage: "pre-commit"}}},
		{"lefthook run pre-commit --all-files --jobs gitleaks,yamllint", []HookRun{{Runner: HookRunnerLefthook, Stage: "pre-commit", Only: []string{"gitleaks", "yamllint"}}}},
		{"lefthook run pre-push --command=lint --job test", []HookRun{{Runner: HookRunnerLefthook, Stage: "pre-push", Only: []string{"lint", "test"}}}},
		{"pip install pre-commit && pre-commit install", nil},
		{"lefthook install", nil},
		{"lefthook run --all-files", nil},
		{"lefthook run pre-commit --tag fast", nil},
		{"pre-commit run -c other.yaml --all-files", nil},
	}
	for _, tc := range cases {
		got := scriptHookRuns(tc.script)
		if !slices.EqualFunc(got, tc.want, func(a, b HookRun) bool {
			return a.Runner == b.Runner && a.Stage == b.Stage && slices.Equal(a.Only, b.Only)
		}) {
			t.Errorf("%q: runs %+v, want %+v", tc.script, got, tc.want)
		}
	}
}
