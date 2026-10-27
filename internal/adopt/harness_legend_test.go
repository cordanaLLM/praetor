// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"
)

type legendGateCase struct {
	name, gates        string
	makefile, lefthook bool
	decline, force     bool
	currentHooks       bool
}

// TestAdoptHarnessRepositoryOwnedGates replays order engine-adopt-force-gate-detect-20261027:
// preserved gates appear in the legend without earning adopted-check cell credit.
func TestAdoptHarnessRepositoryOwnedGates(t *testing.T) {
	const ownMake = "repository-owned `make verify-all` (unread)"
	const ownHooks = "repository-owned `lefthook.yml` (unread)"
	cases := []legendGateCase{
		{name: "both forced", makefile: true, lefthook: true, force: true, gates: ownMake + " + " + ownHooks},
		{name: "both kept", makefile: true, lefthook: true, gates: ownMake + " + " + ownHooks},
		{name: "only Makefile", makefile: true, force: true, gates: "praetor `lefthook.yml` + " + ownMake},
		{name: "only lefthook", lefthook: true, force: true, gates: "generated `make verify-all` + " + ownHooks},
		{name: "fresh generated", force: true, gates: "generated `make verify-all` + praetor `lefthook.yml`"},
		{name: "neither declined", decline: true, force: true, gates: "none: no generated `make verify-all`, no praetor `lefthook.yml`"},
		{name: "existing declined", makefile: true, lefthook: true, decline: true, force: true, gates: ownMake + " + " + ownHooks},
		{name: "current hooks declined", lefthook: true, currentHooks: true, decline: true, force: true, gates: ownHooks},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			harness := adoptLegendFixture(t, tc)
			_, suffix, found := strings.Cut(harness, "Adopted check = ")
			if !found {
				t.Fatal("harness lacks adopted-check legend")
			}
			legend, _, _ := strings.Cut(suffix, "\n")
			t.Log("Adopted check = " + legend)
			if !strings.Contains(legend, "Gates = ("+tc.gates+"); edits not reflected.") {
				t.Errorf("legend gates: got %s; want Gates = (%s)", legend, tc.gates)
			}
			if !strings.Contains(legend, "Adopted-check cells credit generated gates only.") {
				t.Error("legend lacks generated-only cell-credit statement")
			}
			assertLegendCellCredit(t, harness, tc)
		})
	}
}

func adoptLegendFixture(t *testing.T, tc legendGateCase) string {
	t.Helper()
	repo := newTestRepo(t, "legend")
	useLefthook(t, lefthookRuns)
	if tc.makefile {
		mustWrite(t, filepath.Join(repo, makefileName), "verify-all:\n\t@echo repository gate\n")
	}
	if tc.lefthook {
		body := "pre-commit:\n  commands:\n    repository-lint:\n      run: echo repository gate\n"
		if tc.currentHooks {
			body = buildLefthookYAML()
		}
		mustWrite(t, filepath.Join(repo, lefthookFile), body)
	}
	if tc.decline {
		mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nadoption:\n  decline: [makefile, git-hooks]\n")
	}
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework", Force: tc.force}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	return agentsHarness(t, repo)
}

func assertLegendCellCredit(t *testing.T, harness string, tc legendGateCase) {
	t.Helper()
	for _, row := range invariantRows(harness) {
		if (tc.makefile || tc.decline) && strings.Contains(row, " in verify-all") {
			t.Errorf("repository-owned or declined verify-all credited: %s", row)
		}
		if (tc.lefthook || tc.decline) && strings.Contains(row, " in lefthook") {
			t.Errorf("repository-owned or declined lefthook credited: %s", row)
		}
	}
}
