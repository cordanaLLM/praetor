// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package hindsight

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A failing optional source must be named in the report instead of vanishing: the dedupe
// scan refuses an unparsable Go file and the doc catalog refuses malformed JSON, while the
// flavor source still contributes its fact.
func TestDistillWorkspace_Negative_FailingSourcesBecomeWarnings(t *testing.T) {
	root := t.TempDir()
	writeDistillerFile(t, root, "broken.go", "package broken\n\nfunc (\n")
	writeDistillerFile(t, root, filepath.Join(".workingdir", "docs", "catalog.json"), "{not json")

	report, err := DistillWorkspace(context.Background(), root)
	if err != nil || report == nil {
		t.Fatalf("partial distillation must still report: %+v, %v", report, err)
	}
	if len(report.Warnings) != 2 {
		t.Fatalf("want dedupe and package docs warnings, got %q", report.Warnings)
	}
	if !strings.Contains(report.Warnings[0], "distill workspace dedupe") ||
		!strings.Contains(report.Warnings[1], "distill workspace package docs") {
		t.Errorf("warnings do not name the failed sources: %q", report.Warnings)
	}
	if report.Categories[CategoryFlavor] != 1 || report.TotalFacts != 1 {
		t.Errorf("surviving flavor fact missing: %+v", report.Categories)
	}
}

// The flavor source cannot fail against the built-in registry, so its failure path runs
// through the source seam, alongside the required-source abort.
func TestDistillSources_Negative_FlavorFailureAndRequiredAbort(t *testing.T) {
	ctx := context.Background()
	fact := createFact(CategoryBugRuling, "BUG-1", "Resolved Bug BUG-1.", ".workingdir/BUGS.md", nil)
	report, err := distillSources(ctx, t.TempDir(), []distillSource{
		failingSource("flavor archetype", false),
		factSource("bug ledger", true, fact),
	})
	if err != nil || report == nil {
		t.Fatalf("optional failure aborted the report: %v", err)
	}
	if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "distill workspace flavor archetype: boom") {
		t.Errorf("flavor failure not recorded: %q", report.Warnings)
	}
	if report.TotalFacts != 1 || report.Facts[0].Subject != "BUG-1" {
		t.Errorf("surviving facts lost: %+v", report.Facts)
	}

	aborted, err := distillSources(ctx, t.TempDir(), []distillSource{
		factSource("flavor archetype", false, fact),
		failingSource("bug ledger", true),
	})
	if err == nil || aborted != nil || !strings.Contains(err.Error(), "distill workspace bug ledger") {
		t.Errorf("required failure must abort: %+v, %v", aborted, err)
	}
}

// Every source failing, or every optional source failing while the required one finds
// nothing, is an error rather than an empty success that would overwrite a populated cache.
// No sources and no failures stays an empty success.
func TestDistillSources_Boundary_NothingHarvested(t *testing.T) {
	ctx := context.Background()
	allFail := []distillSource{
		failingSource("flavor archetype", false),
		failingSource("bug ledger", true),
		failingSource("dedupe", false),
		failingSource("package docs", false),
	}
	if report, err := distillSources(ctx, t.TempDir(), allFail); err == nil || report != nil {
		t.Errorf("all four failing sources became a report: %+v", report)
	}

	optionalFail := []distillSource{
		failingSource("flavor archetype", false),
		factSource("bug ledger", true),
		failingSource("dedupe", false),
		failingSource("package docs", false),
	}
	report, err := distillSources(ctx, t.TempDir(), optionalFail)
	if err == nil || report != nil {
		t.Fatalf("empty harvest with failures became a report: %+v", report)
	}
	for _, name := range []string{"flavor archetype", "dedupe", "package docs"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error omits failed source %q: %v", name, err)
		}
	}
	if !errors.Is(err, errSourceFailed) {
		t.Errorf("joined error lost the source cause: %v", err)
	}

	empty, err := distillSources(ctx, t.TempDir(), nil)
	if err != nil || empty == nil || empty.TotalFacts != 0 || len(empty.Warnings) != 0 {
		t.Errorf("no sources must be an empty success: %+v, %v", empty, err)
	}
}

var errSourceFailed = errors.New("boom")

func failingSource(name string, required bool) distillSource {
	return distillSource{name: name, required: required, run: func(context.Context, string) ([]MemoryFact, error) {
		return nil, errSourceFailed
	}}
}

func factSource(name string, required bool, facts ...MemoryFact) distillSource {
	return distillSource{name: name, required: required, run: func(context.Context, string) ([]MemoryFact, error) {
		return facts, nil
	}}
}

func writeDistillerFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
