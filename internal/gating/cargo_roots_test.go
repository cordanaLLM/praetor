// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// TrackedCargoRoots finds every Cargo workspace or standalone crate git tracks, not only one at the
// repository root (#1102): a crate in worker/ got no rustfmt, clippy, cargo fetch, cargo audit or
// cargo test, and every one of them reported a skip that read as a pass.
func TestTrackedCargoRoots_3D(t *testing.T) {
	const workspace = "[workspace]\nmembers = [\"crates/*\"]\n"
	const crate = "[package]\nname = \"fx\"\nversion = \"0.1.0\"\n"
	cases := []struct {
		name  string
		files map[string]string
		want  []CargoRoot
	}{
		// Positive: crates below the root are found, each with whether it commits a Cargo.lock.
		{"subdirectory crates", map[string]string{
			"worker/Cargo.toml": crate, "worker/Cargo.lock": "version = 4\n",
			"tools/gen/Cargo.toml": crate,
		}, []CargoRoot{{Dir: "tools/gen"}, {Dir: "worker", Locked: true}}},
		{"root workspace", map[string]string{"Cargo.toml": workspace, "Cargo.lock": "version = 4\n"},
			[]CargoRoot{{Dir: ".", Locked: true}}},
		// Negative: no tracked manifest, or one only where cargo never builds from, is no root.
		{"no manifest", map[string]string{"README.md": "x\n"}, nil},
		{"skipped directories", map[string]string{
			"target/debug/Cargo.toml": crate, "vendor/dep/Cargo.toml": crate,
			"testdata/fx/Cargo.toml": crate, ".cargo/x/Cargo.toml": crate,
		}, nil},
		// Boundary: a workspace whose members are crates counts once, two levels deep included;
		// a crate below a plain crate is its own root, and so is an excluded crate with its own
		// Cargo.lock below a workspace.
		{"workspace members", map[string]string{
			"rust/Cargo.toml": workspace + "\n[workspace.package]\nedition = \"2024\"\n", "rust/Cargo.lock": "version = 4\n",
			"rust/crates/a/Cargo.toml": crate, "rust/crates/b/nested/Cargo.toml": crate,
			"rust/excluded/Cargo.toml": crate, "rust/excluded/Cargo.lock": "version = 4\n",
		}, []CargoRoot{{Dir: "rust", Locked: true}, {Dir: "rust/excluded", Locked: true}}},
		{"crate below a crate", map[string]string{"Cargo.toml": crate, "examples/demo/Cargo.toml": crate},
			[]CargoRoot{{Dir: "."}, {Dir: "examples/demo"}}},
		{"workspace table in a comment", map[string]string{"Cargo.toml": "# [workspace]\n" + crate, "sub/Cargo.toml": crate},
			[]CargoRoot{{Dir: "."}, {Dir: "sub"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newHermeticGitRepo(t)
			for name, content := range tc.files {
				commitFile(t, repo, name, content)
			}
			got, err := TrackedCargoRoots(t.Context(), repo)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("TrackedCargoRoots = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

// Boundary: an untracked manifest is not a root, a directory outside any repository has none, and
// more roots than the stages run is a named refusal.
func TestTrackedCargoRoots_Boundary_UntrackedOutsideAndBound(t *testing.T) {
	repo := newHermeticGitRepo(t)
	writeFile(t, repo+"/scratch/Cargo.toml", "[package]\nname = \"s\"\n")
	if got, err := TrackedCargoRoots(t.Context(), repo); err != nil || len(got) != 0 {
		t.Errorf("untracked manifest: %+v, %v; want none", got, err)
	}
	if got, err := TrackedCargoRoots(t.Context(), t.TempDir()); err != nil || len(got) != 0 {
		t.Errorf("outside a repository: %+v, %v; want none", got, err)
	}
	for index := 0; index <= MaxCargoRoots; index++ {
		writeFile(t, fmt.Sprintf("%s/c%03d/Cargo.toml", repo, index), "[package]\nname = \"c\"\n")
	}
	treeGit(t, repo, "add", "-A")
	if _, err := TrackedCargoRoots(t.Context(), repo); !errors.Is(err, ErrTooManyCargoRoots) {
		t.Errorf("%d roots: err = %v, want %v", MaxCargoRoots+1, err, ErrTooManyCargoRoots)
	}
}
