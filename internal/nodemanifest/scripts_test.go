// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package nodemanifest

import (
	"slices"
	"testing"
)

func TestDeclaredManager(t *testing.T) {
	type want struct {
		manager Manager
		version string
		ok      bool
	}
	for value, expect := range map[string]want{
		// Positive: each known manager, pinned the way Corepack pins it.
		"npm@11.15.0":           {ManagerNpm, "11.15.0", true},
		"pnpm@10.0.0":           {ManagerPnpm, "10.0.0", true},
		"yarn@4.9.1+sha512.abc": {ManagerYarn, "4.9.1+sha512.abc", true},
		"bun@1.3.0":             {ManagerBun, "1.3.0", true},
		// Boundary: no value names no manager and is still a valid declaration.
		"": {"", "", true},
		// Negative: an unknown manager, a bare name, whitespace and a prefix match.
		"deno@2.0.0":   {"", "", false},
		"pnpm":         {"", "", false},
		" npm@11.15.0": {"", "", false},
		"npmx@1.0.0":   {"", "", false},
	} {
		manager, version, ok := DeclaredManager(value)
		if manager != expect.manager || version != expect.version || ok != expect.ok {
			t.Errorf("DeclaredManager(%q) = %q, %q, %v; want %q, %q, %v", value, manager, version, ok, expect.manager, expect.version, expect.ok)
		}
	}
}

func TestManagerLockfiles(t *testing.T) {
	// Positive: every lockfile maps back to the manager that reads it.
	for _, name := range AllLockfiles() {
		manager, ok := LockfileManager(name)
		if !ok || !slices.Contains(manager.Lockfiles(), name) {
			t.Errorf("LockfileManager(%q) = %q, %v; its Lockfiles do not list it", name, manager, ok)
		}
	}
	if got := ManagerBun.Lockfiles(); !slices.Equal(got, []string{"bun.lock", "bun.lockb"}) {
		t.Errorf("ManagerBun.Lockfiles() = %v, want bun.lock before bun.lockb", got)
	}
	// Negative: npm 12 reads no shrinkwrap, and an unknown manager reads nothing.
	if manager, ok := LockfileManager("npm-shrinkwrap.json"); ok {
		t.Errorf("LockfileManager(npm-shrinkwrap.json) = %q, want no manager", manager)
	}
	if got := Manager("deno").Lockfiles(); got != nil {
		t.Errorf("an unknown manager lists lockfiles %v", got)
	}
	// Boundary: a caller mutating the returned slice does not change the table.
	ManagerNpm.Lockfiles()[0] = "mutated"
	if got := ManagerNpm.Lockfiles(); got[0] != "package-lock.json" {
		t.Errorf("Lockfiles returned the table itself: %v", got)
	}
}

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
