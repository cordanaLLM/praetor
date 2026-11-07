// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"os"
	"testing"
)

// TestUnsetCIRunnerEnv_Positive clears every listed runner variable that is set.
func TestUnsetCIRunnerEnv_Positive(t *testing.T) {
	for _, name := range CIRunnerEnv {
		t.Setenv(name, "set-by-ci")
	}
	if err := UnsetCIRunnerEnv(); err != nil {
		t.Fatal(err)
	}
	for _, name := range CIRunnerEnv {
		if v, ok := os.LookupEnv(name); ok {
			t.Fatalf("%s still set to %q", name, v)
		}
	}
}

// TestUnsetCIRunnerEnv_Boundary leaves unlisted variables and an already clean environment alone.
func TestUnsetCIRunnerEnv_Boundary(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	for _, name := range CIRunnerEnv {
		t.Setenv(name, "")
	}
	if err := UnsetCIRunnerEnv(); err != nil {
		t.Fatal(err)
	}
	if err := UnsetCIRunnerEnv(); err != nil {
		t.Fatalf("second call on a clean environment: %v", err)
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Fatal("an unlisted variable was cleared")
	}
}

// TestCIRunnerEnv_Negative refuses an empty or duplicated list, which would let a variable through.
func TestCIRunnerEnv_Negative(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range CIRunnerEnv {
		if name == "" || seen[name] {
			t.Fatalf("CIRunnerEnv holds an empty or duplicate name: %q", name)
		}
		seen[name] = true
	}
	if !seen["GITHUB_WORKFLOW_REF"] {
		t.Fatal("GITHUB_WORKFLOW_REF must be isolated: it made a fixture's failing run read as healthy")
	}
}
