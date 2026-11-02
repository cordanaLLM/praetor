// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type legendGateCase struct {
	name, gates         string
	makefile, lefthook  bool
	decline             bool
	declineMakefileOnly bool
	skipHooks, force    bool
	inactiveHooks       bool
	currentHooks        bool
	currentMakefile     bool
}

// TestAdoptHarnessRepositoryOwnedGates replays order engine-adopt-force-gate-detect-20261027:
// preserved gates appear in the legend without earning adopted-check cell credit.
func TestAdoptHarnessRepositoryOwnedGates(t *testing.T) {
	const ownMake = "repository-owned `make verify-all` (unread)"
	const ownHooks = "repository-owned `lefthook.yml` (unread)"
	const inactiveHooks = "praetor `lefthook.yml` (inactive)"
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
		{name: "inactive hooks makefile declined", declineMakefileOnly: true, inactiveHooks: true, force: true, gates: inactiveHooks},
		{name: "skipped hooks makefile declined", declineMakefileOnly: true, skipHooks: true, force: true, gates: inactiveHooks},
		{name: "inactive hooks with generated Makefile", inactiveHooks: true, force: true, gates: "generated `make verify-all` + " + inactiveHooks},
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

// adoptLegendFixture scaffolds a temporary repository exercising tc and returns its AGENTS.md harness.
func adoptLegendFixture(t *testing.T, tc legendGateCase) string {
	t.Helper()
	repo := newTestRepo(t, "legend")
	if !tc.inactiveHooks {
		useLefthook(t, lefthookRuns)
	}
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
	switch {
	case tc.decline:
		mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nadoption:\n  decline: [makefile, git-hooks]\n")
	case tc.declineMakefileOnly:
		mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nadoption:\n  decline: [makefile]\n")
	}
	opts := AdoptOptions{
		LockSourceRoot: newAdoptLockSource(t), Path: repo,
		Profile: "framework", Force: tc.force, SkipHookActivation: tc.skipHooks,
	}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	return agentsHarness(t, repo)
}

// assertLegendCellCredit verifies that preserved, declined, or inactive gates are not credited in harness table rows.
func assertLegendCellCredit(t *testing.T, harness string, tc legendGateCase) {
	t.Helper()
	for _, row := range invariantRows(harness) {
		if (tc.makefile || tc.decline || tc.declineMakefileOnly) && strings.Contains(row, " in verify-all") {
			t.Errorf("repository-owned or declined verify-all credited: %s", row)
		}
		if (tc.lefthook || tc.decline || tc.inactiveHooks || tc.skipHooks) && strings.Contains(row, " in lefthook") {
			t.Errorf("repository-owned, declined or inactive lefthook credited: %s", row)
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

// TestAdoptForceRefreshesLegacyGeneratedInvariantsAndKeepsCustomRows checks that an AGENTS.md
// carrying the legacy generated invariant header (| Invariant | Rule | Enforcement | On fail |)
// without an Adopted-check legend is refreshed under --force while keeping custom non-HISS rows.
func TestAdoptForceRefreshesLegacyGeneratedInvariantsAndKeepsCustomRows(t *testing.T) {
	const customRow = "| **ACME-01** secrets | never log tokens | review | blocker |"
	legacy := "# legacy Agent Operating Harness\n\n<!-- praetor:head -->\n\n" +
		"## Core Directives & Invariants\n\n" +
		"| Invariant | Rule | Enforcement | On fail |\n" +
		"| :--- | :--- | :--- | :--- |\n" +
		"| **HISS-01** control flow | stale rule text | build | immediate build failure |\n" +
		customRow + "\n\n" +
		"## Operational Rules\n\n1. **Act on verified state.**\n\n" +
		"---\n\n# Instructions\nKeep.\n"
	repo := newTestRepo(t, "legacy-header")
	useLefthook(t, lefthookRuns)
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/legacy\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, agentsFile), legacy)
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework", Force: true}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	got := mustRead(t, filepath.Join(repo, agentsFile))
	if strings.Contains(got, "| Invariant | Rule | Enforcement | On fail |") || strings.Contains(got, "stale rule text") {
		t.Errorf("legacy generated invariant table was preserved instead of refreshed:\n%s", got)
	}
	if !strings.Contains(got, "| Invariant | Rule | Adopted check | On fail |") || !strings.Contains(got, customRow) {
		t.Errorf("refreshed table lacks current header or custom row:\n%s", got)
	}
}

// TestAdoptForcePreservesConfigBandWithoutRegisterBlock checks that a config band carrying
// repository overrides without an existing register marker pair receives the generated register
// section while keeping the repository overrides byte-for-byte.
func TestAdoptForcePreservesConfigBandWithoutRegisterBlock(t *testing.T) {
	const override = "Repository social override: keep this line in the config band."
	fixture := mustRead(t, filepath.Join("testdata", "harness-own-sections.golden"))
	withoutRegister := strings.Replace(fixture,
		"## Text Register\n\n<!-- praetor:register:start -->\n"+
			"Register follows the audience, then the task label of your brief (`register:` in `.standards.yaml`; labels are the router's `target_tasks`).\n"+
			"<!-- praetor:register:end -->\n\n",
		override+"\n\n", 1)
	repo := newTestRepo(t, "cfg-no-register")
	useLefthook(t, lefthookRuns)
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/cfg\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, agentsFile), withoutRegister)
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework", Force: true}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	got := mustRead(t, filepath.Join(repo, agentsFile))
	if !strings.Contains(got, override) || strings.Count(got, "<!-- praetor:register:start -->") != 1 {
		t.Errorf("config-band override without register markers was lost or register block not inserted:\n%s", got)
	}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("second Adopt --force: %v", err)
	}
	if again := mustRead(t, filepath.Join(repo, agentsFile)); again != got {
		t.Errorf("second Adopt --force modified config band:\n%s", again)
	}
	assertConfigBandBoundaries(t, got)
}

// assertConfigBandBoundaries tests empty and malformed config band handling in repositoryConfigBand.
func assertConfigBandBoundaries(t *testing.T, harness string) {
	t.Helper()
	emptyCfg := "<!-- praetor:head -->\n\n<!-- praetor:config -->\n\n<!-- praetor:tail -->\n"
	if got, err := repositoryConfigBand(emptyCfg, harness); err != nil || got != "" {
		t.Errorf("empty config band: got (%q, %v), want empty", got, err)
	}
	malformed := "<!-- praetor:head -->\n\n<!-- praetor:config -->\n<!-- praetor:register:start -->\noverride\n<!-- praetor:tail -->\n"
	if _, err := repositoryConfigBand(malformed, harness); err == nil {
		t.Error("unterminated register marker in config band: want error, got nil")
	}
}

var errSPDXScanCap = errors.New("spdx scan exceeds entry cap")

// TestAdoptTestdataSPDXLicensesBackedByLicenseFiles ensures every SPDX license tag in
// internal/adopt/testdata resolves to an existing LICENSES/<id>.txt file.
func TestAdoptTestdataSPDXLicensesBackedByLicenseFiles(t *testing.T) {
	const tag = "SPDX-" + "License-Identifier:"
	licensesDir := filepath.Join("..", "..", "LICENSES")
	if missing := unbackedSPDXInText(tag+" CC-BY-SA-4.0\n", licensesDir); len(missing) != 1 || missing[0] != "CC-BY-SA-4.0" {
		t.Fatalf("negative check failed to flag CC-BY-SA-4.0: %v", missing)
	}
	if missing := unbackedSPDXInText(tag+" EUPL-1.2 AND MIT\n", licensesDir); len(missing) != 0 {
		t.Fatalf("boundary compound check flagged backed licenses: %v", missing)
	}
	missing, err := scanTestdataSPDXLicenses("testdata", licensesDir, maxSnapshotEntries)
	if err != nil {
		t.Fatalf("walk testdata: %v", err)
	}
	for _, m := range missing {
		t.Error(m)
	}
}

// TestAdoptTestdataSPDXScan_3D verifies positive, negative (cap truncation, unbacked license)
// and boundary (exact limit) behavior of scanTestdataSPDXLicenses.
func TestAdoptTestdataSPDXScan_3D(t *testing.T) {
	licensesDir := filepath.Join("..", "..", "LICENSES")
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "// SPDX-License-Identifier: MIT\n")
	mustWrite(t, filepath.Join(dir, "b.txt"), "// SPDX-License-Identifier: EUPL-1.2\n")

	missing, err := scanTestdataSPDXLicenses(dir, licensesDir, 2)
	if err != nil || len(missing) != 0 {
		t.Fatalf("positive: got missing=%v, err=%v; want empty, nil", missing, err)
	}
	if _, err := scanTestdataSPDXLicenses(dir, licensesDir, 1); !errors.Is(err, errSPDXScanCap) {
		t.Fatalf("negative cap: got err=%v, want %v", err, errSPDXScanCap)
	}

	badDir := t.TempDir()
	mustWrite(t, filepath.Join(badDir, "bad.txt"), "// SPDX-License-Identifier: CC-BY-SA-4.0\n")
	badMissing, err := scanTestdataSPDXLicenses(badDir, licensesDir, 2)
	if err != nil || len(badMissing) != 1 {
		t.Fatalf("negative license: got missing=%v, err=%v; want 1 missing, nil err", badMissing, err)
	}

	emptyDir := t.TempDir()
	if _, err := scanTestdataSPDXLicenses(emptyDir, licensesDir, 0); err != nil {
		t.Fatalf("boundary empty: got err=%v, want nil", err)
	}
}

// scanTestdataSPDXLicenses walks root up to maxEntries and returns findings for SPDX tags lacking license files.
func scanTestdataSPDXLicenses(root, licensesDir string, maxEntries int) ([]string, error) {
	var missing []string
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		count++
		if count > maxEntries {
			return fmt.Errorf("%s exceeds max entries (%d): %w", root, maxEntries, errSPDXScanCap)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if unbacked := unbackedSPDXInText(string(data), licensesDir); len(unbacked) > 0 {
			rel := filepath.ToSlash(path)
			missing = append(missing, fmt.Sprintf("%s declares SPDX license without LICENSES/<id>.txt: %v", rel, unbacked))
		}
		return nil
	})
	if err != nil {
		return missing, err
	}
	return missing, nil
}

// unbackedSPDXInText extracts SPDX license identifiers from text that lack corresponding files in licensesDir.
func unbackedSPDXInText(text, licensesDir string) []string {
	const tag = "SPDX-" + "License-Identifier:"
	var missing []string
	lines := strings.SplitN(text, "\n", maxHarnessLines)
	for i := 0; i < len(lines) && i < maxHarnessLines; i++ {
		_, rest, ok := strings.Cut(lines[i], tag)
		if !ok {
			continue
		}
		rest = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "-->"))
		for _, tok := range strings.Fields(rest) {
			if tok == "AND" || tok == "OR" || tok == "WITH" || tok == "#" {
				continue
			}
			if _, err := os.Stat(filepath.Join(licensesDir, tok+".txt")); err != nil {
				missing = append(missing, tok)
			}
		}
	}
	return missing
}
