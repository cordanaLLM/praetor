// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bump

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// refusingPnpm stands in for a pnpm that ran and refused the update, reporting only on
// standard error, as pnpm does for an unresolvable version.
const refusingPnpm = `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "ERR_PNPM_NO_MATCHING_VERSION No matching version found")
	os.Exit(1)
}
`

// pnpmFixture writes a pnpm project pinning typescript ^5.0.0 and returns it with the
// candidate that raises the pin.
func pnpmFixture(t *testing.T) (string, UpgradeCandidate) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"package.json":   "{\"dependencies\":{\"typescript\":\"^5.0.0\"}}\n",
		"pnpm-lock.yaml": "lockfileVersion: '9.0'\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, UpgradeCandidate{Package: "typescript", CurrentVersion: "^5.0.0", TargetVersion: "^5.7.3", ManifestType: "package.json"}
}

// A pnpm that ran and refused is reported with its reason, and package.json is not
// edited behind its back: a rewritten manifest next to an unchanged pnpm-lock.yaml would
// declare one version and resolve another.
func TestApplyUpdate_PnpmRefusalIsReportedWithoutManifestEdit(t *testing.T) {
	dir, candidate := pnpmFixture(t)
	bin := t.TempDir()
	testsupport.BuildExecutable(t, bin, "pnpm", refusingPnpm)
	t.Setenv("PATH", bin)
	err := ApplyUpdate(t.Context(), dir, candidate)
	if err == nil || !strings.Contains(err.Error(), "ERR_PNPM_NO_MATCHING_VERSION") {
		t.Fatalf("a refusing pnpm was not reported with its reason: %v", err)
	}
	assertPnpmManifest(t, dir, "^5.0.0")
}

// A pnpm that could not start reports why and leaves the manifest alone.
func TestApplyUpdate_PnpmMissingReportsError(t *testing.T) {
	dir, candidate := pnpmFixture(t)
	t.Setenv("PATH", t.TempDir())
	if err := ApplyUpdate(t.Context(), dir, candidate); err == nil {
		t.Fatal("a missing pnpm was reported as a successful update")
	}
	assertPnpmManifest(t, dir, "^5.0.0")
}

func assertPnpmManifest(t *testing.T, dir, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || !strings.Contains(string(data), want) {
		t.Fatalf("package.json = %q, want it to pin %s: %v", data, want, err)
	}
}
