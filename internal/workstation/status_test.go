// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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

// Positive + negative: per-client configuration-root presence via C1 (roots.go). Every known
// client resolves a root; only the one whose directory exists reports it found, and none
// reports a reason, since no resolution failed.
func TestStatusClientPresenceViaC1Roots(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	// GOOS must match the real host, not a simulated one: home is t.TempDir(), a real
	// filesystem path (C:\Users\...\AppData\Local\Temp\... on Windows), and
	// clientsetup.checkCleanAbs judges it against the platform GOOS names, not the actual
	// runtime. A hardcoded "linux" here made isAbsFor reject every Windows temp path as
	// not absolute, so the AGY root check that follows could never succeed on the Windows
	// leg of the portability matrix (#135).
	env := clientsetup.Env{GOOS: runtime.GOOS, Home: home, DirExists: func(p string) bool {
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
	var sawAGY bool
	for _, client := range report.Clients {
		if client.Root == "" || client.Reason != "" {
			t.Fatalf("every known client must resolve a root: %+v", client)
		}
		// .gemini/config makes both the AGY root and its parent, the Gemini CLI root, exist.
		shared := client.Client == clientsetup.AGY || client.Client == clientsetup.Gemini
		sawAGY = sawAGY || client.Client == clientsetup.AGY
		if client.ConfigFound != shared {
			t.Fatalf("%s: config found = %v, want %v: %+v", client.Client, client.ConfigFound, shared, client)
		}
	}
	if !sawAGY {
		t.Fatal("agy must be one of the reported clients")
	}
}

// Negative: a failed resolution (here an empty home) is reported per client with its
// reason instead of a false presence.
func TestStatusClientReasonWhenResolutionFails(t *testing.T) {
	report, err := Status(context.Background(), StatusOptions{
		ManifestPath: filepath.Join(t.TempDir(), "install.json"), ClientEnv: clientsetup.Env{GOOS: runtime.GOOS},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range report.Clients {
		if client.Reason == "" || client.ConfigFound || client.Root != "" {
			t.Fatalf("an unresolvable root must carry a reason: %+v", client)
		}
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
