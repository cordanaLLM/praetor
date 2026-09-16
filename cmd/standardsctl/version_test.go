// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"regexp"
	"strings"
	"testing"
)

// lockPattern mirrors internal/config lockVersionPattern. A pinned_version this package writes
// must satisfy the validator that reads it back, or init produces a lock audit rejects.
var lockPattern = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

// declaredDefaultVersion snapshots the shipped default at package initialisation, before any
// test assigns to version. Without this the default is unobservable: every test sets the
// variable, so reverting it to a plausible literal would change nothing any test can see.
var declaredDefaultVersion = version

// Negative: the shipped default must be empty. A non-empty default is exactly the defect --
// it makes every build claim a version it was never given, and -X cannot be observed at all.
func TestShippedDefaultVersionIsEmpty(t *testing.T) {
	if declaredDefaultVersion != "" {
		t.Errorf("the default version must be empty so an injected one is observable, got %q",
			declaredDefaultVersion)
	}
}

// Boundary: an unreleased build's metadata must be valid SemVer. A second plus sign is not.
func TestDevLockVersionUsesValidBuildMetadata(t *testing.T) {
	for _, tc := range []struct {
		modified bool
		want     string
	}{
		{false, "v0.0.0+abc123def456"},
		{true, "v0.0.0+abc123def456.dirty"},
	} {
		got := formatDevLockVersion("abc123def456", tc.modified)
		if got != tc.want {
			t.Errorf("modified=%v: got %q, want %q", tc.modified, got, tc.want)
		}
		if !lockPattern.MatchString(got) {
			t.Errorf("%q does not satisfy the lock validator", got)
		}
	}
}

// Positive: an injected release version is reported verbatim. Before this, -X wrote to a const
// and was silently discarded, so every build ever shipped reported the same literal (#119).
func TestBuildVersionUsesTheInjectedRelease(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = "v1.4.2"
	if got := buildVersion(); got != "v1.4.2" {
		t.Errorf("an injected version must be reported verbatim, got %q", got)
	}
	pinned, identified := lockVersion()
	if !identified || pinned != "v1.4.2" {
		t.Errorf("a release must pin its own version, got %q identified=%v", pinned, identified)
	}
}

// Negative: whitespace is not a version. A blank injection must fall through to what the build
// can prove rather than pinning an empty string.
func TestBuildVersionIgnoresABlankInjection(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = "   "
	if got := buildVersion(); strings.TrimSpace(got) == "" || got == "   " {
		t.Errorf("a blank injection must not be reported as a version, got %q", got)
	}
}

// Negative: an unidentifiable build must never name a version it cannot stand behind. This is
// the defect #119 records -- a lock naming v1.0.0 cannot say which praetor governed a repo.
func TestUnidentifiedBuildRefusesToInventAVersion(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = ""
	pinned, identified := lockVersion()
	if !identified && pinned != unidentifiedLockVersion {
		t.Errorf("an unidentified build must pin the zero version, got %q", pinned)
	}
	if identified && pinned == "v1.0.0" {
		t.Errorf("v1.0.0 is the literal that identified nothing; it must not be produced, got %q", pinned)
	}
}

// Boundary: whatever this package writes must satisfy the validator that reads it back.
func TestLockVersionAlwaysSatisfiesTheLockValidator(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	for _, injected := range []string{"v1.4.2", "1.4.2", "v2.0.0-rc.1", "", "  "} {
		version = injected
		pinned, _ := lockVersion()
		if !lockPattern.MatchString(pinned) {
			t.Errorf("pinned_version %q (injected %q) does not satisfy the lock validator", pinned, injected)
		}
	}
}

// Boundary: the version command names the binary the harness actually invokes. A release ships
// standardsctl, every governed repository calls praetorctl, and printing the other name sends a
// reader to a command their harness never uses (#118, #120).
func TestVersionOutputNamesPraetorctl(t *testing.T) {
	if strings.Contains(buildVersion(), "standardsctl") {
		t.Error("buildVersion must not embed a binary name")
	}
}
