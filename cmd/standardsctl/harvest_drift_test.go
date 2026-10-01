// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/harvester"
)

// driftDevRoot builds a dev root with two committed repositories whose scripts/guard.py
// differ and whose scripts/same.sh match.
func driftDevRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, guard := range map[string]string{"alpha": "block home\n", "org/beta": "block home\nblock users\n"} {
		dir := filepath.Join(root, filepath.FromSlash(name))
		writeFixtureFile(t, dir, "scripts/guard.py", guard)
		writeFixtureFile(t, dir, "scripts/same.sh", "echo same\n")
		initGitFixture(t, dir)
	}
	return root
}

// Positive: the text report lists the drifted path with both variants and the identical path
// with its repositories, and drift alone exits zero.
func TestRunHarvestDrift_Positive_ReportsDriftWithoutFailing(t *testing.T) {
	root := driftDevRoot(t)
	out, err := captureStdout(t, func() error {
		return runHarvestDrift(t.Context(), []string{"--dir", root})
	})
	if err != nil {
		t.Fatalf("drift must not fail the survey: %v\n%s", err, out)
	}
	mustContain(t, out, "=== Fleet Drift Survey ===\n", "Repositories: 2 (surveyed 2, unborn 0, unreadable 0)\n",
		"Drifted copies (1):\n  - scripts/guard.py (2 variants)\n", " lines=1 in alpha\n", " lines=2 -0 +1 in org/beta\n",
		"Identical copies (1):\n  - scripts/same.sh in alpha, org/beta\n", "Survey complete: true (truncated: false)")
	jsonOut, err := captureStdout(t, func() error {
		return runHarvestDrift(t.Context(), []string{"--json", "--dir", root, "--path", "scripts/guard.py"})
	})
	var report harvester.DriftReport
	if err != nil || json.Unmarshal([]byte(jsonOut), &report) != nil {
		t.Fatalf("JSON survey = %q, err %v", jsonOut, err)
	}
	if len(report.Drifted) != 1 || len(report.Identical) != 0 || len(report.Drifted[0].Variants[1].Digest) != len("sha256:")+64 {
		t.Fatalf("JSON report = %+v, want only the selected drifted path with full digests", report)
	}
}

// Negative: an unsafe path and a positional argument are refused before any scan, and an
// unreadable repository makes the survey exit nonzero with the report still printed.
func TestRunHarvestDrift_Negative_RefusesBadInputAndReportsIncomplete(t *testing.T) {
	mustErrContain(t, runHarvestDrift(t.Context(), []string{"--dir", t.TempDir(), "--path", "../outside"}),
		"must name a directory or file inside the repository")
	mustErrContain(t, runHarvestDrift(t.Context(), []string{"--dir", t.TempDir(), "extra"}),
		"accepts no positional arguments")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "broken", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error {
		return runHarvestDrift(t.Context(), []string{"--dir", root})
	})
	mustErrContain(t, err, "drift survey is incomplete")
	mustContain(t, out, "Survey complete: false", "  error: broken: repository identity unknown; not surveyed\n")
}

// Boundary: an empty dev root is a complete survey with no findings, the dispatch table
// reaches the subcommand, and a digest shorter than the display width is shown whole.
func TestRunHarvestDrift_Boundary_EmptyRootAndDispatch(t *testing.T) {
	root := t.TempDir()
	out, err := captureStdout(t, func() error {
		return dispatchCommand("harvest", []string{"drift", "--dir", root})
	})
	if err != nil {
		t.Fatalf("empty dev root: %v\n%s", err, out)
	}
	mustContain(t, out, "Repositories: 0 (surveyed 0, unborn 0, unreadable 0)\n", "Drifted copies (0):\nIdentical copies (0):\n",
		"Survey complete: true (truncated: false); repository inventory complete: true\n")
	usage, err := captureStdout(t, func() error { return dispatchCommand("harvest", []string{"help"}) })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, usage, "  drift [--dir=path] [--path=prefix]... [--json] ")
	if got := shortDriftDigest("sha256:ab"); got != "sha256:ab" {
		t.Fatalf("short digest = %q", got)
	}
	if got := shortDriftDigest("sha256:" + strings.Repeat("f", 64)); got != "sha256:ffffffffffff" {
		t.Fatalf("long digest = %q", got)
	}
}
