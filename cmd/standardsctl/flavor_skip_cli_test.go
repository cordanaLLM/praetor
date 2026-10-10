package main

import (
	"strings"
	"testing"
)

const nativeGPUManifest = "version: 1\nrepository:\n  owner: fixture\n  name: fixture\nprofiles:\n  - native-gpu-systems\n"

// Positive (#1111): a native-gpu-systems repository holding a patch series and scripts, with no
// meson, CMake or Rust marker, is a stated skip, exit 0: the pre-push flavor-audit job runs this
// command and used to fail "none match" while adoption reported Not applicable.
func TestFlavorAudit_Positive_NoMarkersIsAStatedSkip(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, ".standards.yaml", nativeGPUManifest)
	writeFixtureFile(t, dir, "patches/0001-fix.patch", "--- a\n+++ b\n")
	writeFixtureFile(t, dir, "tools/build.sh", "#!/bin/sh\n")
	out, err := captureStdout(t, func() error { return dispatchCommand("flavor", []string{"audit", dir}) })
	if err != nil {
		t.Fatalf("flavor audit: %v\n%s", err, out)
	}
	mustContain(t, out, "Skipped, not applicable", "profile \"native-gpu-systems\" has flavors", "flavors entry in .standards.yaml")
}

// Negative (#1111): a repository whose markers match a flavor but does not conform still fails.
func TestFlavorAudit_Negative_MatchingNonConformingStillFails(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, ".standards.yaml", nativeGPUManifest)
	writeFixtureFile(t, dir, "meson.build", "project('x', 'c')\n")
	out, err := captureStdout(t, func() error { return dispatchCommand("flavor", []string{"audit", dir}) })
	if err == nil || strings.Contains(out, "Skipped") {
		t.Fatalf("a matching non-conforming repository must fail, got err=%v\n%s", err, out)
	}
	mustContain(t, out, "(Flavor: native-gpu-systems)")
}

// Boundary (#1111): a pin wins over the skip. The same marker-free repository, pinned, is audited.
func TestFlavorAudit_Boundary_PinWinsOverSkip(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, ".standards.yaml", nativeGPUManifest+"flavors:\n  - name: native-gpu-systems\n")
	writeFixtureFile(t, dir, "tools/build.sh", "#!/bin/sh\n")
	out, err := captureStdout(t, func() error { return dispatchCommand("flavor", []string{"audit", dir}) })
	if err == nil || strings.Contains(out, "Skipped") {
		t.Fatalf("a pinned flavor must be audited, got err=%v\n%s", err, out)
	}
	mustContain(t, out, "(Flavor: native-gpu-systems)")
}

// Negative (#1111 review): a mistyped path and an empty directory have no profile, so the audit
// keeps failing instead of skipping.
func TestFlavorAudit_Negative_NoProfileStillFails(t *testing.T) {
	for name, dir := range map[string]string{"missing": "/nonexistent/path", "empty": t.TempDir()} {
		out, err := captureStdout(t, func() error { return dispatchCommand("flavor", []string{"audit", dir}) })
		if err == nil || strings.Contains(out, "Skipped") {
			t.Errorf("%s: a repository no profile classifies must fail, got err=%v\n%s", name, err, out)
		}
	}
}
