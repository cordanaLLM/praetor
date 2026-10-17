// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestApplyTemplates_3D(t *testing.T) {
	// Positive: ApplyTemplates creates .gosec.json from GosecTemplate.
	root := t.TempDir()
	report, err := ApplyTemplates(t.Context(), root, "go", []TemplateItem{GosecTemplate()}, ApplyOptions{})
	if err != nil {
		t.Fatalf("ApplyTemplates failed: %v", err)
	}
	if !slices.Contains(report.CreatedTemplates, ".gosec.json") {
		t.Fatalf("expected .gosec.json in CreatedTemplates, got %v", report.CreatedTemplates)
	}
	content, err := os.ReadFile(filepath.Join(root, ".gosec.json"))
	if err != nil {
		t.Fatalf("read .gosec.json: %v", err)
	}
	if !validJSONObject(content) {
		t.Fatalf("expected valid JSON object in .gosec.json, got %s", content)
	}

	// Negative: writing to an unreadable directory reports an error.
	badDir := filepath.Join(t.TempDir(), "blocked")
	if err := os.MkdirAll(filepath.Join(badDir, ".gosec.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	badReport, err := ApplyTemplates(t.Context(), badDir, "go", []TemplateItem{GosecTemplate()}, ApplyOptions{})
	if err == nil || len(badReport.Errors) == 0 {
		t.Fatalf("expected error when destination is a directory, got err=%v report=%+v", err, badReport)
	}

	// Boundary: dry-run mode plans the template without creating it on disk.
	dryRoot := t.TempDir()
	dryReport, err := ApplyTemplates(t.Context(), dryRoot, "go", []TemplateItem{GosecTemplate()}, ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("ApplyTemplates dry run failed: %v", err)
	}
	if !slices.Contains(dryReport.CreatedTemplates, ".gosec.json") {
		t.Fatalf("expected .gosec.json in dry-run CreatedTemplates, got %v", dryReport.CreatedTemplates)
	}
	if _, err := os.Stat(filepath.Join(dryRoot, ".gosec.json")); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create .gosec.json on disk, stat err: %v", err)
	}
}
