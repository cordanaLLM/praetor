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

// TestProjectionPathTreatsABackslashNameAsThePlatformDoes records what this check
// deliberately does NOT promise, because the first version of this test asserted
// the opposite and failed on Linux.
//
// A backslash is a separator on Windows and an ordinary filename character on
// POSIX, so `.cursor\rules\x.mdc` is one legal file name on Linux and a
// two-segment path on Windows. Refusing it everywhere would reject a legitimate
// POSIX name; accepting it everywhere would require inventing a separator the
// platform does not have. The invariant this function owes is the one above:
// every target the transpiler actually declares resolves identically on every
// host. What a non-declared spelling does is the platform's business.
func TestProjectionPathTreatsABackslashNameAsThePlatformDoes(t *testing.T) {
	got, err := projectionPath("root", `.gemini\GEMINI.md`)
	if filepath.Separator == '/' {
		if err != nil {
			t.Fatalf("a legal POSIX file name containing a backslash was refused: %v", err)
		}
		if !strings.HasSuffix(got, `.gemini\GEMINI.md`) {
			t.Fatalf("POSIX name was rewritten: %q", got)
		}
		return
	}
	if err != nil {
		t.Fatalf("a Windows-spelled declared target was refused: %v", err)
	}
}
