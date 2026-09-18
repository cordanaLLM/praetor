// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/clientid"
	"github.com/cordanaLLM/praetor/internal/clientsetup"
)

// Boundary (spec 9.3 W1): status on a workstation with no install manifest yet.
func TestStatusNotInstalled(t *testing.T) {
	report, err := Status(context.Background(), StatusOptions{ManifestPath: filepath.Join(t.TempDir(), "install.json")})
	if err != nil {
		t.Fatal(err)
	}
	if report.Installed || report.Manifest != nil || report.UpToDate {
		t.Fatalf("an absent manifest must report not installed, got %+v", report)
	}
}

// Positive: status reports a held lock without taking it.
func TestStatusReportsLockHeld(t *testing.T) {
	binDir := t.TempDir()
	if err := os.Mkdir(lockPath(binDir), 0o700); err != nil {
		t.Fatal(err)
	}
	report, err := Status(context.Background(), StatusOptions{BinDir: binDir, ManifestPath: filepath.Join(t.TempDir(), "install.json")})
	if err != nil {
		t.Fatal(err)
	}
	if !report.LockHeld {
		t.Fatal("an existing lock directory must be reported")
	}
	if !LockHeld(binDir) {
		t.Fatal("Status must not have taken or released the lock it observed")
	}
}

// Negative: Status requires a context.
func TestStatusRequiresContext(t *testing.T) {
	var noContext context.Context
	if _, err := Status(noContext, StatusOptions{}); err == nil {
		t.Fatal("nil context accepted")
	}
}

// Negative: a manifest that fails to decode is an error, not a silent "not installed".
func TestStatusRejectsUndecodableManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "install.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Status(context.Background(), StatusOptions{ManifestPath: path}); err == nil {
		t.Fatal("an undecodable manifest was accepted")
	}
}

// Positive + negative: per-client configuration-root presence via C1 (roots.go). AGY has a
// root entry (config found or not); every other known client currently has none and
// reports a stated reason instead of a false presence.
func TestStatusClientPresenceViaC1Roots(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := clientsetup.Env{GOOS: "linux", Home: home, DirExists: func(p string) bool {
		info, err := os.Stat(p)
		return err == nil && info.IsDir()
	}}
	report, err := Status(context.Background(), StatusOptions{
		ManifestPath: filepath.Join(t.TempDir(), "install.json"), ClientEnv: env,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Clients) != len(clientid.Known()) {
		t.Fatalf("clients = %d, want one row per known client (%d)", len(report.Clients), len(clientid.Known()))
	}
	var sawAGY, sawUnresolved bool
	for _, client := range report.Clients {
		if client.Client == clientsetup.AGY {
			sawAGY = true
			if !client.ConfigFound || client.Root == "" {
				t.Fatalf("agy config root must be found: %+v", client)
			}
		} else if client.Reason != "" && !client.ConfigFound {
			sawUnresolved = true
		}
	}
	if !sawAGY {
		t.Fatal("agy must be one of the reported clients")
	}
	if !sawUnresolved {
		t.Fatal("a client with no C1 root entry yet must report a reason instead of a false presence")
	}
}

// Boundary: Status falls back to the manifest's own bin_dir for the lock check when
// StatusOptions.BinDir is not given.
func TestStatusFallsBackToManifestBinDir(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	manifestPath := filepath.Join(root, "config", "install.json")
	opts := Options{Checkout: fakeCheckout(t), BinDir: binDir, ManifestPath: manifestPath, Build: fakeBuild("v1"), GOOS: "linux"}
	if _, err := Install(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lockPath(binDir), 0o700); err != nil {
		t.Fatal(err)
	}
	report, err := Status(context.Background(), StatusOptions{ManifestPath: manifestPath})
	if err != nil {
		t.Fatal(err)
	}
	if !report.LockHeld {
		t.Fatal("status must resolve bin_dir from the manifest when BinDir is not given")
	}
}
