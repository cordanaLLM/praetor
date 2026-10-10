package util

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitFixture plants one .git entry under root and returns the directory to probe (root
// itself unless the fixture nests the probe inside it). A plant func may t.Skip its own
// fixture only; the others still run.
type gitFixture struct {
	name  string
	plant func(t *testing.T, root string) string
}

func here(plant func(t *testing.T, dir string)) func(t *testing.T, root string) string {
	return func(t *testing.T, root string) string {
		plant(t, root)
		return root
	}
}

func plantHead(t *testing.T, dir, content string) {
	t.Helper()
	plantGitDirectory(t, dir, true, true, false)
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func plantHeadSymlink(t *testing.T, dir, target string) {
	t.Helper()
	plantGitDirectory(t, dir, true, true, false)
	if err := os.Symlink(target, filepath.Join(dir, ".git", "HEAD")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func writeGitFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func plantGitDirectory(t *testing.T, dir string, objects, refs, head bool) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if head {
		if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]bool{"objects": objects, "refs": refs} {
		if want {
			if err := os.Mkdir(filepath.Join(gitDir, name), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func differentialFixtures() []gitFixture {
	return []gitFixture{
		{"absent", here(func(t *testing.T, dir string) {})},
		{"empty-directory", here(func(t *testing.T, dir string) { plantGitDirectory(t, dir, false, false, false) })},
		{"head-only-directory", here(func(t *testing.T, dir string) { plantGitDirectory(t, dir, false, false, true) })},
		{"complete-directory", here(func(t *testing.T, dir string) { plantGitDirectory(t, dir, true, true, true) })},
		{"no-head-directory", here(func(t *testing.T, dir string) { plantGitDirectory(t, dir, true, true, false) })},
		{"empty-head", here(func(t *testing.T, dir string) { plantHead(t, dir, "") })},
		{"garbage-head", here(func(t *testing.T, dir string) { plantHead(t, dir, "garbage\n") })},
		{"ref-outside-refs-head", here(func(t *testing.T, dir string) { plantHead(t, dir, "ref: HEAD\n") })},
		{"object-id-head", here(func(t *testing.T, dir string) { plantHead(t, dir, strings.Repeat("a", 40)+"\n") })},
		{"short-object-id-head", here(func(t *testing.T, dir string) { plantHead(t, dir, strings.Repeat("a", 39)+"\n") })},
		{"head-directory", here(func(t *testing.T, dir string) {
			plantGitDirectory(t, dir, true, true, false)
			if err := os.Mkdir(filepath.Join(dir, ".git", "HEAD"), 0o755); err != nil {
				t.Fatal(err)
			}
		})},
		{"head-symlink-into-refs", here(func(t *testing.T, dir string) { plantHeadSymlink(t, dir, "refs/heads/main") })},
		{"head-symlink-outside-refs", here(func(t *testing.T, dir string) { plantHeadSymlink(t, dir, "/etc/passwd") })},
		{"objects-is-file", here(func(t *testing.T, dir string) {
			plantGitDirectory(t, dir, false, true, true)
			if err := os.WriteFile(filepath.Join(dir, ".git", "objects"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		})},
		{"empty-file", here(func(t *testing.T, dir string) { writeGitFile(t, dir, "") })},
		{"no-gitdir-line", here(func(t *testing.T, dir string) { writeGitFile(t, dir, "junk\n") })},
		{"empty-gitdir-path", here(func(t *testing.T, dir string) { writeGitFile(t, dir, "gitdir: \n") })},
		{"oversized-gitlink", here(func(t *testing.T, dir string) {
			writeGitFile(t, dir, "gitdir: "+strings.Repeat("a", MaxGitlinkBytes+1)+"\n")
		})},
		{"gitlink-target-missing", here(func(t *testing.T, dir string) { writeGitFile(t, dir, "gitdir: gone\n") })},
		{"dangling-symlink", here(func(t *testing.T, dir string) {
			if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, ".git")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		})},
		{"symlinked-complete-repository", here(func(t *testing.T, dir string) {
			real := t.TempDir()
			plantGitDirectory(t, real, true, true, true)
			if err := os.Symlink(filepath.Join(real, ".git"), filepath.Join(dir, ".git")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		})},
		{"nested-in-outer-repository", func(t *testing.T, root string) string {
			plantGitDirectory(t, root, true, true, true)
			inner := filepath.Join(root, "inner")
			if err := os.Mkdir(inner, 0o755); err != nil {
				t.Fatal(err)
			}
			plantGitDirectory(t, inner, false, false, true)
			return inner
		}},
	}
}

// gitStopsWalk runs real git rev-parse --show-toplevel at dir with its ancestor search
// ceiled at root's parent. git stops at the entry when it finds a repository (exit 0) or
// dies for a reason other than "not a git repository", the only message for a skipped entry.
func gitStopsWalk(t *testing.T, root, dir string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_CEILING_DIRECTORIES="+filepath.Dir(root))
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true
	}
	return !strings.Contains(string(out), "not a git repository")
}

// Differential: GitWorktreePresent must agree with real git on every fixture entry. The
// only intended differences from HasValidGitRepo are listed in
// TestGitPredicates_Boundary_DocumentedDivergence; every fixture here has no divergence
// between GitWorktreePresent and git. The fixtures sit in a temporary directory whose
// ancestors hold no repository.
func TestGitWorktreePresent_Differential_AgreesWithGitRevParse(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed, so there is no reference to compare against")
	}
	if probe, err := GitWorktreePresent(t.Context(), t.TempDir()); err != nil || probe {
		t.Skipf("temporary directory has a repository among its ancestors (present=%v, err=%v)", probe, err)
	}
	fixtures := append(differentialFixtures(), platformDifferentialFixtures()...)
	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			root := t.TempDir()
			dir := fx.plant(t, root)
			want := gitStopsWalk(t, root, dir)
			got, err := GitWorktreePresent(t.Context(), dir)
			if err != nil || got != want {
				t.Fatalf("GitWorktreePresent = %v (err %v), git stops the walk = %v", got, err, want)
			}
		})
	}
}
