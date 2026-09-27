package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/needs"
)

// isolateDevRootEnv clears both dev-root variables and PRAETOR_FRAMEWORK_DIR and points
// the home directory at a fresh temporary folder, so no test resolves the operator's tree.
// It returns that home directory.
func isolateDevRootEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, name := range []string{devRootEnv, legacyDevRootEnv, frameworkDirEnv} {
		t.Setenv(name, "")
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// clearHomeDir leaves the process without a usable home directory.
func clearHomeDir(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
}

func TestResolveDevRootDir_3D(t *testing.T) {
	home := isolateDevRootEnv(t)

	// Positive: without flag or environment the root is <home>/dev.
	if got, err := resolveDevRootDir("", "--dev-dir"); err != nil || got != filepath.Join(home, "dev") {
		t.Fatalf("home default = %q, %v; want %q", got, err, filepath.Join(home, "dev"))
	}

	// Boundary: only the earlier PRAETOR_DEV_DIR name set still resolves.
	legacy := filepath.Join(home, "legacy")
	t.Setenv(legacyDevRootEnv, legacy)
	if got, err := resolveDevRootDir("", "--dev-dir"); err != nil || got != legacy {
		t.Fatalf("PRAETOR_DEV_DIR alone = %q, %v; want %q", got, err, legacy)
	}

	// Positive + boundary: PRAETOR_DEV_ROOT is honoured and wins when both are set.
	canonical := filepath.Join(home, "canonical")
	t.Setenv(devRootEnv, canonical)
	if got, err := resolveDevRootDir("", "--dev-dir"); err != nil || got != canonical {
		t.Fatalf("both set = %q, %v; want PRAETOR_DEV_ROOT %q", got, err, canonical)
	}

	// Positive: an explicit flag value wins over both variables.
	if got, err := resolveDevRootDir("/srv/dev", "--dev-dir"); err != nil || got != "/srv/dev" {
		t.Fatalf("explicit = %q, %v; want /srv/dev", got, err)
	}

	// Negative: no variable and no home directory is an error naming the flag and the
	// variable, never a working-directory-relative "dev".
	t.Setenv(devRootEnv, "")
	t.Setenv(legacyDevRootEnv, "")
	clearHomeDir(t)
	got, err := resolveDevRootDir("", "--dev-dir")
	if err == nil {
		t.Fatalf("unusable home resolved to %q", got)
	}
	for _, want := range []string{"--dev-dir", devRootEnv} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

// frameworkSelection returns a needs selection whose only configured target is go with the
// given checkout, as a workstation document would configure it.
func frameworkSelection(checkout string) *needsSelection {
	targets := needs.Targets{"go": {Module: "example.com/acme/kit", Contract: "/srv/kit.capabilities.yaml", Checkout: checkout}}
	return &needsSelection{targets: targets}
}

func TestNeedsFrameworkSource_3D(t *testing.T) {
	home := isolateDevRootEnv(t)
	t.Setenv(devRootEnv, filepath.Join(home, "fleet"))
	configured := filepath.Join(home, "kit")

	// Positive: framework.targets.go.checkout is the default, carrying the go target's
	// module and contract.
	source := frameworkSelection(configured).frameworkSource("", false)
	if source.Checkout != configured || source.Module != "example.com/acme/kit" || source.Contract != "/srv/kit.capabilities.yaml" {
		t.Fatalf("configured checkout = %+v", source)
	}

	// Positive: PRAETOR_FRAMEWORK_DIR wins over the configured checkout, --framework over both.
	t.Setenv(frameworkDirEnv, "/srv/framework")
	if got := frameworkSelection(configured).frameworkSource("", false); got.Checkout != "/srv/framework" {
		t.Fatalf("PRAETOR_FRAMEWORK_DIR = %+v", got)
	}
	if got := frameworkSelection(configured).frameworkSource("/srv/explicit", true); got.Checkout != "/srv/explicit" {
		t.Fatalf("explicit --framework = %+v", got)
	}

	// Boundary: an explicit --framework="" selects the declaration (the contract) even with
	// a checkout configured and PRAETOR_FRAMEWORK_DIR set.
	if got := frameworkSelection(configured).frameworkSource("", true); got.Checkout != "" || got.Contract == "" {
		t.Fatalf("explicit empty framework = %+v; want the declared contract", got)
	}

	// Negative: nothing selected never falls back to a checkout under the dev root, and
	// needs no home directory either.
	t.Setenv(frameworkDirEnv, "")
	clearHomeDir(t)
	if got := frameworkSelection("").frameworkSource("", false); got.Checkout != "" {
		t.Fatalf("unselected framework = %+v; want no checkout", got)
	}
}

// newDevRootFleet builds a dev root holding one Go repository.
func newDevRootFleet(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "acme", "widgets")
	writeFixtureFile(t, repo, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixtureFile(t, repo, "go.mod", "module example.com/widgets\n\ngo 1.27\n\nrequire github.com/spf13/cobra v1.8.0\n")
	return root
}

func TestNeedsAggregate_HonoursDevRootEnv(t *testing.T) {
	isolateDevRootEnv(t)
	root := newDevRootFleet(t)

	// Positive: PRAETOR_DEV_ROOT selects the fleet.
	t.Setenv(devRootEnv, root)
	out, err := captureStdout(t, func() error { return dispatchCommand("needs", []string{"aggregate"}) })
	if err != nil {
		t.Fatalf("needs aggregate with PRAETOR_DEV_ROOT: %v\n%s", err, out)
	}
	mustContain(t, out, "**Repositories Scanned**: 1 / 1")

	// Boundary: the dev root no longer selects a framework checkout. Without --framework,
	// PRAETOR_FRAMEWORK_DIR or framework.targets.go.checkout, report falls back to the
	// declaration, here none, instead of failing over a missing path under the dev root.
	t.Setenv(devRootEnv, t.TempDir())
	out, err = captureStdout(t, func() error {
		return dispatchCommand("needs", []string{"report", "--path=" + filepath.Join(root, "acme", "widgets")})
	})
	if err != nil {
		t.Fatalf("needs report without a selected framework: %v\n%s", err, out)
	}
	mustContain(t, out, "Coverage basis: not-configured; builds and tests not run")

	// Negative: no dev root and no home directory fails before scanning anything.
	t.Setenv(devRootEnv, "")
	clearHomeDir(t)
	if err := dispatchCommand("needs", []string{"aggregate"}); err == nil || !strings.Contains(err.Error(), "--dev-dir") {
		t.Fatalf("needs aggregate without a home = %v; want an error naming --dev-dir", err)
	}
}

func TestTopologyAudit_HonoursDevRootEnv(t *testing.T) {
	isolateDevRootEnv(t)

	// Positive: with no --dev-root and no positional root, PRAETOR_DEV_ROOT is audited.
	root := t.TempDir()
	t.Setenv(devRootEnv, root)
	out, err := captureStdout(t, func() error { return dispatchCommand("topology", []string{"audit"}) })
	if err != nil {
		t.Fatalf("topology audit with PRAETOR_DEV_ROOT: %v\n%s", err, out)
	}
	mustContain(t, out, "Workstation Topology Audit: "+root)

	// Boundary: the earlier PRAETOR_DEV_DIR name alone still selects the tree.
	t.Setenv(devRootEnv, "")
	t.Setenv(legacyDevRootEnv, root)
	if _, err := captureStdout(t, func() error { return dispatchCommand("topology", []string{"audit"}) }); err != nil {
		t.Fatalf("topology audit with PRAETOR_DEV_DIR: %v", err)
	}

	// Negative: nothing to resolve is an error naming --dev-root.
	t.Setenv(legacyDevRootEnv, "")
	clearHomeDir(t)
	if err := dispatchCommand("topology", []string{"audit"}); err == nil || !strings.Contains(err.Error(), "--dev-root") {
		t.Fatalf("topology audit without a home = %v; want an error naming --dev-root", err)
	}
}

func TestAdoptAndHarvest_HonourDevRootEnv(t *testing.T) {
	isolateDevRootEnv(t)
	root := newDevRootFleet(t)
	t.Setenv(devRootEnv, root)

	// Positive: harvest workstation and adopt --all-missing scan PRAETOR_DEV_ROOT when
	// no --dir / --dev-dir is given. The fixture's .git is not a real checkout, so the
	// harvest inventory is reported incomplete; the scan root is what is under test.
	out, err := captureStdout(t, func() error { return dispatchCommand("harvest", []string{"workstation"}) })
	if err != nil && !strings.Contains(err.Error(), "inventory is incomplete") {
		t.Fatalf("harvest workstation with PRAETOR_DEV_ROOT: %v\n%s", err, out)
	}
	mustContain(t, out, "Active Dev Repositories: 1", "acme/widgets")
	// The dry-run adoption of the bare fixture itself fails (no pinned lock source);
	// the discovered count proves the scan ran against PRAETOR_DEV_ROOT.
	out, err = captureStdout(t, func() error {
		return dispatchCommand("adopt", []string{"--all-missing", "--dry-run"})
	})
	if err != nil && !strings.Contains(err.Error(), "adoption completed with errors") {
		t.Fatalf("adopt --all-missing with PRAETOR_DEV_ROOT: %v\n%s", err, out)
	}
	mustContain(t, out, "Batch Repository Adoption (1 unmanaged repos found)")

	// Negative: without a dev root both name their override flag.
	t.Setenv(devRootEnv, "")
	clearHomeDir(t)
	if err := dispatchCommand("harvest", []string{"workstation"}); err == nil || !strings.Contains(err.Error(), "--dir") {
		t.Fatalf("harvest workstation without a home = %v; want an error naming --dir", err)
	}
	if err := dispatchCommand("adopt", []string{"--all-missing", "--dry-run"}); err == nil || !strings.Contains(err.Error(), "--dev-dir") {
		t.Fatalf("adopt --all-missing without a home = %v; want an error naming --dev-dir", err)
	}
}
