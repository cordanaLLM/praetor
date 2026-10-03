package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/workstation"
)

// refreshFixture is a committed engine checkout on branch main whose running build names
// fixtureEngineModule, with the default install manifest redirected into a temp directory.
func refreshFixture(t *testing.T) (dir, head, manifestPath string) {
	t.Helper()
	dir, head = newEngineContextFixture(t)
	if _, err := runGitFixture(t, dir, "branch", "-M", "main"); err != nil {
		t.Fatal(err)
	}
	useEngineBuild(t, head)
	manifestPath = filepath.Join(t.TempDir(), "config", "install.json")
	previous := installManifestPath
	t.Cleanup(func() { installManifestPath = previous })
	installManifestPath = func() (string, error) { return manifestPath, nil }
	return dir, head, manifestPath
}

func runGitFixture(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	return util.RunGit(t.Context(), dir, args...)
}

func runRefreshCmd(t *testing.T, args ...string) (workstation.RefreshResult, error) {
	t.Helper()
	out, err := captureStdout(t, func() error {
		return runWorkstationInstall(context.Background(), append([]string{"--if-stale"}, args...))
	})
	if err != nil {
		return workstation.RefreshResult{}, err
	}
	var result workstation.RefreshResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("--if-stale must print one JSON result: %v\n%s", err, out)
	}
	return result, nil
}

// writeManifestAt writes a manifest at commit that records every installed binary, so only the
// commit decides whether a refresh is due.
func writeManifestAt(t *testing.T, path, commit string) {
	t.Helper()
	manifest := config.InstallManifest{
		Version: config.InstallManifestVersion, EngineCommit: commit,
		InstalledAt: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		BinDir:      filepath.Join(filepath.Dir(path), "bin"),
		Binaries:    map[string]config.InstalledBinary{},
	}
	for _, name := range config.InstalledBinaryNames() {
		manifest.Binaries[name] = config.InstalledBinary{SHA256: strings.Repeat("a", 64)}
	}
	if err := config.WriteInstallManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
}

// Positive: --if-stale reaches the engine with the default manifest and the default update
// branch; an install at the checkout HEAD on main is reported current, not rebuilt.
func TestRunWorkstationRefresh_Positive_ReportsCurrentInstall(t *testing.T) {
	dir, head, manifestPath := refreshFixture(t)
	writeManifestAt(t, manifestPath, head)
	result, err := runRefreshCmd(t, "--source", dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Refreshed || !strings.Contains(result.Reason, "is not behind the checkout HEAD") {
		t.Fatalf("an install at HEAD must be reported current: %+v", result)
	}
}

// Negative: no --source is refused; a checkout of another module never reads the manifest,
// even an undecodable one; the default update branch keeps a topic branch from refreshing.
func TestRunWorkstationRefresh_Negative(t *testing.T) {
	if _, err := runRefreshCmd(t); err == nil {
		t.Fatal("--if-stale without --source was accepted")
	}
	dir, _, manifestPath := refreshFixture(t)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := runRefreshCmd(t, "--source", t.TempDir())
	if err != nil || result.Refreshed || !strings.Contains(result.Reason, "not a checkout of this engine's module") {
		t.Fatalf("foreign checkout: %+v, %v", result, err)
	}
	if _, err := runRefreshCmd(t, "--source", dir); err == nil {
		t.Fatal("an undecodable manifest must be an error once the checkout qualifies")
	}
	writeManifestAt(t, manifestPath, strings.Repeat("0", 40))
	if _, err := runGitFixture(t, dir, "checkout", "-q", "-b", "topic"); err != nil {
		t.Fatal(err)
	}
	result, err = runRefreshCmd(t, "--source", dir)
	if err != nil || !strings.Contains(result.Reason, `checkout is on "topic", not the update branch "main"`) {
		t.Fatalf("topic branch: %+v, %v", result, err)
	}
}

// Boundary: with nothing installed the refresh names the manifest it looked for, and an
// explicit relative --manifest is resolved against the working directory.
func TestRunWorkstationRefresh_Boundary_NothingInstalled(t *testing.T) {
	dir, _, manifestPath := refreshFixture(t)
	result, err := runRefreshCmd(t, "--source", dir)
	if err != nil || result.Refreshed || !strings.Contains(result.Reason, "no install manifest at "+manifestPath) {
		t.Fatalf("default manifest: %+v, %v", result, err)
	}
	t.Chdir(t.TempDir())
	result, err = runRefreshCmd(t, "--source", dir, "--manifest", "relative.json")
	want := filepath.Join(mustGetwd(t), "relative.json")
	if err != nil || !strings.Contains(result.Reason, "no install manifest at "+want) {
		t.Fatalf("relative --manifest: %+v, %v", result, err)
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}
