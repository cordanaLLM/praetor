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
	currentMakefile    bool
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
		{name: "current Makefile declined", makefile: true, currentMakefile: true, decline: true, force: true, gates: ownMake},
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
		body := "verify-all:\n\t@echo repository gate\n"
		if tc.currentMakefile {
			body = buildMakefile(&VerificationPlan{Status: verificationNotApplicable, Reasons: []string{notApplicableReason}})
		}
		mustWrite(t, filepath.Join(repo, makefileName), body)
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

// TestAdoptForcePreservesRepositoryOwnedHarness replays the golusoris recurrence
// (order engine-adopt-force-gate-detect-20261027-refinement-20261101): adopt --force on a
// repository with its own harness table, Makefile verify-all and lefthook.yml keeps the
// table, verify-all paragraph and social override byte-for-byte and writes no no-gates line,
// while a repository with neither gets the generated table and the no-gates line.
func TestAdoptForcePreservesRepositoryOwnedHarness(t *testing.T) {
	fixture := mustRead(t, filepath.Join("testdata", "harness-own-sections.golden"))
	const (
		wantVerify = "`make verify-all` runs `praetorctl audit`, context verification, and repository tests. Exit 0 means verified. On failure, agents keep full logs in ephemeral storage and report at most 1,500 diagnostic tokens."
		wantTable  = "## Core Directives & Invariants — Golusoris HISS-21 Lattice\n\n" +
			"| Invariant | Scope | NASA Rule | Rule | Gate in this repository |\n" +
			"| :--- | :--- | :--- | :--- | :--- |\n" +
			"| **HISS-01** | Control Flow | Rule 1 | Recursion strictly prohibited; call graph must be a DAG; zero `goto`. | `praetorctl audit` HISS scan: direct plus mutual recursion and `goto` |\n" +
			"| **HISS-02** | Loops & I/O | Rule 2 | Scalar upper bound on all loops; explicit `context.Context` timeout on all I/O. | `praetorctl audit` loop scan; all-module lint; required full-tree Semgrep HTTP rules |\n" +
			"| **HISS-16** | Context Integrity | Fleet | Single canonical `AGENTS.md`; vendor files compiled, never hand-edited. | `praetorctl compile-context --verify` in lefthook and `make verify-all` |\n" +
			"| **HISS-21** | Platform Neutrality | Fleet | Gates, hooks, and emitted templates run on Linux, macOS, and Windows, or skip with a stated reason; a gate that cannot run is not passing. | Required three-OS `Platform Neutrality` matrix after its first hosted green; local bounded all-module build/vet/short-test gate |"
		wantSocial = "Golusoris social override: no receipt fence; no changelog fragments.\n" +
			"`.github/PULL_REQUEST_TEMPLATE.md` uses `## Summary`; `release-please` reads Conventional\n" +
			"Commits. Generic managed row remains upstream gap `cordanaLLM/praetor#328`."
		noGates = "none: no generated `make verify-all`, no praetor `lefthook.yml`"
	)

	repo := newTestRepo(t, "golusoris")
	useLefthook(t, lefthookRuns)
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/golusoris\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, makefileName), ".PHONY: verify-all\nverify-all:\n\t@echo golusoris gate\n")
	mustWrite(t, filepath.Join(repo, lefthookFile), "pre-commit:\n  commands:\n    lint:\n      run: echo golusoris hook\n")
	mustWrite(t, filepath.Join(repo, agentsFile), fixture)
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework", Force: true}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	got := mustRead(t, filepath.Join(repo, agentsFile))
	for label, want := range map[string]string{"verify-all paragraph": wantVerify, "invariant table": wantTable, "social override": wantSocial} {
		if !strings.Contains(got, want) {
			t.Errorf("adopt --force lost %s byte-for-byte", label)
		}
	}
	if strings.Contains(got, noGates) || strings.Contains(got, "| Invariant | Rule | Adopted check | On fail |") {
		t.Errorf("adopt --force wrote generated table or no-gates line:\n%s", got)
	}

	neither := newTestRepo(t, "neither")
	mustWrite(t, filepath.Join(neither, manifestFile), "version: 1\nadoption:\n  decline: [makefile, git-hooks]\n")
	mustWrite(t, filepath.Join(neither, agentsFile), "# neither Agent Operating Harness\n\n## Core Directives & Invariants\nold table\n\n---\n\n# Instructions\nKeep.\n")
	if _, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: neither, Profile: "framework", Force: true}); err != nil {
		t.Fatalf("Adopt --force neither: %v", err)
	}
	gotNeither := mustRead(t, filepath.Join(neither, agentsFile))
	if !strings.Contains(gotNeither, "| Invariant | Rule | Adopted check | On fail |") || !strings.Contains(gotNeither, noGates) {
		t.Errorf("repository with neither gate must get generated table and no-gates line:\n%s", gotNeither)
	}
}
