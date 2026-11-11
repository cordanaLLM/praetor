// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"testing"
)

func pushGit(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	if out, err := runFixtureGit(t, dir, env, args...); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}

// TestForgeCheckMessage_PlantedMigration checks migration footer enforcement against intentionally breaking commit messages.
func TestForgeCheckMessage_PlantedMigration(t *testing.T) {
	for _, tc := range []struct {
		message string
		valid   bool
	}{
		{"feat!: drop x\n", false},
		{"feat!: drop x\n\nMigration: use the replacement API\n", true},
		{"drop x\n", false},
	} {
		path := writeFixtureFile(t, t.TempDir(), "COMMIT_EDITMSG", tc.message)
		err := runForge([]string{"check-message", path})
		if (err == nil) != tc.valid {
			t.Errorf("message %q: valid=%v, error=%v", tc.message, tc.valid, err)
		}
	}
}
