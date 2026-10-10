// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
)

// identifiedOutcome is an outcome on branch with a complete identity and no cost fields.
func identifiedOutcome(branch string) router.Outcome {
	return router.Outcome{
		Time:   time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC),
		Task:   "coding",
		Target: "light",
		Branch: branch,
		Result: router.OutcomeOK,
		Identity: router.RunIdentity{
			PhysicalModel:  "claude-3-7-sonnet-20250219",
			Harness:        "claude-code",
			HarnessVersion: "1.0.0",
			PromptDigest:   "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			ContextDigest:  "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
		},
	}
}

func TestCollector_Negative_OutcomeWithoutEstimateIsNotMeasured(t *testing.T) {
	dir := t.TempDir()
	prs := filepath.Join(dir, "prs.json")
	writeFile(t, prs, []byte(forgeRecords))
	log := filepath.Join(dir, "outcomes.jsonl")
	o := identifiedOutcome("feat/x")
	actual := 0.5
	o.ActualCost = &actual
	if err := router.AppendOutcome(context.Background(), log, o); err != nil {
		t.Fatal(err)
	}
	report := collect(t, CollectorOptions{ForgeJSONPath: prs, OutcomeLogPath: log, Milestone: "M1"})
	unit := unitByNumber(t, report, 1)
	if unit.EstimateError != NotMeasured || unit.EstimateErrorAmount != nil {
		t.Fatalf("a run without an estimate must leave the unit not measured, got %q %v", unit.EstimateError, unit.EstimateErrorAmount)
	}
	if unit.ResolvedModel != o.Identity.PhysicalModel || unit.Identity == nil {
		t.Fatalf("identity must still join the unit: %+v", unit)
	}
	if report.MilestoneSummary.EstimateErrorAmount != nil {
		t.Fatalf("summary must not sum an unmeasured unit: %v", *report.MilestoneSummary.EstimateErrorAmount)
	}
}

func TestCollector_Negative_OutcomeLogErrorsFail(t *testing.T) {
	dir := t.TempDir()
	prs := filepath.Join(dir, "prs.json")
	writeFile(t, prs, []byte(forgeRecords))
	corrupt := filepath.Join(dir, "corrupt.jsonl")
	writeFile(t, corrupt, []byte("not json\n"))
	for name, path := range map[string]string{
		"explicit log missing": filepath.Join(dir, "absent.jsonl"),
		"corrupt log":          corrupt,
		"log is a directory":   dir,
	} {
		_, err := NewCollector(CollectorOptions{ForgeJSONPath: prs, OutcomeLogPath: path, Milestone: "M1"}).Collect(context.Background())
		if err == nil || !strings.Contains(err.Error(), "read outcomes") {
			t.Errorf("%s: the ledger must fail instead of skipping outcomes, got %v", name, err)
		}
	}
}

func TestCollector_Boundary_DefaultOutcomeLog(t *testing.T) {
	root := t.TempDir()
	prs := filepath.Join(root, "prs.json")
	writeFile(t, prs, []byte(forgeRecords))
	if _, err := NewCollector(CollectorOptions{Root: root, ForgeJSONPath: prs, Milestone: "M1"}).Collect(context.Background()); err != nil {
		t.Fatalf("a missing default outcome log is an empty history: %v", err)
	}
	logPath := filepath.Join(root, ".workingdir", "routing", "outcomes.jsonl")
	if err := os.MkdirAll(logPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCollector(CollectorOptions{Root: root, ForgeJSONPath: prs, Milestone: "M1"}).Collect(context.Background()); err == nil {
		t.Fatal("an unreadable default outcome log must fail, not be skipped")
	}
}
