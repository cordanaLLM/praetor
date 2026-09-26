// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package nodemanifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeFile creates dir/name below root with the given contents.
func writeFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func mustDiscover(t *testing.T, root string) []string {
	t.Helper()
	dirs, err := DiscoverPackageDirs(root)
	if err != nil {
		t.Fatalf("DiscoverPackageDirs: %v", err)
	}
	return dirs
}

// Positive: the root and the members are both present. This is the case both
// callers got wrong — docdistill returned only the root, bump only the members.
func TestDiscoverReturnsRootAndWorkspaceMembers(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name":"root","devDependencies":{"turbo":"^2.10.12"}}`)
	writeFile(t, root, "pnpm-workspace.yaml", "packages:\n  - 'packages/*'\n")
	writeFile(t, root, "packages/alpha/package.json", `{"name":"alpha"}`)
	writeFile(t, root, "packages/beta/package.json", `{"name":"beta"}`)

	want := []string{".", "packages/alpha", "packages/beta"}
	if got := mustDiscover(t, root); !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Positive: npm and yarn declare members in the manifest, in two shapes.
func TestDiscoverReadsNpmWorkspacesInBothShapes(t *testing.T) {
	for name, manifest := range map[string]string{
		"array":  `{"name":"root","workspaces":["packages/*"]}`,
		"object": `{"name":"root","workspaces":{"packages":["packages/*"]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "package.json", manifest)
			writeFile(t, root, "packages/alpha/package.json", `{"name":"alpha"}`)

			want := []string{".", "packages/alpha"}
			if got := mustDiscover(t, root); !equalStrings(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

// Negative: a repository with no Node manifest yields nothing, rather than a
// phantom root entry a caller would then try to read.
func TestDiscoverReturnsNothingWithoutAManifest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/x\n")

	if got := mustDiscover(t, root); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

// Negative: a directory matched by a pattern but carrying no package.json is not
// a workspace member.
func TestDiscoverSkipsMatchedDirectoriesWithoutAManifest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name":"root","workspaces":["packages/*"]}`)
	writeFile(t, root, "packages/alpha/package.json", `{"name":"alpha"}`)
	writeFile(t, root, "packages/docs/README.md", "not a package\n")

	want := []string{".", "packages/alpha"}
	if got := mustDiscover(t, root); !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Boundary: pnpm permits `packages: ['.']`. The root must appear once.
func TestDiscoverDoesNotDoubleCountTheRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name":"root"}`)
	writeFile(t, root, "pnpm-workspace.yaml", "packages:\n  - '.'\n  - 'packages/*'\n")
	writeFile(t, root, "packages/alpha/package.json", `{"name":"alpha"}`)

	want := []string{".", "packages/alpha"}
	if got := mustDiscover(t, root); !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Boundary: a repository declaring both pnpm's file and npm's field gets the
// union, deduplicated — some repositories carry both for tooling that only
// understands one.
func TestDiscoverUnionsPnpmAndNpmDeclarations(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name":"root","workspaces":["apps/*","packages/*"]}`)
	writeFile(t, root, "pnpm-workspace.yaml", "packages:\n  - 'packages/*'\n")
	writeFile(t, root, "packages/alpha/package.json", `{"name":"alpha"}`)
	writeFile(t, root, "apps/web/package.json", `{"name":"web"}`)

	want := []string{".", "apps/web", "packages/alpha"}
	if got := mustDiscover(t, root); !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Boundary: a malformed declaration must not cost the caller the root manifest,
// which is still a real manifest.
func TestDiscoverKeepsTheRootWhenADeclarationIsMalformed(t *testing.T) {
	for name, files := range map[string][2]string{
		"bad yaml":     {"pnpm-workspace.yaml", "packages: [unclosed\n"},
		"bad manifest": {"package.json", `{"name":"root","workspaces":`},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "package.json", `{"name":"root"}`)
			writeFile(t, root, files[0], files[1])

			got := mustDiscover(t, root)
			if len(got) == 0 || got[0] != "." {
				t.Errorf("got %v, want the root to survive", got)
			}
		})
	}
}

// Boundary: a negated pattern is skipped rather than globbed literally, and a
// traversal pattern cannot reach outside the repository.
func TestDiscoverIgnoresNegatedAndEscapingPatterns(t *testing.T) {
	outside := t.TempDir()
	writeFile(t, outside, "package.json", `{"name":"outside"}`)

	root := filepath.Join(outside, "repo")
	writeFile(t, root, "package.json", `{"name":"root"}`)
	writeFile(t, root, "pnpm-workspace.yaml", "packages:\n  - '!packages/private'\n  - '../'\n")

	want := []string{"."}
	if got := mustDiscover(t, root); !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// writeWorkspace creates a root manifest plus members workspace members.
func writeWorkspace(t *testing.T, members int) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name":"root","workspaces":["packages/*"]}`)
	for i := range members {
		writeFile(t, root, fmt.Sprintf("packages/p%04d/package.json", i), `{"name":"p"}`)
	}
	return root
}

// Negative: the resolved set is bounded (HISS-02), and hitting the ceiling is
// reported rather than hidden. The directories found are still returned, so a
// caller that can use a partial set is not left with nothing.
func TestDiscoverReportsTruncationPastTheBound(t *testing.T) {
	root := writeWorkspace(t, MaxWorkspaceDirs+5)

	got, err := DiscoverPackageDirs(root)
	if !errors.Is(err, ErrWorkspaceTruncated) {
		t.Fatalf("err = %v, want ErrWorkspaceTruncated", err)
	}
	if len(got) != MaxWorkspaceDirs {
		t.Errorf("got %d dirs, want the bound of %d", len(got), MaxWorkspaceDirs)
	}
}

// Boundary: a set that fills the bound exactly — root plus MaxWorkspaceDirs-1
// members — examined every member, so it is complete and not truncated.
func TestDiscoverAtExactlyTheBoundIsNotTruncated(t *testing.T) {
	root := writeWorkspace(t, MaxWorkspaceDirs-1)

	got := mustDiscover(t, root)
	if len(got) != MaxWorkspaceDirs {
		t.Errorf("got %d dirs, want %d", len(got), MaxWorkspaceDirs)
	}
}
