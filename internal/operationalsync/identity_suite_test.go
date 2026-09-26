package operationalsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// identitySensitivePackages lists the packages whose own tests read this checkout's repository
// identity straight off disk: internal/config reads its own .standards.yaml, and
// internal/forge/internal/adopt compare the checked-in .github/workflows and
// .github/rulesets/main.json against it. #255 found five such tests failing under an
// operational fork's owner overlay; #258 fixed those in place. #263 then found a sixth
// (internal/config's own repository-manifest test) that the fix-one-at-a-time approach missed,
// because nothing replayed the whole suite against an overlaid copy -- only the five tests
// already known to be identity-sensitive got a regression test. This guard replays the actual
// failure mode instead: it copies this repository's tracked tree, applies the real owner
// overlay to .standards.yaml through this package's own ownerManifest, and runs these
// packages' full test suites against the copy, so any test -- present or future, known or not
// -- that assumes the canonical identity fails here first, not in a real fork's pre-push gate.
var identitySensitivePackages = []string{"./internal/config/...", "./internal/forge/...", "./internal/adopt/..."}

// maxOverlaySuiteFiles bounds the tracked-file copy (HISS-02). The repository carries under
// 2,000 tracked files today; an order of magnitude above that is still a defensive ceiling, not
// a size this repository is expected to approach.
const maxOverlaySuiteFiles = 20000

// maxOverlaySuiteFileBytes bounds each copied file (HISS-02). The largest tracked file today is
// half a megabyte; this stays a generous multiple above that rather than tracking it exactly.
const maxOverlaySuiteFileBytes = 8 << 20

// TestIdentitySensitivePackagesPassUnderTheOwnerOverlay is the durable guard #263 asks for: a
// one-at-a-time fix to a named failing test does not converge, because the next field the
// overlay writes finds the next test nobody added a regression for. Running the real suites
// against a real overlaid copy catches that class of regression without naming individual
// tests. It needs no git remote round trip -- unlike a real `operational sync init`, which
// validates origin/upstream remotes this checkout's shared Git config must not be repurposed
// for a throwaway check -- because the overlay transformation itself is applied in-process
// through ownerManifest, the exact function plan/prepare/init already depend on. That keeps the
// guard's only platform dependency the `git`/`go` binaries every contributor and CI runner
// already needs to build this repository, so it runs on Linux, macOS and Windows alike
// (HISS-21) rather than skipping.
func TestIdentitySensitivePackagesPassUnderTheOwnerOverlay(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a nested go test over a repository copy; excluded from -short")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	copyTrackedTree(t, ctx, repoRoot, dest)
	overlayManifestInPlace(t, dest)

	args := append([]string{"test", "-count=1"}, identitySensitivePackages...)
	out, runErr := util.RunCommand(ctx, dest, "go", args...)
	if runErr != nil {
		t.Fatalf("identity-sensitive packages failed under the owner overlay (%v):\n%s", runErr, out)
	}
}

// TestCopyTrackedTreeIncludesNonIgnoredUntrackedFiles keeps the overlay guard equivalent to a
// direct `go test ./...` from a dirty development checkout. A newly introduced package exists
// before its first commit and must reach the copied tree; ignored private state must not.
func TestCopyTrackedTreeIncludesNonIgnoredUntrackedFiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	g, err := newGit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	testGit(t, g, source, "init", "--quiet")
	testWrite(t, source, ".gitignore", "/private/\n")
	testWrite(t, source, "tracked.go", "package fixture\n")
	testWrite(t, source, "new/package.go", "package added\n")
	testWrite(t, source, "private/state.go", "package private\n")
	testGit(t, g, source, "add", "--", ".gitignore", "tracked.go")

	dest := t.TempDir()
	copyTrackedTree(t, ctx, source, dest)
	for _, rel := range []string{".gitignore", "tracked.go", "new/package.go"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("expected copied working-tree file %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "private", "state.go")); !os.IsNotExist(err) {
		t.Fatalf("ignored private file copied into overlay fixture: %v", err)
	}
}

// copyTrackedTree copies repoRoot's Git-tracked and non-ignored untracked working-tree files into
// dest, preserving their relative paths. It reads the working tree rather than a committed blob
// so a guard run during local development also covers uncommitted packages, exactly as
// `go test ./...` run directly against repoRoot would see them.
func copyTrackedTree(t *testing.T, ctx context.Context, repoRoot, dest string) {
	t.Helper()
	g, err := newGit(ctx)
	if err != nil {
		t.Fatalf("locate git: %v", err)
	}
	out, err := g.run(ctx, repoRoot, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		t.Fatalf("list tracked files: %v", err)
	}
	paths := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(paths) == 1 && paths[0] == "" {
		t.Fatal("repository reports no tracked files")
	}
	if len(paths) > maxOverlaySuiteFiles {
		t.Fatalf("tracked files exceed the guard's bound: %d > %d", len(paths), maxOverlaySuiteFiles)
	}
	for _, rel := range paths {
		copyTrackedFile(t, repoRoot, dest, rel)
	}
}

func copyTrackedFile(t *testing.T, repoRoot, dest, rel string) {
	t.Helper()
	info, err := os.Lstat(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		// A path Git reports but the working tree lacks (removed after the index was read,
		// or a submodule gitlink entry) is not this guard's concern; the real build and its
		// own tests already require a consistent tree.
		return
	}
	if !info.Mode().IsRegular() {
		return
	}
	if info.Size() > maxOverlaySuiteFileBytes {
		t.Fatalf("tracked file exceeds the guard's per-file bound: %s (%d bytes)", rel, info.Size())
	}
	data, err := util.ReadConfined(repoRoot, rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	target, err := util.ConfinePath(dest, rel)
	if err != nil {
		t.Fatalf("confine %s: %v", rel, err)
	}
	if err := util.MkdirSecure(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("create parent directory for %s: %v", rel, err)
	}
	if err := util.WriteFileSecure(target, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// overlayManifestInPlace rewrites dest's .standards.yaml with the same owner overlay
// `operational sync init` writes, through this package's own ownerManifest rather than a
// hand-rolled approximation, so the guard exercises the exact transformation a real fork
// carries rather than a stand-in that could silently drift from it.
func overlayManifestInPlace(t *testing.T, dest string) {
	t.Helper()
	raw, err := util.ReadConfined(dest, ownerPaths[0])
	if err != nil {
		t.Fatalf("read copied manifest: %v", err)
	}
	overlaid, err := ownerManifest(raw, identity{Owner: "lusoris", Name: "praetor", Visibility: "private"})
	if err != nil {
		t.Fatalf("apply owner overlay: %v", err)
	}
	target, err := util.ConfinePath(dest, ownerPaths[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := util.WriteFileSecure(target, overlaid, 0o644); err != nil {
		t.Fatalf("write overlaid manifest: %v", err)
	}
}
