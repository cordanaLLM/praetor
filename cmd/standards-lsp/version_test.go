// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/buildid"
)

// declaredDefaultVersion snapshots the shipped default before any test assigns to version.
var declaredDefaultVersion = version

// Negative: the shipped default must be empty. A literal default is what made every build
// report v1.0.0, and as a const -X main.version could not write it at all (#666).
func TestShippedDefaultVersionIsEmpty(t *testing.T) {
	if declaredDefaultVersion != "" {
		t.Errorf("the default version must be empty so an injected one is observable, got %q", declaredDefaultVersion)
	}
}

// Positive: an injected release is what the daemon reports; without one it reports the
// identity buildid resolves for this build, the one the other binaries report too.
func TestServerVersionReportsTheSharedIdentity(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })
	version = "v1.4.2"
	if got := serverVersion(); got != "v1.4.2" {
		t.Errorf("an injected version must be reported verbatim, got %q", got)
	}
	version = ""
	if got, want := serverVersion(), buildid.Running("").String(); got != want || got == "v1.0.0" {
		t.Errorf("serverVersion() = %q, want the build identity %q", got, want)
	}
}

// Positive, negative and boundary: -version and --version ask for the version anywhere in
// the arguments; no argument, or any other, starts the daemon; a flag past the scan bound
// is not read.
func TestVersionRequested(t *testing.T) {
	beyond := make([]string, maxArgs, maxArgs+1)
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"-version"}, true},
		{[]string{"--stdio", "--version"}, true},
		{nil, false},
		{[]string{"--stdio"}, false},
		{[]string{"version"}, false},
		{append(beyond[:maxArgs-1:maxArgs-1], "-version"), true},
		{append(beyond, "-version"), false},
	} {
		if got := versionRequested(tc.args); got != tc.want {
			t.Errorf("versionRequested(%d args ending %q) = %v, want %v", len(tc.args), last(tc.args), got, tc.want)
		}
	}
}

func last(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[len(args)-1]
}
