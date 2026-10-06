package state

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// stageIndexEntries adds count index entries for one blob under bulk/ without writing a file for
// any of them: the fast form of a repository that tracks that many files.
func stageIndexEntries(t *testing.T, root string, count int) {
	t.Helper()
	blob := stateFixtureGit(t, root, "hash-object", "-w", "tracked.txt")
	var info strings.Builder
	for i := 0; i < count; i++ {
		fmt.Fprintf(&info, "100644 %s\tbulk/f%06d.txt\n", blob, i)
	}
	stateFixtureGitInput(t, root, []byte(info.String()), "update-index", "--index-info")
}

func setModTime(t *testing.T, path string, stamp time.Time) {
	t.Helper()
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func requireErrorContains(t *testing.T, err error, parts ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want one containing %q", parts)
	}
	for _, part := range parts {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("error %q does not contain %q", err, part)
		}
	}
}

// TestStateSyncLargeIndex_3D: a repository tracking more files than the former 10,000-entry cap
// syncs and verifies (#775), and the binding still changes when one of those entries does.
func TestStateSyncLargeIndex_3D(t *testing.T) {
	root := syncFixture(t)
	stageIndexEntries(t, root, 10_050)
	stateFixtureGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "bulk")
	if _, err := SyncState(t.Context(), root, "large index"); err != nil {
		t.Fatalf("a repository tracking 10,052 files failed to sync: %v", err)
	}
	if err := VerifyStateSync(t.Context(), root); err != nil {
		t.Fatalf("a large-index sync does not verify: %v", err)
	}
	stateFixtureGit(t, root, "rm", "-q", "--cached", "bulk/f000000.txt")
	if err := VerifyStateSync(t.Context(), root); err == nil {
		t.Fatal("dropping one of 10,052 index entries left the binding unchanged")
	}
}

// TestValidateSyncIndexRecordBound_3D: exactly maxSyncRecords index records pass the count and
// reach the record checks; one more fails naming the count, the bound and what to do.
func TestValidateSyncIndexRecordBound_3D(t *testing.T) {
	row := "H 100644 e69de29bb2d1d6434b8b29ae775ad8c2e48c5391 0\tx\x00"
	if err := validateSyncIndex(strings.Repeat(row, 3)); err != nil {
		t.Fatalf("valid index records refused: %v", err)
	}
	// The short records fail the record check, which shows the count let them through.
	requireErrorContains(t, validateSyncIndex(strings.Repeat("x\x00", maxSyncRecords)), "index record is malformed")
	requireErrorContains(t, validateSyncIndex(strings.Repeat("x\x00", maxSyncRecords+1)),
		fmt.Sprintf("lists %d index entries, over its bound of %d records", maxSyncRecords+1, maxSyncRecords),
		"report the count")
	for listing, want := range map[string]int{"": 0, "a\x00": 1, "a\x00b": 2} {
		if got := listingRecords(listing); got != want {
			t.Fatalf("listingRecords(%q) = %d, want %d", listing, got, want)
		}
	}
}

// TestStateUntrackedRecordBound_3D: the untracked count bound lets exactly maxSyncRecords names
// through to the per-path inspection and refuses one more with the count, the bound and the
// remedy; a name outside the root or one that cannot be inspected is refused by name.
func TestStateUntrackedRecordBound_3D(t *testing.T) {
	root := t.TempDir()
	writeIntegrityFile(t, filepath.Join(root, "notes.txt"), "notes\n")
	records, err := stateUntrackedBinding(t.Context(), root, "notes.txt\x00")
	if err != nil || len(records) != 1 || records[0] != fmt.Sprintf("%x", sha256.Sum256([]byte("notes\n"))) {
		t.Fatalf("a small untracked file is not bound by its bytes: %q, %v", records, err)
	}
	_, err = stateUntrackedBinding(t.Context(), root, strings.Repeat("gone\x00", maxSyncRecords))
	requireErrorContains(t, err, `bind untracked path "gone"`)
	_, err = stateUntrackedBinding(t.Context(), root, strings.Repeat("gone\x00", maxSyncRecords+1))
	requireErrorContains(t, err,
		fmt.Sprintf("lists %d untracked paths, over its bound of %d records", maxSyncRecords+1, maxSyncRecords), ".gitignore")
	_, err = stateUntrackedBinding(t.Context(), root, "../outside\x00")
	requireErrorContains(t, err, `untracked path "../outside" is not local`)
}

// TestStateUntrackedMetadataBinding_3D: a regular file at the per-file bound is bound by the
// SHA-256 of its bytes and a 2 MiB file by type, size and modification time (#776); once the
// content budget is spent, every later file is bound by metadata however small it is.
func TestStateUntrackedMetadataBinding_3D(t *testing.T) {
	root := t.TempDir()
	exact := strings.Repeat("a", contextopt.MaxSourceBytes)
	writeIntegrityFile(t, filepath.Join(root, "exact.bin"), exact)
	big := filepath.Join(root, "big.yuv")
	writeIntegrityFile(t, big, strings.Repeat("\x00", 2<<20))
	stamp := time.Unix(1_700_000_000, 0)
	setModTime(t, big, stamp)
	records, err := stateUntrackedBinding(t.Context(), root, "exact.bin\x00big.yuv\x00")
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256([]byte(exact))); records[0] != want {
		t.Fatalf("a file at the per-file bound got %q, want its digest", records[0])
	}
	if want := fmt.Sprintf("metadata ---------- %d %d", 2<<20, stamp.UnixNano()); records[1] != want {
		t.Fatalf("a 2 MiB file got %q, want %q", records[1], want)
	}

	var listing strings.Builder
	for i := 0; i < contextopt.MaxTotalBytes/contextopt.MaxSourceBytes; i++ {
		name := fmt.Sprintf("part%d.bin", i)
		writeIntegrityFile(t, filepath.Join(root, name), exact)
		listing.WriteString(name + "\x00")
	}
	writeIntegrityFile(t, filepath.Join(root, "late.txt"), "x")
	listing.WriteString("late.txt\x00")
	records, err = stateUntrackedBinding(t.Context(), root, listing.String())
	if err != nil {
		t.Fatal(err)
	}
	if last := records[len(records)-2]; len(last) != 64 {
		t.Fatalf("the file that spends the budget exactly got %q, want its digest", last)
	}
	if late := records[len(records)-1]; !strings.HasPrefix(late, "metadata ---------- 1 ") {
		t.Fatalf("a file past the content budget got %q, want a metadata record", late)
	}
}

// TestStateSyncStrayUntrackedFiles_3D: a 2 MiB untracked file and an untracked nested
// repository no longer fail the sync (#776), and the binding goes stale when the large file's
// modification time or size changes.
func TestStateSyncStrayUntrackedFiles_3D(t *testing.T) {
	root := syncFixture(t)
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	stateFixtureGit(t, nested, "init", "-q")
	stateFixtureGit(t, nested, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-q", "--allow-empty", "-m", "nested")
	media := filepath.Join(root, "media")
	if err := os.Mkdir(media, 0o700); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(media, "decoded.yuv")
	writeIntegrityFile(t, big, strings.Repeat("\x00", 2<<20))
	if _, err := SyncState(t.Context(), root, "stray untracked files"); err != nil {
		t.Fatalf("a 2 MiB untracked file or a nested repository failed the sync: %v", err)
	}
	if err := VerifyStateSync(t.Context(), root); err != nil {
		t.Fatalf("metadata-bound untracked paths do not verify: %v", err)
	}
	setModTime(t, big, time.Unix(1_700_000_100, 0))
	if err := VerifyStateSync(t.Context(), root); err == nil {
		t.Fatal("a new modification time on a metadata-bound file left the binding unchanged")
	}
	if _, err := SyncState(t.Context(), root, "after touch"); err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, big, strings.Repeat("\x00", 2<<20+1))
	setModTime(t, big, time.Unix(1_700_000_100, 0))
	if err := VerifyStateSync(t.Context(), root); err == nil {
		t.Fatal("a new size on a metadata-bound file left the binding unchanged")
	}
}

// TestStateSyncNamedPipeUntracked_3D: a named pipe called "-" beside the tracked files does not
// fail the sync, and one the untracked listing names is bound by its metadata, which changes
// with its modification time. Named pipes need mkfifo; MakeFIFO skips elsewhere (HISS-21).
func TestStateSyncNamedPipeUntracked_3D(t *testing.T) {
	root := syncFixture(t)
	pipe := filepath.Join(root, "-")
	testsupport.MakeFIFO(t, pipe)
	if _, err := SyncState(t.Context(), root, "named pipe"); err != nil {
		t.Fatalf("an untracked named pipe failed the sync: %v", err)
	}
	if err := VerifyStateSync(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	setModTime(t, pipe, time.Unix(1_700_000_000, 0))
	before, err := stateUntrackedBinding(t.Context(), root, "-\x00")
	if err != nil || len(before) != 1 || !strings.HasPrefix(before[0], "metadata p") {
		t.Fatalf("a listed named pipe is not bound by metadata: %q, %v", before, err)
	}
	setModTime(t, pipe, time.Unix(1_700_000_001, 0))
	after, err := stateUntrackedBinding(t.Context(), root, "-\x00")
	if err != nil || after[0] == before[0] {
		t.Fatalf("a new modification time on a named pipe left its record %q unchanged: %v", before[0], err)
	}
}

// TestStateSyncUntrackedSymlink: an untracked symlink, dangling here, is bound by its metadata
// instead of failing the sync. Platforms that refuse to create symlinks skip (HISS-21).
func TestStateSyncUntrackedSymlink(t *testing.T) {
	root := syncFixture(t)
	if err := os.Symlink("nowhere", filepath.Join(root, "dangling")); err != nil {
		t.Skipf("this platform refuses to create a symlink: %v", err)
	}
	if _, err := SyncState(t.Context(), root, "untracked symlink"); err != nil {
		t.Fatalf("an untracked symlink failed the sync: %v", err)
	}
	records, err := stateUntrackedBinding(t.Context(), root, "dangling\x00")
	if err != nil || !strings.HasPrefix(records[0], "metadata L") {
		t.Fatalf("an untracked symlink is not bound by metadata: %q, %v", records, err)
	}
}

// TestStateSyncIrregularStateInputFailsClosed_3D: the ledgers and tracked files are state inputs,
// so one that is irregular or over the per-file bound still fails the sync, naming its path and
// the reason; a ledger at the bound binds.
func TestStateSyncIrregularStateInputFailsClosed_3D(t *testing.T) {
	t.Run("oversized ledger", func(t *testing.T) {
		root := syncFixture(t)
		backlog := filepath.Join(root, WorkingDirName, "BACKLOG.md")
		writeIntegrityFile(t, backlog, strings.Repeat("x", contextopt.MaxSourceBytes))
		if _, err := SyncState(t.Context(), root, "ledger at the bound"); err != nil {
			t.Fatalf("a ledger at the per-file bound failed the sync: %v", err)
		}
		writeIntegrityFile(t, backlog, strings.Repeat("x", contextopt.MaxSourceBytes+1))
		_, err := SyncState(t.Context(), root, "oversized ledger")
		requireErrorContains(t, err, ".workingdir/BACKLOG.md: 1048577 bytes, over the 1048576-byte limit", "fail closed")
	})
	t.Run("named pipe ledger", func(t *testing.T) {
		root := syncFixture(t)
		backlog := filepath.Join(root, WorkingDirName, "BACKLOG.md")
		if err := os.Remove(backlog); err != nil {
			t.Fatal(err)
		}
		testsupport.MakeFIFO(t, backlog)
		_, err := SyncState(t.Context(), root, "named pipe ledger")
		requireErrorContains(t, err, ".workingdir/BACKLOG.md: not a regular file (mode p", "fail closed")
	})
	t.Run("named pipe tracked file", func(t *testing.T) {
		root := syncFixture(t)
		tracked := filepath.Join(root, "tracked.txt")
		if err := os.Remove(tracked); err != nil {
			t.Fatal(err)
		}
		testsupport.MakeFIFO(t, tracked)
		_, err := SyncState(t.Context(), root, "named pipe tracked file")
		requireErrorContains(t, err, "bind state Git unstaged diff", "tracked.txt")
	})
}

// TestRunSyncProbeByteBound_3D: a binding listing exactly at its byte cap binds; one byte more
// than the cap fails naming the listing, the cap, the records read before it and the remedy.
func TestRunSyncProbeByteBound_3D(t *testing.T) {
	root := syncFixture(t)
	probe := stateGitProbes("available")[1]
	listing, err := runSyncProbe(t.Context(), root, probe)
	if err != nil || listingRecords(listing) != 2 {
		t.Fatalf("index listing = %q, %v; want the two fixture entries", listing, err)
	}
	probe.limit = len(listing)
	if exact, err := runSyncProbe(t.Context(), root, probe); err != nil || exact != listing {
		t.Fatalf("a listing exactly at its cap: %q, %v", exact, err)
	}
	probe.limit = len(listing) - 1
	_, err = runSyncProbe(t.Context(), root, probe)
	requireErrorContains(t, err,
		fmt.Sprintf("cannot bind the Git index listing: it exceeds %d bytes after 1 records", probe.limit), syncReportIndex)
}
