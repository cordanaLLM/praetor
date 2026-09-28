package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

// replacedReport is a report whose lefthook.yml was replaced and whose README was reconciled.
func replacedReport(dryRun bool) *adopt.AdoptReport {
	return &adopt.AdoptReport{
		DryRun:          dryRun,
		BaselineStatus:  "skipped",
		ReconciledFiles: []string{"README.md", "lefthook.yml"},
		ActionDetails: []adopt.ActionDetail{
			{Path: "README.md", Action: "reconcile", Details: "Governance block verified"},
			{Path: "lefthook.yml", Action: "replace", Details: "Scaffolded; replaced existing content (-1/+9 lines); backup: .workingdir/adopt-backups/x/lefthook.yml"},
		},
	}
}

// Positive: a replaced file is printed in its own section with its replace detail, and not
// repeated among the reconciled files; a dry run labels the section as planned.
func TestPrintAdoptedFiles_Positive_ReplacedSection(t *testing.T) {
	out, err := captureStdout(t, func() error { printAdoptedFiles(replacedReport(false)); return nil })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Files Reconciled (1):", "~ [SYNC] README.md", "Files Replaced (1):",
		"! [REPL] lefthook.yml", "(-1/+9 lines); backup: .workingdir/adopt-backups/x/lefthook.yml")
	if strings.Count(out, "lefthook.yml ") != 1 {
		t.Fatalf("replaced file listed more than once:\n%s", out)
	}
	out, err = captureStdout(t, func() error { printAdoptedFiles(replacedReport(true)); return nil })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Files Planned for Reconciliation (1):", "Files Planned for Replacement (1):", "! [PLAN]  lefthook.yml")
}

// Negative: a report without a replace entry prints no replaced section and keeps every
// reconciled file.
func TestPrintAdoptedFiles_Negative_NoReplacedSection(t *testing.T) {
	rep := replacedReport(false)
	rep.ActionDetails[1].Action = "merge"
	out, err := captureStdout(t, func() error { printAdoptedFiles(rep); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Replaced") || strings.Contains(out, "Replacement") {
		t.Fatalf("replaced section without a replace entry:\n%s", out)
	}
	mustContain(t, out, "Files Reconciled (2):", "~ [SYNC] lefthook.yml")
}

// Boundary: when every reconciled file was replaced, the reconciled section is left out
// instead of printed empty.
func TestPrintAdoptedFiles_Boundary_OnlyReplacedFiles(t *testing.T) {
	rep := replacedReport(false)
	rep.ReconciledFiles, rep.ActionDetails = []string{"lefthook.yml"}, rep.ActionDetails[1:]
	out, err := captureStdout(t, func() error { printAdoptedFiles(rep); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Files Reconciled") {
		t.Fatalf("empty reconciled section printed:\n%s", out)
	}
	mustContain(t, out, "Files Replaced (1):")
}

// Positive, negative and boundary: the --all-missing summary counts replaced files apart from
// reconciled ones, on success and in the written-before-failure line, and reads 0 replaced for
// a report without a replace entry.
func TestPrintBatchResult_CountsReplacedApart(t *testing.T) {
	out, err := captureStdout(t, func() error {
		printBatchResult("r", false, replacedReport(false), nil)
		printBatchResult("f", false, replacedReport(false), errors.New("step failed"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "  Reconciled: 1 files\n", "  Replaced:   1 files\n",
		"  Written before failure: 0 created, 1 reconciled, 1 replaced\n")
	plain := replacedReport(false)
	plain.ActionDetails[1].Action = "merge"
	out, err = captureStdout(t, func() error { printBatchResult("p", false, plain, nil); return nil })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "  Reconciled: 2 files\n", "  Replaced:   0 files\n")
}
