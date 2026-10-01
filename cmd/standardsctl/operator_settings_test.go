package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// TestMain keeps every test off the operator's own settings: no install manifest and no
// settings or framework environment variable reaches a command unless a test sets one, and no
// forge token, so the audit's live Actions checks never ask the real forge.
func TestMain(m *testing.M) {
	installManifestPath = func() (string, error) { return "", nil }
	resolveActionsToken = func(context.Context) string { return "" }
	for _, name := range []string{config.FleetConfigEnv, config.WorkstationConfigEnv, frameworkDirEnv} {
		if err := os.Unsetenv(name); err != nil {
			fmt.Fprintf(os.Stderr, "isolate %s: %v\n", name, err)
			os.Exit(2)
		}
	}
	os.Exit(m.Run())
}

// acmeWorkstation writes a workstation document whose go target is example.com/acme/kit,
// declared by a contract beside it that replaces pgx with the kit's db package.
func acmeWorkstation(t *testing.T) string {
	t.Helper()
	return acmeWorkstationWith(t, "version: 1\nframework: example.com/acme/kit\npackages:\n"+
		"  - import: example.com/acme/kit/db\n    capabilities: [db.postgres]\n    replaces: [github.com/jackc/pgx]\n")
}

// acmeKitWorkstation is acmeWorkstation with the placeholder go contract of the needs
// engine tests (internal/needs/testdata/contracts/kit.capabilities.yaml), whose package
// paths (db/pgx, core/config, core/log, ogenkit) a checkout fixture provides.
func acmeKitWorkstation(t *testing.T) string {
	t.Helper()
	contract, err := os.ReadFile(filepath.Join("..", "..", "internal", "needs", "testdata", "contracts", "kit.capabilities.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return acmeWorkstationWith(t, string(contract))
}

// acmeWorkstationWith writes the acme workstation document with contract as its go
// target's contract and returns the document's path.
func acmeWorkstationWith(t *testing.T, contract string) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, "kit.capabilities.yaml", contract)
	writeFixtureFile(t, dir, "workstation.yaml", "framework:\n  targets:\n    go:\n      module: example.com/acme/kit\n"+
		"      builder_kits: [acme/kit]\n      contract: kit.capabilities.yaml\n  migration_branch: refactor/acme-adoption\n"+
		"forge:\n  default_owner: acme\n")
	return filepath.Join(dir, "workstation.yaml")
}

func parseSettingsFlags(t *testing.T, args ...string) *operatorSettingsFlags {
	t.Helper()
	fs := flag.NewFlagSet("settings test", flag.ContinueOnError)
	opts := registerOperatorSettingsFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return opts
}

func TestOperatorSettingsFlags_Positive(t *testing.T) {
	workstation := acmeWorkstation(t)
	for name, opts := range map[string]*operatorSettingsFlags{
		"flag": parseSettingsFlags(t, "--workstation-config="+workstation),
		"env": func() *operatorSettingsFlags {
			t.Setenv(config.WorkstationConfigEnv, workstation)
			return parseSettingsFlags(t)
		}(),
	} {
		settings, err := opts.load(t.Context())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if settings.Framework.Targets["go"].Module != "example.com/acme/kit" || settings.Forge.DefaultOwner != "acme" {
			t.Fatalf("%s: settings = %+v %+v", name, settings.Framework, settings.Forge)
		}
	}
}

func TestOperatorSettingsFlags_Negative(t *testing.T) {
	invalid := filepath.Join(t.TempDir(), "workstation.yaml")
	writeFixtureFile(t, filepath.Dir(invalid), "workstation.yaml", "framework: {targets: {cobol: {}}}\n")
	if _, err := parseSettingsFlags(t, "--workstation-config="+invalid).load(t.Context()); err == nil ||
		!strings.Contains(err.Error(), "load operator settings") || !strings.Contains(err.Error(), "cobol") {
		t.Fatalf("invalid document: %v", err)
	}
	if _, err := parseSettingsFlags(t, "--fleet-config="+filepath.Join(t.TempDir(), "missing.yaml")).load(t.Context()); err == nil {
		t.Fatal("a missing selected document loaded")
	}
	if _, err := loadNeedsSelection(t.Context(), parseSettingsFlags(t, "--workstation-config="+invalid)); err == nil {
		t.Fatal("needs commands must refuse an invalid settings document")
	}
}

// Boundary: the default manifest is read when --manifest is not given, and an explicit empty
// --manifest selects none, so an unreadable default manifest is bypassed.
func TestOperatorSettingsFlags_BoundaryManifest(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "install.json")
	writeFixtureFile(t, filepath.Dir(broken), "install.json", "{not json")
	previous := installManifestPath
	installManifestPath = func() (string, error) { return broken, nil }
	t.Cleanup(func() { installManifestPath = previous })

	if _, err := parseSettingsFlags(t).load(t.Context()); err == nil {
		t.Fatal("the default manifest was not read")
	}
	if _, err := defaultOperatorSettingsFlags().load(t.Context()); err == nil {
		t.Fatal("the environment-and-manifest selection skipped the manifest")
	}
	settings, err := parseSettingsFlags(t, "--manifest=").load(t.Context())
	if err != nil || len(settings.Framework.Targets) != 0 || settings.Hooks.Scope != config.HookScopeGoverned {
		t.Fatalf("explicit empty manifest = %+v, %v; want the built-in defaults", settings.Framework, err)
	}
	installManifestPath = func() (string, error) { return "", os.ErrNotExist }
	if settings, err := defaultOperatorSettingsFlags().load(t.Context()); err != nil || len(settings.Framework.Targets) != 0 {
		t.Fatalf("no configuration directory = %v; want the built-in defaults", err)
	}
}
