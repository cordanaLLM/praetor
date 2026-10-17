// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// libraryClient declares the library-client profile, the way a Cargo repository chooses the
// rust-library flavor.
const libraryClient = "profiles: [library-client]\n"

// Positive (#67): a declared library-client crate with only src/lib.rs resolves to rust-library.
func TestDetectRustLibraryPositive(t *testing.T) {
	repo := t.TempDir()
	writeRustFile(t, repo, ".standards.yaml", libraryClient)
	writeRustFile(t, repo, "Cargo.toml", "[package]\nname = \"library\"\n")
	writeRustFile(t, repo, "src/lib.rs", "pub fn value() -> u8 { 1 }\n")
	if got, _ := resolve(repo); got != "rust-library" {
		t.Fatalf("declared library crate resolved as %q, want rust-library", got)
	}
}

// Negative: a crate that publishes a binary, inferred from src/main.rs or declared as [[bin]], is
// no library, so a declared library-client profile matches no flavor and says so.
func TestDetectRustLibraryNegativePublishedBinary(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"src/main.rs": {"Cargo.toml": "[package]\nname = \"binary\"\n", "src/main.rs": "fn main() {}\n"},
		"[[bin]]": {
			"Cargo.toml": "[package]\nname = \"mixed\"\n\n[lib]\n\n[[bin]]\nname = \"tool\"\n",
			"src/lib.rs": "pub fn value() {}\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			writeRustFile(t, repo, ".standards.yaml", libraryClient)
			for rel, text := range files {
				writeRustFile(t, repo, rel, text)
			}
			if _, err := flavor.Resolve(repo); !errors.Is(err, flavor.ErrNoFlavorMatched) {
				t.Fatalf("a published binary under library-client resolved with %v, want ErrNoFlavorMatched", err)
			}
		})
	}
}

// Boundary: a library workspace keeps rust-library with a private helper binary, whether the
// helper sets publish = false itself or inherits it from [workspace.package]; a published binary
// member turns it away.
func TestDetectRustLibraryBoundaryWorkspaceHelpers(t *testing.T) {
	cases := []struct {
		name, root, tool string
		want             bool
	}{
		{"own publish = false", "", "publish = false\n", true},
		{"inherited publish = false", "\n[workspace.package]\npublish = false\n", "publish.workspace = true\n", true},
		{"empty registry list", "", "publish = []\n", true},
		{"published binary member", "", "", false},
		{"inherited publish = true", "\n[workspace.package]\npublish = true\n", "publish.workspace = true\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			writeRustFile(t, repo, ".standards.yaml", libraryClient)
			writeRustFile(t, repo, "Cargo.toml", "[workspace]\nmembers = [\"crates/lib\", \"tools/check\"]\n"+tc.root)
			writeRustFile(t, repo, "crates/lib/Cargo.toml", "[package]\nname = \"lib\"\n")
			writeRustFile(t, repo, "crates/lib/src/lib.rs", "pub fn value() {}\n")
			writeRustFile(t, repo, "tools/check/Cargo.toml", "[package]\nname = \"check\"\n"+tc.tool)
			writeRustFile(t, repo, "tools/check/src/main.rs", "fn main() {}\n")
			got, _ := resolve(repo)
			if (got == "rust-library") != tc.want {
				t.Fatalf("resolved as %q, want rust-library = %v", got, tc.want)
			}
		})
	}
}

// Boundary: without a declaration a library crate classifies as native-gpu-systems and keeps
// rust-systems, as before the library flavor existed; so does a declared native-gpu-systems one.
func TestUndeclaredRustLibraryKeepsRustSystems(t *testing.T) {
	for name, manifest := range map[string]string{"undeclared": "", "native-gpu-systems": "profiles: [native-gpu-systems]\n"} {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			if manifest != "" {
				writeRustFile(t, repo, ".standards.yaml", manifest)
			}
			writeRustFile(t, repo, "Cargo.toml", "[package]\nname = \"library\"\n")
			writeRustFile(t, repo, "src/lib.rs", "pub fn value() {}\n")
			if got, _ := resolve(repo); got != "rust-systems" {
				t.Fatalf("library crate resolved as %q, want rust-systems", got)
			}
		})
	}
}

// Positive: the scaffold of a Rust library has no Dockerfile and no Go linter configuration, and
// its CI runs the pinned API compatibility check.
func TestApplyRustLibraryPositiveNoGoOrContainerFiles(t *testing.T) {
	repo := t.TempDir()
	writeRustFile(t, repo, "Cargo.toml", "[package]\nname = \"library\"\n")
	writeRustFile(t, repo, "src/lib.rs", "pub fn value() {}\n")
	if _, err := flavor.ApplyFlavor(t.Context(), repo, "rust-library", false); err != nil {
		t.Fatalf("apply Rust library flavor: %v", err)
	}
	for _, path := range []string{"Dockerfile", ".golangci.yml"} {
		if _, err := os.Stat(filepath.Join(repo, path)); !os.IsNotExist(err) {
			t.Errorf("Rust library scaffold has %s (stat error: %v)", path, err)
		}
	}
	workflow, err := os.ReadFile(filepath.Join(repo, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workflow), "cargo-semver-checks 0.51.0") ||
		!strings.Contains(string(workflow), "cargo semver-checks \\") ||
		!strings.Contains(string(workflow), "--baseline-rev \"$BASELINE_REV\"") {
		t.Fatalf("Rust library CI lacks pinned API compatibility check:\n%s", workflow)
	}
}

// Negative: a Go repository declaring library-client resolves to go-library, which keeps its
// templates.
func TestGoLibraryClientProfileKeepsGoLibraryFlavor(t *testing.T) {
	repo := t.TempDir()
	writeRustFile(t, repo, "go.mod", "module example/library\n")
	writeRustFile(t, repo, "internal/library.go", "package library\n")
	writeRustFile(t, repo, ".standards.yaml", libraryClient)
	if got, _ := resolve(repo); got != "go-library" {
		t.Fatalf("Go library-client profile resolved as %q, want go-library", got)
	}
	goFlavor, err := flavor.Get("go-library")
	if err != nil {
		t.Fatal(err)
	}
	if got := goFlavor.RequiredTemplates(); len(got) != 4 || got[3].Source != "go/ci-go.yml.tmpl" {
		t.Fatalf("Go library templates changed: %+v", got)
	}
}

func writeRustFile(t *testing.T, root, rel, text string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
