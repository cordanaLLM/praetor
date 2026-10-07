// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/hisscoverage"
)

// #844: the HISS catalog owns every rule title, yet a coverage file whose title differed from it
// failed `hiss coverage --verify`, so each catalog retitle broke every adopter's hook.

// coverageClaim is one rule entry's coverage block: a manual claim needs no fixture.
const coverageClaim = "    coverage:\n      - language: go\n        state: manual\n        rationale: upheld by review\n"

// coverageRepo stages body as the HISS coverage file of a fresh repository root.
func coverageRepo(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(hisscoverage.CatalogFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write coverage file: %v", err)
	}
	return root
}

// catalogTitle is the HISS catalog's title of id.
func catalogTitle(t *testing.T, id string) string {
	t.Helper()
	rule, ok := hisscatalog.LookupRule(id)
	if !ok {
		t.Fatalf("%s missing from the HISS catalog", id)
	}
	return rule.Title
}

// runCoverage runs `hiss coverage` with args and returns its standard output.
func runCoverage(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return dispatchCommand("hiss", append([]string{"coverage"}, args...)) })
}

func TestRunHissCoverage_Positive_OmittedTitleRendersTheCatalogTitle(t *testing.T) {
	root := coverageRepo(t, "version: 1\nrules:\n  - id: HISS-14\n"+coverageClaim)
	out, err := runCoverage(t, "--path="+root, "--verify")
	if err != nil {
		t.Fatalf("an omitted title must pass: %v\n%s", err, out)
	}
	mustContain(t, out, "  HISS-14 "+catalogTitle(t, "HISS-14")+"\n", "[PASS]")
	if strings.Contains(out, "[WARN]") {
		t.Fatalf("an omitted title must not warn:\n%s", out)
	}
}

func TestRunHissCoverage_Negative_DriftedTitleWarnsWithTheCatalogText(t *testing.T) {
	root := coverageRepo(t, "version: 1\nrules:\n  - id: HISS-14\n    title: Append-Only ABI\n"+coverageClaim)
	out, err := runCoverage(t, "--path="+root, "--verify")
	if err != nil {
		t.Fatalf("a drifted title must warn, not fail: %v\n%s", err, out)
	}
	current := catalogTitle(t, "HISS-14")
	mustContain(t, out,
		"  HISS-14 "+current+"\n",
		`[WARN] rule HISS-14 title "Append-Only ABI" differs from the HISS catalog title "`+current+`"`,
		"--sync-titles --write", "[PASS]")
}

func TestRunHissCoverage_Positive_SyncTitlesFixesElevenDriftedTitles(t *testing.T) {
	var body strings.Builder
	body.WriteString("version: 1\nrules:\n")
	for n := 1; n <= 11; n++ {
		fmt.Fprintf(&body, "  - id: HISS-%02d\n    title: Earlier name %d\n%s", n, n, coverageClaim)
	}
	root := coverageRepo(t, body.String())
	path := filepath.Join(root, filepath.FromSlash(hisscoverage.CatalogFile))

	out, err := runCoverage(t, "--path="+root, "--sync-titles")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	mustContain(t, out, "11 stale (dry run, nothing written)",
		`HISS-01 "Earlier name 1" -> "`+catalogTitle(t, "HISS-01")+`"`, "Rerun with --sync-titles --write")
	if data, _ := os.ReadFile(path); string(data) != body.String() {
		t.Fatal("the dry run must leave the coverage file untouched")
	}

	out, err = runCoverage(t, "--path="+root, "--sync-titles", "--write")
	if err != nil {
		t.Fatalf("write: %v\n%s", err, out)
	}
	mustContain(t, out, "11 stale (rewritten)", "Rewrote 11 title(s)")

	out, err = runCoverage(t, "--path="+root, "--verify")
	if err != nil || strings.Contains(out, "[WARN]") {
		t.Fatalf("the synced file must verify without a warning: %v\n%s", err, out)
	}
	out, err = runCoverage(t, "--path="+root, "--sync-titles")
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	mustContain(t, out, "titles: in sync")
}

func TestRunHissCoverage_Negative_UnknownRuleStillFails(t *testing.T) {
	root := coverageRepo(t, "version: 1\nrules:\n  - id: HISS-99\n    title: Invented\n"+coverageClaim)
	for _, mode := range []string{"--verify", "--sync-titles"} {
		_, err := runCoverage(t, "--path="+root, mode)
		mustErrContain(t, err, "rule HISS-99 is not a registered invariant")
	}
}

func TestRunHissCoverage_Boundary_WriteBelongsToSyncTitles(t *testing.T) {
	root := coverageRepo(t, "version: 1\nrules:\n  - id: HISS-14\n"+coverageClaim)
	_, err := runCoverage(t, "--path="+root, "--write")
	mustErrContain(t, err, "--write applies only with --sync-titles")
	_, err = runCoverage(t, "--path="+root, "--sync-titles", "--verify")
	mustErrContain(t, err, "separate runs")
}

// #571: the top-level help says to run `<command> -h`, but `hiss` read every first token as
// a subcommand name, so -h, --help and help failed with "unknown hiss subcommand".

func TestRunHiss_Positive_HelpTokensPrintUsageAndSucceed(t *testing.T) {
	for _, tok := range []string{"-h", "--help", "help"} {
		out, err := captureStdout(t, func() error { return dispatchCommand("hiss", []string{tok}) })
		if err != nil {
			t.Fatalf("hiss %s must exit success, got %v", tok, err)
		}
		mustContain(t, out, "Usage: praetorctl hiss", "coverage [--path=.] [--verify]")
	}
}

func TestRunHiss_Negative_UnknownSubcommandIsRejected(t *testing.T) {
	err := dispatchCommand("hiss", []string{"bogus"})
	mustErrContain(t, err, "unknown hiss subcommand: bogus")
}

func TestRunHiss_Boundary_NoArgsPrintsUsageAndSucceeds(t *testing.T) {
	out, err := captureStdout(t, func() error { return dispatchCommand("hiss", nil) })
	if err != nil {
		t.Fatalf("hiss with no args must exit success, got %v", err)
	}
	mustContain(t, out, "Usage: praetorctl hiss")
}
