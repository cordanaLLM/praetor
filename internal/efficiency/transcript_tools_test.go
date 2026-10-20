// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import "testing"

func TestEfficiencyTranscriptCreationOptions(t *testing.T) {
	for _, tc := range []struct{ name, command, root, branch string }{
		{"quiet-first", "git worktree add -q -b train/engine-20261020 /home/u/dev/worktrees/praetor-train-engine-20261020 origin/main", "/home/u/dev/worktrees/praetor-train-engine-20261020", "train/engine-20261020"},
		{"git-C", "git -C /home/u/dev/repo worktree add -q -b fix/x /home/u/dev/worktrees/repo-x origin/controller", "/home/u/dev/worktrees/repo-x", "fix/x"},
		{"force-short", "git worktree add -f -b fix/x /w/a main", "/w/a", "fix/x"},
		{"force-long", "git worktree add -b fix/x --force /w/a main", "/w/a", "fix/x"},
		{"lock", "git worktree add --lock -B fix/x /w/a main", "/w/a", "fix/x"},
		{"no-checkout", "git worktree add --no-checkout -b fix/x /w/a main", "/w/a", "fix/x"},
		{"checkout", "git worktree add -b fix/x --checkout /w/a main", "/w/a", "fix/x"},
		{"track", "git worktree add --track -b fix/x /w/a origin/main", "/w/a", "fix/x"},
		{"no-track", "git worktree add -b fix/x --no-track /w/a origin/main", "/w/a", "fix/x"},
		{"guess-remote", "git worktree add --guess-remote -b fix/x /w/a origin/main", "/w/a", "fix/x"},
		{"reset-middle", "git worktree add -q -B fix/x --lock --no-checkout /w/a main", "/w/a", "fix/x"},
		{"orphan", "git worktree add -q --orphan fix/x --lock /w/a", "/w/a", "fix/x"},
		{"orphan-first", "git worktree add --orphan fix/x -q /w/a", "/w/a", "fix/x"},
		{"detach", "git worktree add --detach /w/a origin/main", "/w/a", ""},
		{"detach-with-options", "git worktree add -q --detach --force /w/a feat/existing", "/w/a", ""},
		{"separator", "git worktree add -q -b fix/x -- /w/a origin/main", "/w/a", "fix/x"},
		{"separator-dash-path", "git worktree add -b fix/x -- -a main", "/checkout/-a", "fix/x"},
		{"separator-stops-options", "git worktree add -- --detach feat/existing", "/checkout/--detach", "feat/existing"},
		{"unknown-first", "git worktree add --unknown -b fix/x /w/a main", "", ""},
		{"unknown-after-branch", "git worktree add -b fix/x --unknown /w/a main", "", ""},
		{"missing-branch", "git worktree add -q -b", "", ""},
		{"missing-path", "git worktree add -q -b fix/x", "", ""},
		{"missing-separated-path", "git worktree add -q --", "", ""},
		{"separator-is-not-branch", "git worktree add -b -- /w/a", "", ""},
		{"next-command-is-not-path", "git worktree add -q; echo /w/a", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := newTranscriptJoin(nil, "main")
			if err := j.bashPaths(tc.command, "/checkout", make(map[string]bool)); err != nil {
				t.Fatal(err)
			}
			if tc.root == "" {
				if len(j.roots) != 0 {
					t.Fatalf("unsupported creation recorded: %v", j.roots)
				}
				return
			}
			if branch, ok := j.roots[tc.root]; !ok || branch != tc.branch {
				t.Fatalf("roots=%v; want %s -> %q", j.roots, tc.root, tc.branch)
			}
		})
	}
}
