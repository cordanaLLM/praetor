// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
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
		for _, alias := range targetsOf(name)[1:] {
			target, err := os.Readlink(filepath.Join(opts.BinDir, alias))
			if err != nil || target != name {
				t.Fatalf("alias %s -> %q, %v; want -> %s", alias, target, err, name)
			}
		}
	}
}

// Positive (#377): a fresh install places tribunusctl beside the three engine binaries,
// records all four in the manifest, places no alias for it, and status reports the install
// current with nothing missing.
func TestInstall_Positive_DistributesTribunusctl(t *testing.T) {
	opts := testOptions(t, fakeBuild("v1"))
	ctx := context.Background()
	result, err := Install(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"praetorctl", "praetor-mcp", "praetor-lsp", "tribunusctl"}
	if !slices.Equal(binaryNames, want) || len(result.Manifest.Binaries) != len(want) {
		t.Fatalf("binaryNames = %v, manifest binaries = %v; want %v", binaryNames, result.Manifest.Binaries, want)
	}
	if _, ok := result.Manifest.Binaries["tribunusctl"]; !ok {
		t.Fatalf("manifest must record tribunusctl: %v", result.Manifest.Binaries)
	}
	assertContent(t, filepath.Join(opts.BinDir, "tribunusctl"), "v1:tribunusctl")
	entries, err := os.ReadDir(opts.BinDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(allTargetNames()) || len(allTargetNames()) != 7 {
		t.Fatalf("bin directory holds %d entries, targets %v; want four binaries and three aliases", len(entries), allTargetNames())
	}
	status, err := Status(ctx, StatusOptions{Checkout: opts.Checkout, ManifestPath: opts.ManifestPath})
	if err != nil || !status.UpToDate || len(status.MissingBinaries) != 0 {
		t.Fatalf("a fresh install must be current with nothing missing: %+v, %v", status, err)
	}
}

// dropRecordedBinary rewrites the manifest at path without name and removes the installed
// file: the state an install made before name joined binaryNames leaves behind.
func dropRecordedBinary(t *testing.T, opts Options, name string) {
	t.Helper()
	manifest, err := config.ReadInstallManifest(context.Background(), opts.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	delete(manifest.Binaries, name)
	if err := config.WriteInstallManifest(opts.ManifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(opts.BinDir, name)); err != nil {
		t.Fatal(err)
	}
}

// Negative (#377): a manifest written before tribunusctl was distributed, at the checkout
// HEAD, is reported not current and names the missing binary; the next install places it
// and records it, and status is current again.
func TestStatus_Negative_ManifestWithoutTribunusctlIsNotCurrent(t *testing.T) {
	opts := testOptions(t, fakeBuild("v1"))
	ctx := context.Background()
	if _, err := Install(ctx, opts); err != nil {
		t.Fatal(err)
	}
	dropRecordedBinary(t, opts, "tribunusctl")
	status, err := Status(ctx, StatusOptions{Checkout: opts.Checkout, ManifestPath: opts.ManifestPath})
	if err != nil {
		t.Fatal(err)
	}
	if status.UpToDate || !slices.Equal(status.MissingBinaries, []string{"tribunusctl"}) ||
		status.CommitsBehind == nil || *status.CommitsBehind != 0 {
		t.Fatalf("an install lacking tribunusctl at HEAD must be not current and name it: %+v", status)
	}
	opts.Build = fakeBuild("v2")
	repaired, err := Install(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := repaired.Manifest.Binaries["tribunusctl"]; !ok {
		t.Fatalf("the next install must record tribunusctl: %v", repaired.Manifest.Binaries)
	}
	assertContent(t, filepath.Join(opts.BinDir, "tribunusctl"), "v2:tribunusctl")
	status, err = Status(ctx, StatusOptions{Checkout: opts.Checkout, ManifestPath: opts.ManifestPath})
	if err != nil || !status.UpToDate || len(status.MissingBinaries) != 0 {
		t.Fatalf("status after the repairing install: %+v, %v", status, err)
	}
}

// Boundary (#377): a failed install over a prior set without tribunusctl restores exactly
// that set, tribunusctl still absent and the manifest still lacking it; a failure over a full
// set restores the engine binaries. tribunusctl builds last, so no build failure places it;
// TestRestoreFromBackup_Boundary_TribunusctlAsRecorded restores a placed one.
func TestInstall_Boundary_RollbackRestoresRecordedSet(t *testing.T) {
	opts := testOptions(t, fakeBuild("v1"))
	ctx := context.Background()
	if _, err := Install(ctx, opts); err != nil {
		t.Fatal(err)
	}
	dropRecordedBinary(t, opts, "tribunusctl")
	opts.Build = failingBuild("tribunusctl", errors.New("fixture compiler failure"))
	if _, err := Install(ctx, opts); err == nil {
		t.Fatal("a tribunusctl build failure must fail Install")
	}
	for _, name := range []string{"praetorctl", "praetor-mcp", "praetor-lsp"} {
		assertContent(t, filepath.Join(opts.BinDir, name), "v1:"+name)
	}
	if _, err := os.Lstat(filepath.Join(opts.BinDir, "tribunusctl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a prior set without tribunusctl must stay without it, lstat: %v", err)
	}
	manifest, err := config.ReadInstallManifest(ctx, opts.ManifestPath)
	if err != nil || !slices.Equal(missingBinaries(manifest), []string{"tribunusctl"}) {
		t.Fatalf("a failed install must leave the prior manifest: %+v, %v", manifest, err)
	}

	opts.Build = fakeBuild("v1")
	if _, err := Install(ctx, opts); err != nil {
		t.Fatal(err)
	}
	opts.Build = failingBuild("praetor-lsp", errors.New("fixture compiler failure"))
	if _, err := Install(ctx, opts); err == nil {
		t.Fatal("a praetor-lsp build failure must fail Install")
	}
	for _, name := range binaryNames {
		assertContent(t, filepath.Join(opts.BinDir, name), "v1:"+name)
	}
}

// Boundary (#377): restoring every target after a full placement returns a prior set to what
// was recorded: a tribunusctl that was absent is removed again, one that was present is
// restored byte for byte.
func TestRestoreFromBackup_Boundary_TribunusctlAsRecorded(t *testing.T) {
	for name, prior := range map[string]bool{"absent before": false, "present before": true} {
		t.Run(name, func(t *testing.T) {
			opts := testOptions(t, fakeBuild("v1"))
			ctx := context.Background()
			if _, err := Install(ctx, opts); err != nil {
				t.Fatal(err)
			}
			if !prior {
				dropRecordedBinary(t, opts, "tribunusctl")
			}
			states, err := inspectTargets(opts.BinDir)
			if err != nil {
				t.Fatal(err)
			}
			backupDir, err := backupTargets(opts.BinDir, states)
			if err != nil {
				t.Fatal(err)
			}
			opts.Build = fakeBuild("v2")
			if _, err := placeAll(ctx, opts, opts.Checkout, states, backupDir); err != nil {
				t.Fatal(err)
			}
			assertContent(t, filepath.Join(opts.BinDir, "tribunusctl"), "v2:tribunusctl")
			if failures := restoreFromBackup(opts.GOOS, opts.BinDir, backupDir, states, allTargetNames()); len(failures) != 0 {
				t.Fatalf("restore failures: %v", failures)
			}
			assertContent(t, filepath.Join(opts.BinDir, "praetorctl"), "v1:praetorctl")
			_, err = os.Lstat(filepath.Join(opts.BinDir, "tribunusctl"))
			if prior {
				assertContent(t, filepath.Join(opts.BinDir, "tribunusctl"), "v1:tribunusctl")
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a tribunusctl absent before must be removed by the restore, lstat: %v", err)
			}
		})
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
	// The restriction this asserts is a POSIX mode bit. Windows synthesises Mode() from the
	// read-only attribute alone, so the chmod below leaves the binary at 0666 and no execute
	// bit is ever reported; installMode already returns early there for that reason, naming
	// #135. Asserting the refusal anyway tested the platform, not the code. util.ModeIsProtection
	// is the repository's existing predicate for this and prints the reason once per process.
	if !util.ModeIsProtection() {
		t.Skip("a non-executable permission cannot be expressed where the mode is not the protection")
	}
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

// Positive + negative + boundary: every installed binary has a build package that is a main
// package of this module and none else does, aliases belong only to installed binaries and
// tribunusctl has none, no name without an alias resolves as one, and goBuild refuses a
// name with no build package.
func TestBuildTables_CoverEveryBinary(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range binaryNames {
		pkg, ok := buildPackages[name]
		if !ok {
			t.Fatalf("%s has no build package", name)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(pkg), "main.go")); err != nil {
			t.Fatalf("%s builds from %s, which holds no main.go: %v", name, pkg, err)
		}
	}
	if len(buildPackages) != len(binaryNames) {
		t.Fatalf("build packages %v name more than binaryNames %v", buildPackages, binaryNames)
	}
	for name := range binaryAliases {
		if !slices.Contains(binaryNames, name) {
			t.Fatalf("alias table names %s, which is not installed", name)
		}
	}
	if _, ok := binaryAliases["tribunusctl"]; ok {
		t.Fatal("tribunusctl never had a legacy name and must place no alias")
	}
	if name, ok := primaryFor(""); ok {
		t.Fatalf("the empty name resolved as the alias of %s", name)
	}
	err := goBuild(context.Background(), t.TempDir(), "praetor-evil", filepath.Join(t.TempDir(), "out"))
	if err == nil {
		t.Fatal("goBuild accepted a binary with no build package")
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
