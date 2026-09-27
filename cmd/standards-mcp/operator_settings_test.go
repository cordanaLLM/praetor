package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/needs"
)

// TestMain keeps every tool call off the operator's own settings: no install manifest and no
// settings or framework environment variable applies unless a test sets one.
func TestMain(m *testing.M) {
	installManifestPath = func() (string, error) { return "", nil }
	for _, name := range []string{config.FleetConfigEnv, config.WorkstationConfigEnv, needs.FrameworkDirEnv} {
		if err := os.Unsetenv(name); err != nil {
			fmt.Fprintf(os.Stderr, "isolate %s: %v\n", name, err)
			os.Exit(2)
		}
	}
	os.Exit(m.Run())
}

// selectAcmeTarget points PRAETOR_WORKSTATION_CONFIG at a workstation document whose go
// target is example.com/acme/kit, declared by a contract that replaces pgx.
func selectAcmeTarget(t *testing.T) {
	t.Helper()
	selectAcmeContract(t, "version: 1\nframework: example.com/acme/kit\npackages:\n"+
		"  - import: example.com/acme/kit/db\n    capabilities: [db.postgres]\n    replaces: [github.com/jackc/pgx]\n")
}

// selectAcmeKit is selectAcmeTarget with the placeholder go contract of the needs engine
// tests (internal/needs/testdata/contracts/kit.capabilities.yaml), whose package paths
// (db/pgx, core/config, core/log, ogenkit) a checkout fixture provides. It returns the
// configured target, for calls that bypass the operator settings.
func selectAcmeKit(t *testing.T) needs.Targets {
	t.Helper()
	contract, err := os.ReadFile(filepath.Join("..", "..", "internal", "needs", "testdata", "contracts", "kit.capabilities.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	path := selectAcmeContract(t, string(contract))
	return needs.Targets{"go": {Module: "example.com/acme/kit", BuilderKits: []string{"acme/kit"}, Contract: path}}
}

// selectAcmeContract points PRAETOR_WORKSTATION_CONFIG at a workstation document whose go
// target is example.com/acme/kit, declared by contract, and returns the contract's path.
func selectAcmeContract(t *testing.T, contract string) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, "kit.capabilities.yaml", contract)
	writeFixtureFile(t, dir, "workstation.yaml", "framework: {targets: {go: {module: example.com/acme/kit, "+
		"builder_kits: [acme/kit], contract: kit.capabilities.yaml}}}\n")
	t.Setenv(config.WorkstationConfigEnv, filepath.Join(dir, "workstation.yaml"))
	return filepath.Join(dir, "kit.capabilities.yaml")
}

// The MCP report selects the operator settings at call time, as hook does, and reports the
// configured target; with none configured it says so.
func TestNeedsReportHonoursConfiguredTarget(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, "go.mod", "module example.com/consumer\n\ngo 1.27\nrequire github.com/jackc/pgx/v5 v5.7.2\n")
	selectAcmeTarget(t)
	declared := callTool(t, srv, "standards_needs_report", nil)
	expectText(t, "configured target", declared, "Framework: example.com/acme/kit (declared) | Mapping availability: 100.0%")
	expectText(t, "declared contract", declared, "Capability contract: kit.capabilities.yaml (declared packages, not source-observed)")
	expectText(t, "contract replacement", declared, "replacement candidate: example.com/acme/kit/db")
	if strings.Contains(declared.Content[0].Text, "golusoris") {
		t.Fatalf("a configured target reported built-in framework data:\n%s", declared.Content[0].Text)
	}

	// Negative: an invalid settings document is an error result, never a silent default.
	invalid := filepath.Join(t.TempDir(), "workstation.yaml")
	writeFixtureFile(t, filepath.Dir(invalid), "workstation.yaml", "framework: {targets: {cobol: {}}}\n")
	t.Setenv(config.WorkstationConfigEnv, invalid)
	expectError(t, "invalid settings", callTool(t, srv, "standards_needs_report", nil), "Failed to load operator settings")

	// Boundary: with no document selected there is no framework, and no built-in one.
	t.Setenv(config.WorkstationConfigEnv, "")
	unset := callTool(t, srv, "standards_needs_report", nil)
	expectText(t, "not configured", unset, "Framework: "+needs.FrameworkNotConfiguredText+" | Mapping availability: n/a (no target framework configured)")
	expectText(t, "not configured basis", unset, "Coverage basis: not-configured; builds and tests not run")
	if strings.Contains(unset.Content[0].Text, "%") {
		t.Fatalf("an unconfigured report rendered a percentage:\n%s", unset.Content[0].Text)
	}
}

// The MCP report shows a deprecated .needs.yaml key the scan read through its alias, and
// nothing once the manifest uses the new key.
func TestNeedsReportShowsDeprecatedKey(t *testing.T) {
	srv, root := newFixtureServer(t)
	manifest := "version: 1\nrepository: fixture\nlanguage: go\ncapabilities:\n  required: []\n" +
		"dependencies:\n  - package: github.com/jackc/pgx/v5\n    capability: db.postgres\n    status: covered\n    %s: example.com/acme/kit/db\n"
	writeFixtureFile(t, root, ".needs.yaml", fmt.Sprintf(manifest, "golusoris_replacement"))
	legacy := callTool(t, srv, "standards_needs_report", nil)
	expectText(t, "deprecation", legacy, "Deprecated input: the .needs.yaml key golusoris_replacement is deprecated")

	writeFixtureFile(t, root, ".needs.yaml", fmt.Sprintf(manifest, "framework_replacement"))
	current := callTool(t, srv, "standards_needs_report", nil)
	if strings.Contains(current.Content[0].Text, "Deprecated input") {
		t.Fatalf("a manifest using the new key reported a deprecation:\n%s", current.Content[0].Text)
	}
}
