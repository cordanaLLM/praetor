package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNeedsReportUsesExplicitFrameworkCoverage(t *testing.T) {
	repo := newNeedsRepo(t)
	writeFixtureFile(t, repo, "go.mod", "module example.com/consumer\ngo 1.27\nrequire github.com/jackc/pgx/v5 v5.7.2\n")
	writeFixtureFile(t, repo, "main.go", "package main\nfunc main() {}\n")
	framework := t.TempDir()
	run := func(selected string) (string, error) {
		return captureStdout(t, func() error {
			return dispatchCommand("needs", []string{"report", "--path=" + repo, "--framework=" + selected})
		})
	}
	before, err := run(framework)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, before, "Mapping availability: 0.0%", "Coverage basis: source-observed; builds and tests not run")
	writeFixtureFile(t, framework, "go.mod", "module example.com/framework\ngo 1.27\n")
	writeFixtureFile(t, framework, "db/pgx/doc.go", "package pgx\ntype Available struct{}\n")
	after, err := run(framework)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, after, "Mapping availability: 100.0%", "example.com/framework/db/pgx", "builds and tests not run")
	declared, err := run("")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, declared, "Coverage basis: catalog-declared; builds and tests not run")
	if _, err := run(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing selected framework must preserve filesystem error: %v", err)
	}
}
