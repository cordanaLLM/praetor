package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/needs"
)

func TestNeedsMigrationAndEpicAgreeWithSelectedReport(t *testing.T) {
	repo := newNeedsRepo(t)
	writeFixtureFile(t, repo, "go.mod", "module example.org/consumer\ngo 1.27\nrequire github.com/jackc/pgx/v5 v5.7.2\n")
	writeFixtureFile(t, repo, "main.go", "package main\nimport \"github.com/jackc/pgx/v5\"\nfunc main() { _ = pgx.Connect }\n")
	framework := t.TempDir()
	writeFixtureFile(t, framework, "go.mod", "module example.org/fork\ngo 1.27\n")
	for _, tc := range []struct{ source, score string }{{"package pgx\n", "0.0%"}, {"package pgx\ntype Exists struct{}\n", "100.0%"}} {
		writeFixtureFile(t, framework, "db/pgx/doc.go", tc.source)
		for _, command := range []string{"report", "migrate", "epic"} {
			out, err := captureStdout(t, func() error {
				return dispatchCommand("needs", []string{command, "--path=" + repo, "--framework=" + framework})
			})
			if err != nil {
				t.Fatal(err)
			}
			mustContain(t, out, tc.score, "source-observed", "example.org/fork", "unverified")
			if command != "report" {
				mustContain(t, out, "candidate", "blocked")
			}
		}
	}
	_, err := captureStdout(t, func() error {
		return dispatchCommand("needs", []string{"migrate", "--path=" + repo, "--framework=" + framework, "--apply"})
	})
	if !errors.Is(err, needs.ErrUnverifiedMigration) {
		t.Fatalf("CLI lost admission error: %v", err)
	}
	for _, command := range []string{"report", "migrate", "epic"} {
		err = dispatchCommand("needs", []string{command, "--path=" + repo, "--framework=" + filepath.Join(framework, "missing")})
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s silently replaced missing selection: %v", command, err)
		}
	}
}
