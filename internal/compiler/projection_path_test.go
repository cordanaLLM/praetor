// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestProjectionPathAcceptsDeclaredSlashTargets pins the property that broke
// compile-context on Windows: a vendor target is declared as a slash path, and
// its cleanliness must be judged with slash semantics on every host.
//
// filepath.Clean returns the host separator, so `filepath.Clean(rel) != rel` was
// always true on Windows and every projection was refused with "compiled output
// requires a clean relative file path". compile-context --verify still worked,
// because the read path does not call this, so the tooling correctly reported
// CLAUDE.md as out of sync and could not reconcile it.
//
// These are the real targets the transpiler declares, not invented ones.
func TestProjectionPathAcceptsDeclaredSlashTargets(t *testing.T) {
	root := filepath.Join("repo", "root")
	for _, relative := range []string{
		"CLAUDE.md",
		".cursor/rules/hiss-invariants.mdc",
		".github/copilot-instructions.md",
		".windsurfrules",
		".gemini/GEMINI.md",
		".codex/rules.md",
	} {
		t.Run(relative, func(t *testing.T) {
			got, err := projectionPath(root, relative)
			if err != nil {
				t.Fatalf("declared vendor target rejected: %v", err)
			}
			if want := filepath.Join(root, relative); got != want {
				t.Fatalf("projection path = %q, want %q", got, want)
			}
		})
	}
}

// TestProjectionPathRefusesEscapingOrUncleanTargets is the negative half: making
// the check slash-aware must not make it permissive. Containment is still a host
// question and filepath.IsLocal still answers it.
func TestProjectionPathRefusesEscapingOrUncleanTargets(t *testing.T) {
	for _, relative := range []string{
		"",
		".",
		"..",
		"../escape.md",
		"nested/../../escape.md",
		"/absolute.md",
		"./unclean.md",
		"double//slash.md",
		"trailing/",
	} {
		t.Run(relative, func(t *testing.T) {
			if _, err := projectionPath("root", relative); err == nil {
				t.Fatalf("unsafe or unclean target accepted: %q", relative)
			}
		})
	}
}

// TestProjectionPathRefusesBackslashSpelledTargets is the boundary: a target
// spelled with the host separator is not a declared target. Accepting it would
// reintroduce the divergence from the other side, by letting a Windows-shaped
// value through that a Linux run would refuse.
func TestProjectionPathRefusesBackslashSpelledTargets(t *testing.T) {
	for _, relative := range []string{
		`.cursor\rules\hiss-invariants.mdc`,
		`.gemini\GEMINI.md`,
	} {
		t.Run(relative, func(t *testing.T) {
			_, err := projectionPath("root", relative)
			if err == nil && filepath.Separator == '/' {
				t.Fatalf("backslash-spelled target accepted on a POSIX host: %q", relative)
			}
			if err != nil && !strings.Contains(err.Error(), "clean relative file path") {
				t.Fatalf("unexpected refusal reason: %v", err)
			}
		})
	}
}
