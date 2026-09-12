package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/needs"
)

func TestNeedsReportDoesNotTreatEmptyFrameworkAsCovered(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, "go.mod", "module example.com/consumer\n\ngo 1.27\nrequire github.com/jackc/pgx/v5 v5.7.2\n")
	if err := os.Mkdir(filepath.Join(root, "framework"), 0o700); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, srv, "standards_needs_report", map[string]any{"framework": "framework"})
	expectText(t, "empty framework", result, "Mapping availability: 0.0%")
	expectText(t, "unverified availability", result, "Coverage basis: source-observed; builds and tests not run")
	if strings.Contains(result.Content[0].Text, "✓") {
		t.Fatalf("empty framework reported a supported replacement: %s", result.Content[0].Text)
	}
}

func TestNeedsReportUsesObservedPackageAndRejectsMissingFramework(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, "go.mod", "module example.com/consumer\n\ngo 1.27\nrequire github.com/jackc/pgx/v5 v5.7.2\n")
	writeFixtureFile(t, root, "framework/go.mod", "module example.com/framework\ngo 1.27\n")
	writeFixtureFile(t, root, "framework/db/pgx/doc.go", "package pgx\ntype Available struct{}\n")
	result := callTool(t, srv, "standards_needs_report", map[string]any{"framework": "framework"})
	expectText(t, "available source", result, "Mapping availability: 100.0%")
	expectText(t, "fork package", result, "example.com/framework/db/pgx")
	expectText(t, "unverified source", result, "Coverage basis: source-observed; builds and tests not run")
	declared := callTool(t, srv, "standards_needs_report", nil)
	expectText(t, "declared only", declared, "Coverage basis: catalog-declared; builds and tests not run")
	missing := callTool(t, srv, "standards_needs_report", map[string]any{"framework": "missing"})
	expectError(t, "missing selected framework", missing, "open selected framework")
}

func TestNeedsMCPReportMatchesMigrationCandidateEvidence(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, "go.mod", "module example.org/consumer\ngo 1.27\nrequire github.com/jackc/pgx/v5 v5.7.2\n")
	writeFixtureFile(t, root, "main.go", "package main\nimport \"github.com/jackc/pgx/v5\"\nfunc main() { _ = pgx.Connect }\n")
	writeFixtureFile(t, root, "framework/go.mod", "module example.org/fork\ngo 1.27\n")
	framework := filepath.Join(root, "framework")
	for _, tc := range []struct {
		source string
		score  float64
	}{{"package pgx\n", 0}, {"package pgx\ntype Exists struct{}\n", 100}} {
		writeFixtureFile(t, root, "framework/db/pgx/doc.go", tc.source)
		result := callTool(t, srv, "standards_needs_report", map[string]any{"framework": "framework"})
		expectText(t, "same mapping score", result, fmt.Sprintf("Mapping availability: %.1f%%", tc.score))
		plan, err := needs.PlanMigration(t.Context(), root, framework)
		if err != nil {
			t.Fatal(err)
		}
		epic, err := needs.GeneratePreMigrationEpic(t.Context(), root, framework)
		if err != nil {
			t.Fatal(err)
		}
		if plan.MappingAvailability != tc.score || epic.ReadinessScore != tc.score || epic.CoverageBasis != plan.CoverageBasis {
			t.Fatalf("transport/candidate mismatch: %+v %+v", plan, epic)
		}
		expectText(t, "same source basis", result, plan.CoverageBasis)
	}
}
