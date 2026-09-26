// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package nodemanifest

import "testing"

func TestNpmRuns(t *testing.T) {
	for value, want := range map[string]bool{
		// Positive: npm itself, and no declaration at all.
		"npm@11.15.0": true,
		"":            true,
		// Negative: another manager, however it is pinned.
		"pnpm@10.0.0":           false,
		"yarn@4.9.1+sha512.abc": false,
		"bun@1.3.0":             false,
		// Boundary: a bare name pins no npm version and a prefix match is not npm.
		"npm":           false,
		"npmx@1.0.0":    false,
		" npm@11.15.0 ": false,
	} {
		if got := NpmRuns(value); got != want {
			t.Errorf("NpmRuns(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestScriptRuns(t *testing.T) {
	for script, want := range map[string]bool{
		// Positive: any real command.
		"vitest run":     true,
		"node --test":    true,
		"true":           true,
		"  tsc --noEmit": true,
		// Negative: absent, blank, and the placeholder `npm init` writes.
		"":      false,
		" \t\n": false,
		`echo "Error: no test specified" && exit 1`: false,
		// Boundary: surrounding whitespace does not disguise the placeholder, and a script
		// that merely starts like it is a different command.
		"  echo \"Error: no test specified\" && exit 1\n":     false,
		`echo "Error: no test specified" && exit 1 || vitest`: true,
	} {
		if got := ScriptRuns(script); got != want {
			t.Errorf("ScriptRuns(%q) = %v, want %v", script, got, want)
		}
	}
}
