package gc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// gcGitFixture creates a real repository and a registered linked worktree in a
// temporary directory. It never touches the caller's Git configuration or cache.
func gcGitFixture(t *testing.T) (root, worktree string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root = t.TempDir()
	runGCTestGit(t, root, "init", "-q")
	runGCTestGit(t, root, "-c", "user.name=gc-test", "-c", "user.email=gc@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	worktree = filepath.Join(root, ".standards", "worktrees", "released-old")
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		t.Fatalf("create worktree parent: %v", err)
	}
	runGCTestGit(t, root, "worktree", "add", "-q", "-b", "gc-fixture", worktree, "HEAD")
	return root, worktree
}

func runGCTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
	}
}

func releasedGCOptions(root string, paths ...string) Options {
	return Options{
		RootDir:        root,
		ReleasedPaths:  paths,
		MaxWorktreeAge: 24 * time.Hour,
		MaxArtifactAge: 24 * time.Hour,
		DryRun:         false,
		SkipGitPrune:   true,
		SkipTestCache:  true,
	}
}

func ageFileT(t *testing.T, path string, age time.Duration) {
	t.Helper()
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("age %s: %v", path, err)
	}
}

func TestCollect_UnknownWorktreeIsProtectedAndReleasedRegisteredWorktreeIsRemoved(t *testing.T) {
	root, released := gcGitFixture(t)
	unknown := filepath.Join(root, ".standards", "worktrees", "unknown-old")
	if err := os.MkdirAll(unknown, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, filepath.Join(unknown, "payload"), "keep me")
	ageTree(t, unknown, 48*time.Hour)
	ageTree(t, released, 48*time.Hour)

	report, err := Collect(context.Background(), releasedGCOptions(root, ".standards/worktrees/released-old"))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if _, statErr := os.Stat(released); !os.IsNotExist(statErr) {
		t.Fatalf("released registered worktree survived or removal failed: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(unknown, "payload")); statErr != nil {
		t.Fatalf("unlisted worktree was deleted: %v", statErr)
	}
	if len(report.SkippedWorktrees) == 0 {
		t.Fatalf("expected unlisted worktree to be reported protected: %+v", report)
	}
}

func TestCollect_ReleasedArtifactRequiresAgeAndRelease(t *testing.T) {
	root := t.TempDir()
	eph := filepath.Join(root, ".standards", "ephemeral")
	tmp := filepath.Join(root, ".standards", "tmp")
	if err := os.MkdirAll(eph, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(eph, "old.sarif")
	fresh := filepath.Join(eph, "fresh.sarif")
	unknown := filepath.Join(eph, "unlisted.sarif")
	for _, path := range []string{old, fresh, unknown} {
		writeFileT(t, path, "fixture")
	}
	ageFileT(t, old, 48*time.Hour)
	ageFileT(t, unknown, 48*time.Hour)

	opts := releasedGCOptions(root, ".standards/ephemeral/old.sarif")
	opts.EphemeralDir = eph
	report, err := Collect(context.Background(), opts)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if _, statErr := os.Stat(old); !os.IsNotExist(statErr) {
		t.Fatalf("released old artifact was not removed: %v", statErr)
	}
	for _, path := range []string{fresh, unknown} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("protected artifact %s was removed: %v", path, statErr)
		}
	}
	if len(report.SkippedArtifacts) == 0 {
		t.Fatalf("expected protected artifacts in report: %+v", report)
	}
}

func TestCollect_RejectsOutsideAndSymlinkReleaseRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.sarif")
	writeFileT(t, outsideFile, "must survive")
	link := filepath.Join(root, ".standards")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	for _, release := range []string{outsideFile, ".standards/ephemeral/outside.sarif"} {
		report, err := Collect(context.Background(), Options{
			RootDir:       root,
			ReleasedPaths: []string{release},
			DryRun:        false,
			SkipGitPrune:  true,
			SkipTestCache: true,
		})
		if err == nil || report == nil {
			t.Fatalf("release path %q should be rejected with a report: report=%+v err=%v", release, report, err)
		}
	}
	if _, err := os.Stat(outsideFile); err != nil {
		t.Fatalf("outside fixture was modified: %v", err)
	}
}

func TestCollect_DryRunReportsPlanWithoutRemovalOrReclaimedBytes(t *testing.T) {
	root, released := gcGitFixture(t)
	ageTree(t, released, 48*time.Hour)
	opts := releasedGCOptions(root, ".standards/worktrees/released-old")
	opts.DryRun = true
	report, err := Collect(context.Background(), opts)
	if err != nil {
		t.Fatalf("Collect dry-run: %v", err)
	}
	if _, statErr := os.Stat(released); statErr != nil {
		t.Fatalf("dry-run removed worktree: %v", statErr)
	}
	if report.ReclaimedBytes != 0 || len(report.PrunedWorktrees) != 0 {
		t.Fatalf("dry-run populated committed removal fields: %+v", report)
	}
	if report.PlannedReclaimedBytes <= 0 || len(report.PlannedWorktrees) != 1 {
		t.Fatalf("dry-run omitted removal plan: %+v", report)
	}
}

func TestCollect_CancelledContextIsIncompleteAndDoesNotDelete(t *testing.T) {
	root, released := gcGitFixture(t)
	ageTree(t, released, 48*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := Collect(ctx, releasedGCOptions(root, ".standards/worktrees/released-old"))
	if err == nil || report == nil {
		t.Fatalf("cancelled GC must return an incomplete report and error: report=%+v err=%v", report, err)
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "cancel") {
		t.Fatalf("expected cancellation error, got %v", err)
	}
	if report.Complete {
		t.Fatalf("cancelled GC reported complete: %+v", report)
	}
	if _, statErr := os.Stat(released); statErr != nil {
		t.Fatalf("cancelled GC removed worktree: %v", statErr)
	}
}

func TestRemoveEntries_PreservesFileCreatedAfterInspection(t *testing.T) {
	rootPath := t.TempDir()
	dirPath := filepath.Join(rootPath, "released")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(dirPath, "original")
	writeFileT(t, original, "original")
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close root: %v", err)
		}
	})
	_, snapshot, err := scanTree(context.Background(), root, "released", false)
	if err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(dirPath, "created-after-scan")
	writeFileT(t, created, "must survive")
	if err := removeEntries(context.Background(), root, snapshot); err == nil {
		t.Fatal("expected nonempty directory removal to fail")
	}
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("file created after inspection was removed: %v", err)
	}
}

func TestRemoveEntries_RejectsSameSizeReplacement(t *testing.T) {
	rootPath := t.TempDir()
	path := filepath.Join(rootPath, "released")
	writeFileT(t, path, "same-size")
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close root: %v", err)
		}
	})
	info, err := root.Lstat("released")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, path, "same-size")
	if err := removeEntries(context.Background(), root, []treeEntry{{path: "released", info: info}}); err == nil {
		t.Fatal("same-size replacement was removed")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("replacement disappeared: %v", err)
	}
}

// cancelAfterChecks is a context that cancels itself on the (limit+1)th Err poll, so a test
// can place cancellation between two specific checks. It honours the context.Context
// contract rather than only the polling path: Done is a real channel that closes at the
// same moment Err first reports context.Canceled, and both stay in that state afterwards,
// so code that selects on Done observes the same cancellation as code that polls Err.
type cancelAfterChecks struct {
	mu     sync.Mutex
	checks int
	limit  int
	done   chan struct{}
}

func newCancelAfterChecks(limit int) *cancelAfterChecks {
	return &cancelAfterChecks{limit: limit, done: make(chan struct{})}
}

func (c *cancelAfterChecks) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelAfterChecks) Done() <-chan struct{}       { return c.done }
func (c *cancelAfterChecks) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checks > c.limit {
		return context.Canceled
	}
	c.checks++
	if c.checks > c.limit {
		close(c.done)
		return context.Canceled
	}
	return nil
}
func (c *cancelAfterChecks) Value(any) any { return nil }

// channelClosed reports whether done is closed without blocking.
func channelClosed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// TestCancelAfterChecksHonoursTheContextContract pins the double itself: Done stays open while
// Err reports nil (positive), closes exactly when Err first reports cancellation (boundary),
// and neither reverts on later polls (negative: no un-cancellation, no double close).
func TestCancelAfterChecksHonoursTheContextContract(t *testing.T) {
	ctx := newCancelAfterChecks(2)
	for poll := 1; poll <= 2; poll++ {
		if err := ctx.Err(); err != nil {
			t.Fatalf("poll %d: Err = %v before the limit", poll, err)
		}
		if channelClosed(ctx.Done()) {
			t.Fatalf("poll %d: Done closed while Err reports nil", poll)
		}
	}
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("poll past the limit: Err = %v, want context.Canceled", err)
	}
	if !channelClosed(ctx.Done()) {
		t.Fatal("Err reports cancellation but Done is still open")
	}
	for poll := 0; poll < 3; poll++ {
		if err := ctx.Err(); !errors.Is(err, context.Canceled) {
			t.Fatalf("later poll %d: Err = %v, cancellation reverted", poll, err)
		}
	}
	if !channelClosed(ctx.Done()) {
		t.Fatal("Done reopened after cancellation")
	}
}

func TestRemoveEntries_StopsDuringRemovalOnCancellation(t *testing.T) {
	rootPath := t.TempDir()
	first := filepath.Join(rootPath, "first")
	second := filepath.Join(rootPath, "second")
	writeFileT(t, first, "first")
	writeFileT(t, second, "second")
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close root: %v", err)
		}
	})
	firstInfo, err := root.Lstat("first")
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := root.Lstat("second")
	if err != nil {
		t.Fatal(err)
	}
	ctx := newCancelAfterChecks(1)
	err = removeEntries(ctx, root, []treeEntry{{path: "first", info: firstInfo}, {path: "second", info: secondInfo}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation during removal, got %v", err)
	}
	if !channelClosed(ctx.Done()) {
		t.Fatal("removal stopped on cancellation the context never signalled through Done")
	}
	if _, statErr := os.Stat(first); statErr != nil {
		t.Fatalf("cancellation removed later entry unexpectedly: %v", statErr)
	}
	if _, statErr := os.Stat(second); !os.IsNotExist(statErr) {
		t.Fatalf("first inspected entry was not removed before cancellation: %v", statErr)
	}
}

func TestCollect_DryRunCacheOptInPlansWithoutGlobalCacheMutation(t *testing.T) {
	root := t.TempDir()
	report, err := Collect(context.Background(), Options{
		RootDir:          root,
		DryRun:           true,
		CleanGoTestCache: true,
		SkipGitPrune:     true,
		SkipTestCache:    false,
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !report.PlannedCacheCleanup || report.Complete != true {
		t.Fatalf("expected complete planned cache cleanup: %+v", report)
	}
	if len(report.CleanedCacheArtifacts) != 0 || report.ReclaimedBytes != 0 {
		t.Fatalf("dry-run cache opt-in performed cleanup: %+v", report)
	}
}

func TestCollect_BareArtifactRepositoryIsProtected(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, ".standards", "ephemeral", "bare")
	if err := os.MkdirAll(filepath.Join(bare, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, filepath.Join(bare, "HEAD"), "ref: refs/heads/main\n")
	ageTree(t, bare, 48*time.Hour)
	report, err := Collect(context.Background(), Options{
		RootDir:        root,
		ReleasedPaths:  []string{".standards/ephemeral/bare"},
		MaxArtifactAge: 24 * time.Hour,
		SkipGitPrune:   true,
		SkipTestCache:  true,
	})
	if err == nil || report == nil || report.Complete {
		t.Fatalf("bare repository must fail closed: report=%+v err=%v", report, err)
	}
	if _, statErr := os.Stat(filepath.Join(bare, "HEAD")); statErr != nil {
		t.Fatalf("bare repository was modified: %v", statErr)
	}
}

func TestCollect_PreflightFailureCausesZeroMutations(t *testing.T) {
	root := t.TempDir()
	eph := filepath.Join(root, ".standards", "ephemeral")
	valid := filepath.Join(eph, "valid")
	invalid := filepath.Join(eph, "invalid-bare")
	if err := os.MkdirAll(filepath.Join(invalid, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, valid, "valid")
	writeFileT(t, filepath.Join(invalid, "HEAD"), "ref: refs/heads/main\n")
	ageFileT(t, valid, 48*time.Hour)
	ageTree(t, invalid, 48*time.Hour)
	report, err := Collect(context.Background(), Options{
		RootDir: root, ReleasedPaths: []string{
			".standards/ephemeral/valid", ".standards/ephemeral/invalid-bare",
		}, MaxArtifactAge: 24 * time.Hour, SkipGitPrune: true, SkipTestCache: true,
	})
	if err == nil || report == nil || report.Complete {
		t.Fatalf("invalid later artifact must fail preflight: report=%+v err=%v", report, err)
	}
	if _, statErr := os.Stat(valid); statErr != nil {
		t.Fatalf("valid earlier artifact was removed before preflight completed: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(invalid, "HEAD")); statErr != nil {
		t.Fatalf("invalid artifact was modified: %v", statErr)
	}
}

func TestCollect_RejectsInvalidPoolsAndUnknownOrMissingRelease(t *testing.T) {
	root := t.TempDir()
	cases := []Options{
		{RootDir: root, WorktreesDir: ".standards", EphemeralDir: ".standards/ephemeral"},
		{RootDir: root, ReleasedPaths: []string{".standards/ephemeral/missing"}},
		{RootDir: root, ReleasedPaths: []string{".standards/unknown/item"}},
	}
	for i, opts := range cases {
		report, err := Collect(context.Background(), opts)
		if err == nil || report == nil || report.Complete {
			t.Errorf("case %d should fail with incomplete report: report=%+v err=%v", i, report, err)
		}
	}
}

// errorSharingViolation is Windows' ERROR_SHARING_VIOLATION. The number means something else
// elsewhere (EPIPE on Linux), so it is compared only on Windows.
const errorSharingViolation syscall.Errno = 32

func TestCheckLiveRootRejectsRenamedAndReplacedRoot(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "root")
	if err := os.Mkdir(rootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	pinned, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	// Registered before anything can fail: an open handle left at cleanup keeps Windows from
	// removing the temporary directory.
	t.Cleanup(func() {
		if err := pinned.Close(); err != nil {
			t.Errorf("close root: %v", err)
		}
	})
	c := collector{opts: Options{RootDir: rootPath}, root: pinned}
	if err := os.Rename(rootPath, filepath.Join(parent, "renamed")); err != nil {
		// Windows refuses to rename a directory while a handle to it is open, so the pinned root
		// cannot be swapped there at all. That refusal is the platform holding the invariant,
		// and it is asserted rather than skipped; the untouched root must still be accepted.
		var errno syscall.Errno
		if runtime.GOOS != "windows" || !errors.As(err, &errno) || errno != errorSharingViolation {
			t.Fatal(err)
		}
		t.Logf("rename of the pinned root refused by the platform: %v", err)
		if err := c.checkLiveRoot(); err != nil {
			t.Fatalf("the untouched pinned root was rejected: %v", err)
		}
		return
	}
	if err := os.Mkdir(rootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := c.checkLiveRoot(); err == nil {
		t.Fatal("renamed and replacement root was accepted")
	}
}
