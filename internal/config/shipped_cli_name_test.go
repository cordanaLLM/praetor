// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// invocationPattern matches a bare `standardsctl <subcommand>` command a reader would type.
//
// `go run ./cmd/standardsctl audit` is a package path and legitimate, so a preceding path
// character excludes it, as do the directory listings that name the source tree.
var invocationPattern = regexp.MustCompile(`(^|[^/\w.-])standardsctl [a-z]`)

// docsTaughtToAdopters are the surfaces a new adopter reads before running anything. A release
// installs praetorctl; teaching standardsctl here hands them a command their harness never uses
// and, before the release also shipped that name, one they did not have at all (#118, #120).
var docsTaughtToAdopters = []string{
	"README.md",
	filepath.Join("docs", "guides", "onboarding.md"),
}

func TestShippedDocsTeachTheCommandTheHarnessInvokes(t *testing.T) {
	for _, rel := range docsTaughtToAdopters {
		data, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for number, line := range strings.Split(string(data), "\n") {
			if invocationPattern.MatchString(line) {
				t.Errorf("%s:%d teaches a standardsctl invocation; the harness invokes praetorctl:\n  %s",
					rel, number+1, strings.TrimSpace(line))
			}
		}
	}
}

// Negative: the pattern must not condemn the package path or the source directory, or the rule
// would force a rename of things that are correctly named.
func TestCommandInvocationPatternSparesPackagePaths(t *testing.T) {
	allowed := []string{
		"go run ./cmd/standardsctl audit",
		"go run ./cmd/standardsctl compile-context --verify",
		"│   ├── standardsctl/           # Governance CLI",
		"the ./cmd/standardsctl package",
	}
	for _, line := range allowed {
		if invocationPattern.MatchString(line) {
			t.Errorf("pattern must not match a package path or directory: %q", line)
		}
	}
	forbidden := []string{
		"standardsctl audit",
		"Run `standardsctl compile-context` to regenerate.",
		"| `standardsctl devcontainer` |",
	}
	for _, line := range forbidden {
		if !invocationPattern.MatchString(line) {
			t.Errorf("pattern must match a bare invocation: %q", line)
		}
	}
}

// A release must install the name the harness invokes. Before this, a release archive carried
// only standardsctl while every governed repository called praetorctl (#118).
func TestReleaseShipsTheInvokedBinary(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	config := string(data)
	if !strings.Contains(config, "binary: praetorctl") {
		t.Error("a release must ship a praetorctl binary: the harness invokes it by that name")
	}
	// Boundary: the old name stays, so an existing install or script keeps working.
	if !strings.Contains(config, "binary: standardsctl") {
		t.Error("the standardsctl alias must remain so a release does not break existing installs")
	}
}
