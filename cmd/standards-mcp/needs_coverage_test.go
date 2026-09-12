package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
