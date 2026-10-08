// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// lintedCrate is a member manifest followed by lints, a [lints] section or "".
func lintedCrate(name, lints string) string {
	return crate(name, "2021") + lints
}

// CargoWarningsLints reads the level every crate of the root Cargo.toml's workspace gives the
// warnings lint group (the Cargo reference's [lints] section and workspaces' lints table;
// measured with cargo 1.98.1: a member without [lints] workspace = true does not inherit).
// Positive: a root package, a workspace whose members inherit, the inline and table spellings.
// Negative: a member that does not inherit, a warn level, an unreadable member. Boundary: no
// root manifest and a workspace with no crate.
func TestCargoWarningsLints(t *testing.T) {
	inherit := "\n[lints]\nworkspace = true\n"
	workspace := "[workspace]\nmembers = [\"crates/*\"]\n\n[workspace.lints.rust]\nwarnings = { level = \"forbid\", priority = -1 }\n"
	cases := map[string]struct {
		files         map[string]string
		level, detail string
	}{
		"positive: the root package": {map[string]string{"Cargo.toml": lintedCrate("w", "\n[lints.rust]\nwarnings = \"deny\"\n")},
			"deny", "every crate of the root Cargo.toml denies warnings"},
		"positive: inheriting members": {map[string]string{"Cargo.toml": workspace,
			"crates/a/Cargo.toml": lintedCrate("a", inherit), "crates/b/Cargo.toml": "lints = { workspace = true }\n" + crate("b", "2021")},
			"forbid", "forbids warnings"},
		"positive: a dotted key": {map[string]string{"Cargo.toml": lintedCrate("w", "\n[lints]\nrust.warnings = \"forbid\"\n")}, "forbid", "forbids"},
		"positive: the weakest level wins": {map[string]string{"Cargo.toml": workspace + "\n[package]\nname = \"w\"\n\n[lints.rust]\nwarnings = \"deny\"\n",
			"crates/a/Cargo.toml": lintedCrate("a", inherit)}, "deny", "denies"},
		"negative: a member not inheriting": {map[string]string{"Cargo.toml": workspace,
			"crates/a/Cargo.toml": lintedCrate("a", inherit), "crates/b/Cargo.toml": lintedCrate("b", "")},
			"", "crates/b/Cargo.toml does not deny warnings"},
		"negative: warn": {map[string]string{"Cargo.toml": lintedCrate("w", "\n[lints.rust]\nwarnings = \"warn\"\n")}, "", "the root package does not deny"},
		"negative: a missing member": {map[string]string{"Cargo.toml": "[workspace]\nmembers = [\"gone\"]\n"},
			"", "the workspace member gone/Cargo.toml cannot be read"},
		"boundary: no root manifest":  {map[string]string{"README.md": "x\n"}, "", ""},
		"boundary: no crate declared": {map[string]string{"Cargo.toml": "[workspace]\nmembers = []\n"}, "", "declares no crate"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lints := flavor.CargoWarningsLints(t.Context(), repoWithFiles(t, tc.files))
			if lints.Level != tc.level || !strings.Contains(lints.Detail, tc.detail) || (tc.detail == "") != (lints.Detail == "") {
				t.Fatalf("CargoWarningsLints = %+v; want level %q, detail naming %q", lints, tc.level, tc.detail)
			}
		})
	}
}
