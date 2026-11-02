// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

// TestAdoptPinReportOutput pins formatting of reconciled engine pin movements and untouched pin status notes.
func TestAdoptPinReportOutput(t *testing.T) {
	old, next := strings.Repeat("a", 40), strings.Repeat("b", 40)
	report := &adopt.AdoptReport{
		ReconciledFiles: []string{".github/workflows/ci.yml"},
		ActionDetails: []adopt.ActionDetail{
			{Path: ".github/workflows/ci.yml", Action: "reconcile", Details: "Moved engine pin " + old + " -> " + next},
		},
	}
	out, err := captureStdout(t, func() error { printAdoptedFiles(report); return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".github/workflows/ci.yml", old, next} {
		if !strings.Contains(out, want) {
			t.Fatalf("pin report lacks %s: %s", want, out)
		}
	}
	report.ActionDetails = []adopt.ActionDetail{{Path: ".github/workflows", Action: "skip", Details: "No workflow engine commit pin declared; untouched"}}
	out, err = captureStdout(t, func() error { printAdoptEnginePinStatus(report); return nil })
	if err != nil || !strings.Contains(out, "No workflow engine commit pin declared; untouched") {
		t.Fatalf("absence not reported: %q: %v", out, err)
	}
}
