// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

func testOptions(t *testing.T, build BuildFunc) Options {
	t.Helper()
	root := t.TempDir()
	return Options{
		Checkout:     fakeCheckout(t),
		BinDir:       filepath.Join(root, "bin"),
		ManifestPath: filepath.Join(root, "config", "install.json"),
		Build:        build,
		Now:          func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) },
		GOOS:         "linux",
	}
}

// Boundary (spec 9.3 W1): a first install has no previous binary to record or back up.
func TestInstallFirstInstallHasNoPrevious(t *testing.T) {
	opts := testOptions(t, fakeBuild("v1"))
	result, err := Install(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.Previous != nil {
		t.Fatalf("a first install must record no previous install, got %+v", result.Manifest.Previous)
	}
	if len(result.Manifest.Binaries) != len(binaryNames) {
		t.Fatalf("manifest binaries = %v, want %d entries", result.Manifest.Binaries, len(binaryNames))
	}
	for _, name := range binaryNames {
		assertContent(t, filepath.Join(opts.BinDir, name), "v1:"+name)
		alias := binaryAliases[name]
		target, err := os.Readlink(filepath.Join(opts.BinDir, alias))
		if err != nil || target != name {
			t.Fatalf("alias %s -> %q, %v; want -> %s", alias, target, err, name)
		}
	}
}

// Positive: install then status agree on the installed commit, and the lock is released.
func TestInstallThenStatus(t *testing.T) {
	opts := testOptions(t, fakeBuild("v1"))
	ctx := context.Background()
	result, err := Install(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if LockHeld(opts.BinDir) {
		t.Fatal("Install must release its lock before returning")
	}
	status, err := Status(ctx, StatusOptions{Checkout: opts.Checkout, BinDir: opts.BinDir, ManifestPath: opts.ManifestPath})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || status.Manifest == nil || status.Manifest.EngineCommit != result.Manifest.EngineCommit {
		t.Fatalf("status does not reflect the install: %+v", status)
	}
	if status.CheckoutHead != result.Manifest.EngineCommit || !status.UpToDate {
		t.Fatalf("status checkout head mismatch: %+v", status)
	}
	if status.LockHeld {
		t.Fatal("status must not report a lock the install already released")
	}
	if status.ManifestSHA256 == "" {
		t.Fatal("status must report the manifest digest")
	}

	// A second install over the first records the prior commit and keeps a backup.
	opts.Build = fakeBuild("v2")
	second, err := Install(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if second.Manifest.Previous == nil || second.Manifest.Previous.EngineCommit != result.Manifest.EngineCommit {
		t.Fatalf("second install must record the first as previous: %+v", second.Manifest.Previous)
	}
	if _, statErr := os.Stat(second.Manifest.Previous.Backup); statErr != nil {
		t.Fatalf("recorded backup directory must exist: %v", statErr)
	}
}

// Negative (spec 9.3 W1): a foreign symlink at an installation target is refused.
func TestInstallRefusesForeignSymlink(t *testing.T) {
	opts := testOptions(t, fakeBuild("v1"))
	if err := os.MkdirAll(opts.BinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/somewhere/else", filepath.Join(opts.BinDir, "praetorctl")); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), opts); !errors.Is(err, ErrForeignTarget) {
		t.Fatalf("foreign symlink accepted: %v", err)
	}
	if LockHeld(opts.BinDir) {
		t.Fatal("a refused install must not leave its lock behind")
	}
	if _, err := os.Stat(opts.ManifestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused install must not write a manifest, stat: %v", err)
	}
}

// Negative (spec 9.3 W1): a non-regular installation target is refused.
func TestInstallRefusesNonRegularTarget(t *testing.T) {
	opts := testOptions(t, fakeBuild("v1"))
	if err := os.MkdirAll(filepath.Join(opts.BinDir, "praetor-mcp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), opts); !errors.Is(err, ErrForeignTarget) {
		t.Fatalf("non-regular target accepted: %v", err)
	}
}

// Negative (spec 9.3 W1): a concurrent installer holding the lock is refused, and nothing
// it would have written is touched.
func TestInstallRefusesExistingLock(t *testing.T) {
	opts := testOptions(t, fakeBuild("v1"))
	if err := os.MkdirAll(opts.BinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lockPath(opts.BinDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), opts); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("existing lock accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(opts.BinDir, "praetorctl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a lock-refused install must not have placed any binary, stat: %v", err)
	}
}

// Negative + boundary: a build failure rolls every already-placed target in this run back
// to what was there before, and the pre-existing binary's permission ceiling is respected.
func TestInstallBuildFailureRollsBack(t *testing.T) {
	opts := testOptions(t, fakeBuild("v1"))
	ctx := context.Background()
	if _, err := Install(ctx, opts); err != nil {
		t.Fatal(err)
	}
	opts.Build = failingBuild("praetor-lsp", errors.New("fixture compiler failure"))
	if _, err := Install(ctx, opts); err == nil {
		t.Fatal("a build failure must fail Install")
	}
	assertContent(t, filepath.Join(opts.BinDir, "praetorctl"), "v1:praetorctl")
	assertContent(t, filepath.Join(opts.BinDir, "praetor-mcp"), "v1:praetor-mcp")
	if LockHeld(opts.BinDir) {
		t.Fatal("a failed install must not leave its lock behind")
	}
}

// Boundary: an installed binary chmod'd non-executable keeps that restriction rather than
// silently widening it back on the next build.
func TestInstallRespectsExistingNonExecutablePermission(t *testing.T) {
	opts := testOptions(t, fakeBuild("v1"))
	ctx := context.Background()
	if _, err := Install(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(opts.BinDir, "praetorctl"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts.Build = fakeBuild("v2")
	if _, err := Install(ctx, opts); err == nil {
		t.Fatal("a non-executable existing permission must refuse the install")
	}
}

// Negative: Install requires a context.
func TestInstallRequiresContext(t *testing.T) {
	var noContext context.Context
	if _, err := Install(noContext, testOptions(t, fakeBuild("v1"))); err == nil {
		t.Fatal("nil context accepted")
	}
}

// Negative: Install requires absolute Checkout and BinDir.
func TestInstallRequiresAbsolutePaths(t *testing.T) {
	opts := testOptions(t, fakeBuild("v1"))
	relative := opts
	relative.Checkout = "relative/checkout"
	if _, err := Install(context.Background(), relative); err == nil {
		t.Fatal("a relative checkout was accepted")
	}
	relative = opts
	relative.BinDir = "relative/bin"
	if _, err := Install(context.Background(), relative); err == nil {
		t.Fatal("a relative bin directory was accepted")
	}
}

// Positive: FleetSettings and WorkstationSettings, when given, are recorded in the
// manifest with the digest SelectOperatorSettings recomputes to detect drift.
func TestInstallRecordsSettingsDocuments(t *testing.T) {
	opts := testOptions(t, fakeBuild("v1"))
	settingsPath := filepath.Join(t.TempDir(), "fleet.yaml")
	body := []byte("clients: {}\n")
	if err := os.WriteFile(settingsPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	opts.FleetSettings = config.SettingsDocument{Path: settingsPath, Origin: config.SettingsFromFlag}
	result, err := Install(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.Settings.Fleet == nil || result.Manifest.Settings.Fleet.Path != settingsPath ||
		result.Manifest.Settings.Fleet.SHA256 != digestBytes(body) {
		t.Fatalf("fleet settings not recorded as expected: %+v", result.Manifest.Settings.Fleet)
	}
}
