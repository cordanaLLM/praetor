package operationalsync

import (
	"context"
	"errors"
	"fmt"
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
// The run keeps every test of internal/adopt, although that package dominates its time (#831):
// no test there is marked as reading identity, a test reaches the copy's .standards.yaml through
// any helper that resolves the repository root, and a name or file selection would be the same
// one-at-a-time list #263 found incomplete.
var identitySensitivePackages = []string{"./internal/config/...", "./internal/forge/...", "./internal/adopt/..."}

// maxOverlaySuiteFiles bounds the tracked-file copy (HISS-02). The repository carries under
// 2,000 tracked files today; an order of magnitude above that is still a defensive ceiling, not
// a size this repository is expected to approach.
const maxOverlaySuiteFiles = 20000

// maxOverlaySuiteFileBytes bounds each copied file (HISS-02). The largest tracked file today is
// half a megabyte; this stays a generous multiple above that rather than tracking it exactly.
const maxOverlaySuiteFileBytes = 8 << 20

// overlaySuiteMargin is the part of the outer go test -timeout deadline the guard holds back
// from its nested run. The copy and the overlay run inside the nested deadline and are bounded
// by it (HISS-02); the margin covers what must still happen after it, before the outer binary's
// own timeout panics without a failing assertion: stopping the nested go test, which gets
// util.CommandWaitDelay of grace before it is killed, and reporting its output.
const overlaySuiteMargin = time.Minute

// overlaySuiteMinimum is the shortest window the guard starts its copy, overlay and nested run
// in. The nested go test builds and runs three packages' tests, and the fastest whole run
// measured took 37 s with a warm build cache on a 32-core Linux host (#831); a window under
// this floor cannot hold that run on any runner, so it fails at once and names both deadlines.
// A window above the floor that still proves too short fails through overlaySuiteFailure,
// which names both deadlines as well.
const overlaySuiteMinimum = 15 * time.Second

// overlaySuiteUnbounded bounds the nested run when go test -timeout 0 sets no outer deadline
// (HISS-02). It equals the -timeout 30m that make test and the Portability workflow pass.
const overlaySuiteUnbounded = 30 * time.Minute

// suiteDeadline is the nested go test's deadline and the outer go test -timeout deadline it was
// derived from; hasOuter is false when -timeout 0 set none.
type suiteDeadline struct {
	nested, outer time.Time
	hasOuter      bool
}

// String names both deadlines, so a failure says which one ran out and which one set it.
func (d suiteDeadline) String() string {
	outer := "none (go test -timeout 0)"
	if d.hasOuter {
		outer = d.outer.Format(time.RFC3339)
	}
	return fmt.Sprintf("nested go test deadline %s, outer go test -timeout deadline %s", d.nested.Format(time.RFC3339), outer)
}

// deriveSuiteDeadline derives the nested run's deadline from the outer test binary's, given as
// t.Deadline reports it: the outer deadline less overlaySuiteMargin, or now plus
// overlaySuiteUnbounded when there is none. It refuses a window under overlaySuiteMinimum and
// names both deadlines and the flag that moves them (#831).
func deriveSuiteDeadline(now, outer time.Time, hasOuter bool) (suiteDeadline, error) {
	if !hasOuter {
		return suiteDeadline{nested: now.Add(overlaySuiteUnbounded)}, nil
	}
	d := suiteDeadline{nested: outer.Add(-overlaySuiteMargin), outer: outer, hasOuter: true}
	if left := d.nested.Sub(now); left < overlaySuiteMinimum {
		return d, fmt.Errorf("%s: %s left for the nested run after the %s margin, under its %s minimum; raise go test -timeout",
			d, left.Round(time.Second), overlaySuiteMargin, overlaySuiteMinimum)
	}
	return d, nil
}

// overlaySuiteFailure words a failed nested run. A run its derived deadline stopped names both
// deadlines; any other failure is the identity regression the guard exists to report.
func overlaySuiteFailure(d suiteDeadline, ctxErr, runErr error, out string) string {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return fmt.Sprintf("identity-sensitive packages did not finish by the %s (%v):\n%s", d, runErr, out)
	}
	return fmt.Sprintf("identity-sensitive packages failed under the owner overlay (%v):\n%s", runErr, out)
}

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
	// The nested run's deadline comes from the outer go test -timeout rather than a fixed minute
	// count: internal/adopt alone took 341 to 557 s on macOS runners, past the eight minutes this
	// guard once allowed (#831), and any fixed count only moves that cliff (HISS-21).
	outer, hasOuter := t.Deadline()
	deadline, err := deriveSuiteDeadline(time.Now(), outer, hasOuter)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline.nested)
	defer cancel()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	out, runErr := runOverlaySuite(t, ctx, repoRoot, identitySensitivePackages)
	if runErr != nil {
		t.Fatal(overlaySuiteFailure(deadline, ctx.Err(), runErr, out))
	}
}

// runOverlaySuite copies repoRoot's working tree into a fresh directory, applies the owner
// overlay to the copy's .standards.yaml and runs go test over packages there. It returns the
// nested run's output and error instead of failing t, so the guard and the fixture proving it
// refuses a planted regression share one implementation.
func runOverlaySuite(t *testing.T, ctx context.Context, repoRoot string, packages []string) (string, error) {
	t.Helper()
	dest := t.TempDir()
	copyTrackedTree(t, ctx, repoRoot, dest)
	overlayManifestInPlace(t, dest)
	args := append([]string{"test", "-count=1"}, packages...)
	return util.RunCommand(ctx, dest, "go", args...)
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
	overlaid, err := ownerManifest(raw, identity{Owner: "example-owner", Name: "praetor", Visibility: "private"})
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
