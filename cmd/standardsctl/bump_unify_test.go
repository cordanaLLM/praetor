// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/bump"
)

// runUnifyFixture writes package.json, hides go and pnpm so the static manifest path runs, and
// returns the unify output and the manifest afterwards.
func runUnifyFixture(t *testing.T, manifest string, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	writeFixtureFile(t, dir, "package.json", manifest)
	text, err := captureStdout(t, func() error {
		return runBumpUnify(t.Context(), append([]string{"--path", dir}, args...))
	})
	return text, readFixtureFile(t, dir, "package.json"), err
}

func TestBumpUnifyApplyRaisesOnlyDependenciesBehindTheCatalog(t *testing.T) {
	text, after, err := runUnifyFixture(t, `{"dependencies":{"typescript":"^5.0.0","eslint":"^99.0.0"}}`, "--apply")
	if err != nil {
		t.Fatalf("unify --apply: %v\n%s", err, text)
	}
	// The package.json rewrite splices only the raised value, so the compact fixture stays
	// compact and eslint, ahead of the catalog, keeps its bytes.
	want := `{"dependencies":{"typescript":"` + bump.FleetCatalog["typescript"].Version + `","eslint":"^99.0.0"}}`
	if after != want {
		t.Fatalf("unify --apply wrote\n%s\nwant typescript raised to the catalog pin and eslint unchanged:\n%s", after, want)
	}
	if !strings.Contains(text, "[AHEAD OF CATALOG, left unchanged] (1):") || !strings.Contains(text, "Successfully unified 1 dependencies") {
		t.Fatalf("unify output = %q", text)
	}
}

// TestBumpUnifyApplyNeverDowngrades is BUG-161's reproduction: a manifest ahead of every pin
// used to be rewritten back to the pins by --apply.
func TestBumpUnifyApplyNeverDowngrades(t *testing.T) {
	manifest := `{"dependencies":{"eslint":"^99.0.0"},"devDependencies":{"typescript":"~99.1.0"}}`
	text, after, err := runUnifyFixture(t, manifest, "--apply")
	if err != nil {
		t.Fatalf("unify --apply: %v\n%s", err, text)
	}
	if after != manifest {
		t.Fatalf("manifest ahead of the catalog was rewritten:\n%s", after)
	}
	if strings.Contains(text, "Successfully unified") || !strings.Contains(text, "No repository dependency is behind the fleet catalog.") {
		t.Fatalf("unify output = %q", text)
	}
}

func TestBumpUnifyReportsUnifiedAndUnrankedBoundaries(t *testing.T) {
	text, _, err := runUnifyFixture(t, `{"dependencies":{"prettier":"`+bump.FleetCatalog["prettier"].Version+`"}}`)
	if err != nil || !strings.Contains(text, "All repository dependencies are unified with the fleet catalog.") {
		t.Fatalf("equal pin output = %q, %v", text, err)
	}
	text, after, err := runUnifyFixture(t, `{"dependencies":{"vite":"workspace:*"}}`, "--apply")
	if err != nil || !strings.Contains(text, "[NOT SEMVER-COMPARABLE, left unchanged] (1):") || !strings.Contains(after, "workspace:*") {
		t.Fatalf("unranked output = %q, manifest %q, %v", text, after, err)
	}
}
