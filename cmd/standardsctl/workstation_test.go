package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestRunWorkstationRequiresSubcommand(t *testing.T) {
	if err := runWorkstation(nil); err == nil || !strings.Contains(err.Error(), "workstation install") {
		t.Fatalf("empty args must report usage, got %v", err)
	}
}

func TestRunWorkstationRejectsUnknownSubcommand(t *testing.T) {
	if err := runWorkstation([]string{"rollback"}); err == nil || !strings.Contains(err.Error(), "unknown workstation subcommand") {
		t.Fatalf("unknown subcommand accepted: %v", err)
	}
}

func TestRunWorkstationHelp(t *testing.T) {
	if err := runWorkstation([]string{"help"}); err != nil {
		t.Fatalf("help must not error: %v", err)
	}
}

// Negative: --source is required; a bare "workstation install" with no other flags must
// not reach the engine at all.
func TestResolveInstallOptionsRequiresSource(t *testing.T) {
	if _, err := resolveInstallOptions(context.Background(), "", "", "", "", ""); err == nil ||
		!strings.Contains(err.Error(), "--source is required") {
		t.Fatalf("missing --source accepted: %v", err)
	}
}

// Positive: relative --source and --bin-dir become absolute. With neither settings flag
// given, loadInstallSettings never touches the filesystem, so no .standards.yaml is needed.
func TestResolveInstallOptionsAbsolutizesPaths(t *testing.T) {
	dir := t.TempDir()
	opts, err := resolveInstallOptions(context.Background(), dir, filepath.Join(dir, "bin"), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(opts.Checkout) || !filepath.IsAbs(opts.BinDir) {
		t.Fatalf("install options must carry absolute paths: %+v", opts)
	}
	if opts.FleetSettings.Origin != config.SettingsNotConfigured || opts.WorkstationSettings.Origin != config.SettingsNotConfigured {
		t.Fatalf("unset settings flags must report not configured: %+v / %+v", opts.FleetSettings, opts.WorkstationSettings)
	}
}

// The wiring test for settings: a --fleet-config flag reaches config.LoadEffectivePolicyContext
// (proven by its own "read policy source" error against a checkout with no .standards.yaml),
// without this package needing a fully governed repository fixture -- that loader's success
// path is internal/config's own test responsibility (S1).
func TestResolveInstallOptionsFleetConfigReachesSettingsLoader(t *testing.T) {
	dir := t.TempDir()
	fleet := filepath.Join(t.TempDir(), "fleet.yaml")
	if err := os.WriteFile(fleet, []byte("clients: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := resolveInstallOptions(context.Background(), dir, filepath.Join(dir, "bin"), "", fleet, "")
	if err == nil || !strings.Contains(err.Error(), "load settings") {
		t.Fatalf("--fleet-config did not reach the settings loader: %v", err)
	}
}

func TestSettingsDocumentFlag(t *testing.T) {
	if doc := settingsDocumentFlag(""); doc.Origin != config.SettingsNotConfigured || doc.Path != "" {
		t.Fatalf("an empty flag must report not configured: %+v", doc)
	}
	path := filepath.Join(t.TempDir(), "workstation.yaml")
	doc := settingsDocumentFlag(path)
	if doc.Path != path || doc.Origin != config.SettingsFromFlag {
		t.Fatalf("a given flag must be recorded from the flag: %+v", doc)
	}
}

// Positive + boundary: the bin-dir resolution order is explicit flag, then the loaded
// update.bin_dir setting, then the per-OS default -- exercised here without a settings
// document, so it falls through to the default (C1).
func TestResolveInstallBinDirDefaultsThroughC1(t *testing.T) {
	binDir, err := resolveInstallBinDir("", config.DefaultOperatorSettings())
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(binDir) {
		t.Fatalf("default bin directory must be absolute: %q", binDir)
	}
}

func TestResolveInstallBinDirExplicitWins(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "bin")
	settings := config.DefaultOperatorSettings()
	settings.Update.BinDir = filepath.Join(t.TempDir(), "elsewhere")
	binDir, err := resolveInstallBinDir(explicit, settings)
	if err != nil || binDir != explicit {
		t.Fatalf("explicit --bin-dir must win over the setting: %q, %v", binDir, err)
	}
}

// The wiring test: an invalid checkout (no go.mod) fails fast inside the real `go build`
// the engine runs, which proves the CLI flags reached workstation.Install without this
// package building a real repository or paying for a real multi-second compile.
func TestRunWorkstationInstallReachesTheEngine(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	manifestPath := filepath.Join(dir, "config", "install.json")
	err := runWorkstationInstall(context.Background(), []string{
		"--source", dir, "--bin-dir", binDir, "--manifest", manifestPath,
	})
	if err == nil {
		t.Fatal("an empty checkout with no go.mod must fail the build")
	}
	if _, statErr := os.Stat(lockPathForTest(binDir)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a failed install must not leave its lock behind, stat: %v", statErr)
	}
}

func TestRunWorkstationInstallAcceptsNoPositionalArguments(t *testing.T) {
	if err := runWorkstationInstall(context.Background(), []string{"--source", t.TempDir(), "extra"}); err == nil {
		t.Fatal("a positional argument was accepted")
	}
}

// Positive: status against a fresh manifest path prints a report and does not error.
func TestRunWorkstationStatusReachesTheEngine(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "install.json")
	if err := runWorkstationStatus(context.Background(), []string{"--manifest", manifestPath}); err != nil {
		t.Fatalf("status against an uninstalled workstation must not error: %v", err)
	}
}

func TestRunWorkstationStatusAcceptsNoPositionalArguments(t *testing.T) {
	if err := runWorkstationStatus(context.Background(), []string{"unexpected"}); err == nil {
		t.Fatal("a positional argument was accepted")
	}
}

// Positive: --home reaches the client environment resolveStatusOptions builds, and marks
// it foreign (no relocation-variable inheritance) exactly as harvestClientRoots does.
func TestResolveStatusOptionsWiresHomeIntoClientEnv(t *testing.T) {
	home := t.TempDir()
	opts, err := resolveStatusOptions("", "", "", home)
	if err != nil {
		t.Fatal(err)
	}
	if opts.ClientEnv.Home != home || opts.ClientEnv.Getenv != nil {
		t.Fatalf("--home must select a foreign, non-relocated client environment: %+v", opts.ClientEnv)
	}
}

// lockPathForTest mirrors internal/workstation's unexported lockPath so this package's
// wiring test can assert on lock cleanup without exporting installation internals.
func lockPathForTest(binDir string) string {
	return filepath.Join(binDir, ".praetor-workstation-install.lock")
}
