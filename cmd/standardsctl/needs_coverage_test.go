package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/needs"
)

// A checkout without its own capabilities.yaml is observed against the configured go
// contract; with no target configured the report says so instead of scoring.
func TestNeedsReportUsesExplicitFrameworkCoverage(t *testing.T) {
	repo := newNeedsRepo(t)
	writeFixtureFile(t, repo, "go.mod", "module example.com/consumer\ngo 1.27\nrequire github.com/jackc/pgx/v5 v5.7.2\n")
	writeFixtureFile(t, repo, "main.go", "package main\nfunc main() {}\n")
	t.Setenv(config.WorkstationConfigEnv, acmeKitWorkstation(t))
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
	mustContain(t, declared, "Coverage basis: catalog-declared; builds and tests not run", "Capability contract: kit.capabilities.yaml")
	if _, err := run(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing selected framework must preserve filesystem error: %v", err)
	}
	// Boundary: with no target configured the declaration is not configured, never 0%.
	t.Setenv(config.WorkstationConfigEnv, "")
	unset, err := run("")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, unset, "Framework: "+needs.FrameworkNotConfiguredText, "Mapping availability: n/a (no target framework configured)",
		"Coverage basis: not-configured")
	if strings.Contains(unset, "%") {
		t.Fatalf("an unconfigured report rendered a percentage:\n%s", unset)
	}
}

func TestNeedsLibraryRelationshipsUseSharedFormatter(t *testing.T) {
	repo := newNeedsRepo(t)
	writeFixtureFile(t, repo, "go.mod", "module example.com/consumer\ngo 1.27\nrequire (\ngo.uber.org/fx v1.24.0\ngithub.com/knadh/koanf/v2 v2.3.0\ngithub.com/lmittmann/tint v1.1.2\ngithub.com/ogen-go/ogen v1.13.0\n)\n")
	writeFixtureFile(t, repo, "main.go", "package main\nimport _ \"log/slog\"\nfunc main() {}\n")
	t.Setenv(config.WorkstationConfigEnv, acmeKitWorkstation(t))
	framework := t.TempDir()
	writeFixtureFile(t, framework, "go.mod", "module example.com/framework\ngo 1.27\n")
	for _, name := range []string{"core/config", "core/log", "ogenkit"} {
		writeFixtureFile(t, framework, name+"/adapter.go", "package adapter\ntype Available struct{}\n")
	}
	selection, err := loadNeedsSelection(t.Context(), defaultOperatorSettingsFlags())
	if err != nil {
		t.Fatal(err)
	}
	index, err := needs.InspectFramework(t.Context(), selection.frameworkSource(framework, true))
	if err != nil {
		t.Fatal(err)
	}
	report, err := needs.ScanRepoWithFramework(t.Context(), repo, index, selection.registry)
	if err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error {
		return dispatchCommand("needs", []string{"report", "--path=" + repo, "--framework=" + framework})
	})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, output, needs.FormatLibraryRelationships(report), "foundation; retain library", "wrapped-by", "tooling", "log/slog", "basis=catalog-declared", "basis=source-observed")
	if strings.Contains(output, "Drop-In") || strings.Contains(output, "replacement candidate:") || report.Readiness.TotalThirdPartyDeps != 4 {
		t.Fatalf("library roles became replacement claims or changed dependency counts: %s", output)
	}
}
