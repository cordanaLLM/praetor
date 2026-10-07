package milestone

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/state"
)

// Issue #412: a create, close or remote sync that fails after milestones.json was written
// must not leave the store and BACKLOG.md disagreeing. These tests fail each commit step on
// purpose, through the commitWriters seam or a busy working-directory lock, and prove the
// store returns to its prior bytes or absence, or keeps its change when BACKLOG.md landed
// before the failure. commitWriters is package state, so none of them runs in parallel.

var (
	errInjected = errors.New("injected commit fault")
	errRestore  = errors.New("injected restore fault")
)

const (
	restoredNote  = "restored to its prior state"
	publishedNote = "published before the failure"
)

// injectWriters swaps the commit writers, keeping every writer override leaves nil at its
// production value. The returned function puts the production writers back at once; the
// test cleanup does it as well.
func injectWriters(t *testing.T, override ledgerWriters) func() {
	t.Helper()
	saved := commitWriters
	if override.writeStore == nil {
		override.writeStore = saved.writeStore
	}
	if override.removeStore == nil {
		override.removeStore = saved.removeStore
	}
	if override.writeBacklog == nil {
		override.writeBacklog = saved.writeBacklog
	}
	commitWriters = override
	restore := func() { commitWriters = saved }
	t.Cleanup(restore)
	return restore
}

// ledgerFiles is both ledger files as a test found them.
type ledgerFiles struct {
	store       []byte
	storeExists bool
	backlog     string
}

func readLedgers(t *testing.T, dir string) ledgerFiles {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, state.WorkingDirName, MilestonesFile))
	if errors.Is(err, fs.ErrNotExist) {
		return ledgerFiles{backlog: readBacklog(t, dir)}
	}
	if err != nil {
		t.Fatalf("read milestones.json: %v", err)
	}
	return ledgerFiles{store: data, storeExists: true, backlog: readBacklog(t, dir)}
}

func assertLedgersUnchanged(t *testing.T, dir string, before ledgerFiles) {
	t.Helper()
	after := readLedgers(t, dir)
	if after.storeExists != before.storeExists || !bytes.Equal(after.store, before.store) {
		t.Fatalf("failed commit left milestones.json changed (existed %v, now %v):\n%s",
			before.storeExists, after.storeExists, after.store)
	}
	if after.backlog != before.backlog {
		t.Fatalf("failed commit changed BACKLOG.md:\n%s", after.backlog)
	}
}

// seedLedger returns a repository whose working directory holds BACKLOG.md and, when
// withStore is set, a store with one open milestone #1 titled "existing".
func seedLedger(t *testing.T, withStore bool) string {
	t.Helper()
	dir := setupTestDir(t)
	if withStore {
		writeStoreFixture(t, dir, &MilestoneStore{Milestones: []Milestone{{Number: 1, Title: "existing", State: StateOpen}}})
	}
	return dir
}

// assertSingleRow fails unless exactly one stored milestone is titled title, with number,
// and BACKLOG.md renders it.
func assertSingleRow(t *testing.T, dir, title string, number int) {
	t.Helper()
	found := 0
	for _, row := range storeRows(t, dir) {
		if row.Title == title {
			found++
			if row.Number != number {
				t.Fatalf("milestone %q has number %d, want %d", title, row.Number, number)
			}
		}
	}
	if found != 1 {
		t.Fatalf("store holds %d milestones titled %q, want exactly 1", found, title)
	}
	if !strings.Contains(readBacklog(t, dir), "**"+title+"**") {
		t.Fatalf("BACKLOG.md does not render %q:\n%s", title, readBacklog(t, dir))
	}
}

// holdWorkingDirLock takes the lock every BACKLOG.md writer takes on the working directory,
// as a concurrent ledger writer would, and returns its idempotent release.
func holdWorkingDirLock(t *testing.T, dir string) func() {
	t.Helper()
	root, err := os.OpenRoot(filepath.Join(dir, state.WorkingDirName))
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := contextopt.LockDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(errors.Join(err, root.Close()))
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		if err := errors.Join(unlock(), root.Close()); err != nil {
			t.Errorf("release working-directory lock: %v", err)
		}
	}
	t.Cleanup(release)
	return release
}

// commitOperation is one ledger-committing command, the store it starts from and what a
// successful retry must leave behind.
type commitOperation struct {
	withStore bool
	run       func(ctx context.Context, dir string) error
	converged func(t *testing.T, dir string)
}

func commitOperations(endpoint string) map[string]commitOperation {
	create := func(title string) func(context.Context, string) error {
		return func(ctx context.Context, dir string) error {
			_, err := CreateMilestone(ctx, dir, title, "", nil)
			return err
		}
	}
	return map[string]commitOperation{
		"create into an absent store": {false, create("v1"), func(t *testing.T, dir string) {
			assertSingleRow(t, dir, "v1", 1)
		}},
		"create beside an existing store": {true, create("v2"), func(t *testing.T, dir string) {
			assertSingleRow(t, dir, "v2", 2)
			assertSingleRow(t, dir, "existing", 1)
		}},
		"close": {true, func(ctx context.Context, dir string) error {
			_, err := CloseMilestone(ctx, dir, "1")
			return err
		}, func(t *testing.T, dir string) {
			if rows := storeRows(t, dir); len(rows) != 1 || rows[0].State != StateClosed {
				t.Fatalf("retried close did not converge: %+v", rows)
			}
			if !strings.Contains(readBacklog(t, dir), "Closed") {
				t.Fatal("BACKLOG.md does not render the retried close")
			}
		}},
		"remote sync": {true, func(ctx context.Context, dir string) error {
			_, err := SyncWithGitHub(ctx, dir, "owner", "repo", "test-token", endpoint)
			return err
		}, func(t *testing.T, dir string) {
			assertSingleRow(t, dir, "remote", 2)
			assertSingleRow(t, dir, "existing", 1)
		}},
	}
}

// Negative: a BACKLOG.md write refused because another ledger writer holds the working
// directory fails create, close and remote sync with the store restored, on every platform.
// Positive: the retry after the lock is released writes both ledgers once.
func TestCommit_Negative_BusyWorkingDirLockRestoresStore(t *testing.T) {
	isolateForge(t)
	_, srv := newFakeForge(t, RemoteMilestone{Number: 7, Title: "remote", State: StateOpen})
	for name, op := range commitOperations(srv.URL) {
		t.Run(name, func(t *testing.T) { assertBusyLockRestores(t, op) })
	}
}

func assertBusyLockRestores(t *testing.T, op commitOperation) {
	t.Helper()
	ctx := context.Background()
	dir := seedLedger(t, op.withStore)
	before := readLedgers(t, dir)
	release := holdWorkingDirLock(t, dir)
	// A writer of this process waits for the holder until its context ends (#820); a zero
	// budget makes the BACKLOG.md write give up at once, as a write held past its budget does.
	impatient, err := contextopt.WithDirectoryLockBudget(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = op.run(impatient, dir)
	if err == nil || !strings.Contains(err.Error(), restoredNote) {
		t.Fatalf("busy BACKLOG.md write must fail with the store restored: %v", err)
	}
	assertLedgersUnchanged(t, dir, before)
	release()
	if err := op.run(ctx, dir); err != nil {
		t.Fatalf("retry after the lock is released: %v", err)
	}
	op.converged(t, dir)
}

// landThenFail writes the store and reports a failure on its first call, as a write whose
// rename landed before a later step failed would; every later call writes normally.
func landThenFail() func(string, []byte) error {
	calls := 0
	return func(rootPath string, data []byte) error {
		calls++
		if err := writeStoreFile(rootPath, data); err != nil {
			return err
		}
		if calls == 1 {
			return errInjected
		}
		return nil
	}
}

// backlogLandThenFail publishes BACKLOG.md through the production writer and then reports
// a failure, as contextopt.ReplaceRootSnapshot does when its directory sync, unlock or
// staging cleanup fails after the rename or link already published the file.
func backlogLandThenFail(ctx context.Context, update *backlogUpdate) error {
	return errors.Join(writeBacklog(ctx, update), errInjected)
}

// stepFault is one commit step failed on purpose, and the outcome note the error must
// carry: none when nothing was written, restoredNote when the store write had to be
// undone, publishedNote when BACKLOG.md landed and the store keeps the matching change.
type stepFault struct {
	override func() ledgerWriters
	note     string
}

// Negative: every commit step that fails leaves both ledgers agreeing, whether the store
// existed or not. A step that failed before BACKLOG.md landed leaves both as the command
// found them and the retry converges without a duplicate; a BACKLOG.md write that landed
// and then failed leaves both carrying the change.
func TestCommit_Negative_StepFaultsKeepLedgersAgreeing(t *testing.T) {
	steps := map[string]stepFault{
		"store write refused": {func() ledgerWriters {
			return ledgerWriters{writeStore: func(string, []byte) error { return errInjected }}
		}, ""},
		"store write landed then failed": {func() ledgerWriters {
			return ledgerWriters{writeStore: landThenFail()}
		}, restoredNote},
		"backlog write failed": {func() ledgerWriters {
			return ledgerWriters{writeBacklog: func(context.Context, *backlogUpdate) error { return errInjected }}
		}, restoredNote},
		"backlog write landed then failed": {func() ledgerWriters {
			return ledgerWriters{writeBacklog: backlogLandThenFail}
		}, publishedNote},
	}
	for name, step := range steps {
		for _, withStore := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/store exists=%v", name, withStore), func(t *testing.T) {
				assertStepFaultSettles(t, step, withStore)
			})
		}
	}
}

func assertStepFaultSettles(t *testing.T, step stepFault, withStore bool) {
	t.Helper()
	ctx := context.Background()
	dir := seedLedger(t, withStore)
	before := readLedgers(t, dir)
	reset := injectWriters(t, step.override())
	_, err := CreateMilestone(ctx, dir, "new", "", nil)
	assertOutcomeNote(t, err, step.note)
	want := 1
	if withStore {
		want = 2
	}
	if step.note == publishedNote {
		assertSingleRow(t, dir, "new", want)
		return
	}
	assertLedgersUnchanged(t, dir, before)
	reset()
	if _, err := CreateMilestone(ctx, dir, "new", "", nil); err != nil {
		t.Fatalf("retry after the fault: %v", err)
	}
	assertSingleRow(t, dir, "new", want)
}

// assertOutcomeNote fails unless err carries the injected fault and exactly the outcome
// note named, or no note at all when note is empty.
func assertOutcomeNote(t *testing.T, err error, note string) {
	t.Helper()
	if !errors.Is(err, errInjected) {
		t.Fatalf("error = %v, want the injected fault", err)
	}
	for _, candidate := range []string{restoredNote, publishedNote} {
		if strings.Contains(err.Error(), candidate) != (candidate == note) {
			t.Fatalf("error = %v, want outcome note %q", err, note)
		}
	}
}

// Negative: when the restore itself fails, the error carries both failures and says the
// store keeps the change, so the operator is never told the ledger is clean when it is not.
func TestCommit_Negative_RestoreFailureReportsBothErrors(t *testing.T) {
	for _, withStore := range []bool{false, true} {
		t.Run(fmt.Sprintf("store exists=%v", withStore), func(t *testing.T) {
			dir := seedLedger(t, withStore)
			writes := 0
			injectWriters(t, ledgerWriters{
				writeBacklog: func(context.Context, *backlogUpdate) error { return errInjected },
				removeStore:  func(string) error { return errRestore },
				writeStore: func(rootPath string, data []byte) error {
					writes++
					if writes > 1 {
						return errRestore
					}
					return writeStoreFile(rootPath, data)
				},
			})
			_, err := CreateMilestone(context.Background(), dir, "kept", "", nil)
			if !errors.Is(err, errInjected) || !errors.Is(err, errRestore) || !strings.Contains(err.Error(), "keeps this change") {
				t.Fatalf("error = %v, want the fault and the restore failure", err)
			}
			if rows := storeRows(t, dir); rows[len(rows)-1].Title != "kept" {
				t.Fatalf("the reported change is not the store's: %+v", rows)
			}
		})
	}
}

// Boundary: a caller whose context is cancelled while BACKLOG.md is written still gets the
// store restored; the restore runs detached from that cancellation.
func TestCommit_Boundary_CancelledCallerStillRestores(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := seedLedger(t, true)
	before := readLedgers(t, dir)
	injectWriters(t, ledgerWriters{writeBacklog: func(ctx context.Context, _ *backlogUpdate) error {
		cancel()
		return ctx.Err()
	}})
	_, err := CloseMilestone(ctx, dir, "1")
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), restoredNote) {
		t.Fatalf("error = %v, want the cancellation with the store restored", err)
	}
	assertLedgersUnchanged(t, dir, before)
}

// Boundary: a caller cancelled after BACKLOG.md was published, the window in which the
// compare-and-swap writer's directory sync reports the cancellation, keeps the store
// change: restoring it would leave BACKLOG.md rendering a close the store no longer holds.
func TestCommit_Boundary_CancelledAfterPublishKeepsBothLedgers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := seedLedger(t, true)
	injectWriters(t, ledgerWriters{writeBacklog: func(ctx context.Context, update *backlogUpdate) error {
		if err := writeBacklog(ctx, update); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	}})
	_, err := CloseMilestone(ctx, dir, "1")
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), publishedNote) {
		t.Fatalf("error = %v, want the cancellation with both ledgers carrying the close", err)
	}
	if rows := storeRows(t, dir); len(rows) != 1 || rows[0].State != StateClosed {
		t.Fatalf("store dropped the close BACKLOG.md renders: %+v", rows)
	}
	if !strings.Contains(readBacklog(t, dir), "Closed") {
		t.Fatal("BACKLOG.md does not render the close")
	}
}

// Boundary: a render byte-identical to the BACKLOG.md it was rendered from proves nothing
// about whether the failed write landed, and either way leaves BACKLOG.md as found, so the
// store is put back as well. Closing an already closed milestone changes only its
// timestamp, which the block does not render.
func TestCommit_Boundary_UnchangedRenderRestoresStore(t *testing.T) {
	ctx := context.Background()
	dir := seedLedger(t, true)
	if _, err := CloseMilestone(ctx, dir, "1"); err != nil {
		t.Fatalf("first close: %v", err)
	}
	before := readLedgers(t, dir)
	injectWriters(t, ledgerWriters{writeBacklog: backlogLandThenFail})
	_, err := CloseMilestone(ctx, dir, "1")
	if !errors.Is(err, errInjected) || strings.Contains(err.Error(), publishedNote) {
		t.Fatalf("error = %v, want the fault without a published BACKLOG.md change", err)
	}
	assertLedgersUnchanged(t, dir, before)
}

// Negative: a BACKLOG.md the failed write left unreadable cannot show whether the write
// landed; the store is left holding the change and the error says so, rather than
// restored blind.
func TestCommit_Negative_UnreadableBacklogKeepsStoreChange(t *testing.T) {
	dir := seedLedger(t, true)
	injectWriters(t, ledgerWriters{writeBacklog: func(_ context.Context, update *backlogUpdate) error {
		path := filepath.Join(update.root, state.WorkingDirName, BacklogFile)
		return errors.Join(errInjected, os.WriteFile(path, []byte("# Project Backlog\x00\n"), 0o600))
	}})
	_, err := CreateMilestone(context.Background(), dir, "v2", "", nil)
	if !errors.Is(err, errInjected) || !strings.Contains(err.Error(), "could not be read back") ||
		strings.Contains(err.Error(), restoredNote) {
		t.Fatalf("error = %v, want the fault with the unreadable BACKLOG.md reported", err)
	}
	if rows := storeRows(t, dir); len(rows) != 2 || rows[1].Title != "v2" {
		t.Fatalf("the store was restored without evidence the write failed: %+v", rows)
	}
}

// Boundary: a store another writer replaced after this commit wrote it is left as found;
// the restore never discards that writer's update.
func TestCommit_Boundary_ConcurrentStoreWriteIsLeftAsFound(t *testing.T) {
	dir := seedLedger(t, true)
	other := storeJSON(t, &MilestoneStore{Milestones: []Milestone{{Number: 9, Title: "other writer", State: StateOpen}}})
	injectWriters(t, ledgerWriters{writeBacklog: func(_ context.Context, update *backlogUpdate) error {
		if err := writeStoreFile(update.root, other); err != nil {
			return err
		}
		return errInjected
	}})
	_, err := CreateMilestone(context.Background(), dir, "mine", "", nil)
	if !errors.Is(err, errInjected) || !strings.Contains(err.Error(), "left as found") {
		t.Fatalf("error = %v, want the fault with the concurrent store left as found", err)
	}
	assertStoreBytes(t, dir, other)
}

// Negative: BACKLOG.md changed by another writer between render and write is refused by the
// compare-and-swap writer; the store is restored and the other writer's bytes survive. The
// retry renders from the new BACKLOG.md and keeps both.
func TestCommit_Negative_ConcurrentBacklogEditRestoresStore(t *testing.T) {
	ctx := context.Background()
	dir := seedLedger(t, true)
	before := readLedgers(t, dir)
	appended := before.backlog + "\n### Discharged Tasks\n- [x] concurrent\n"
	reset := injectWriters(t, ledgerWriters{writeBacklog: func(ctx context.Context, update *backlogUpdate) error {
		path := filepath.Join(update.root, state.WorkingDirName, BacklogFile)
		if err := os.WriteFile(path, []byte(appended), 0o600); err != nil {
			return err
		}
		return writeBacklog(ctx, update)
	}})
	_, err := CreateMilestone(ctx, dir, "v2", "", nil)
	if err == nil || !strings.Contains(err.Error(), "changed") || !strings.Contains(err.Error(), restoredNote) {
		t.Fatalf("error = %v, want the refused stale render with the store restored", err)
	}
	assertStoreBytes(t, dir, before.store)
	if got := readBacklog(t, dir); got != appended {
		t.Fatalf("the other writer's BACKLOG.md bytes were lost:\n%s", got)
	}
	reset()
	if _, err := CreateMilestone(ctx, dir, "v2", "", nil); err != nil {
		t.Fatalf("retry after the concurrent edit: %v", err)
	}
	assertSingleRow(t, dir, "v2", 2)
	if !strings.Contains(readBacklog(t, dir), "- [x] concurrent") {
		t.Fatal("the retry dropped the other writer's BACKLOG.md block")
	}
}

// Negative: a store the restore cannot read (a link planted where the store was) is
// reported and left untouched rather than replaced blind.
func TestCommit_Negative_UnreadableStoreIsNotRestoredBlind(t *testing.T) {
	scratch := t.TempDir()
	target := filepath.Join(scratch, "elsewhere.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(scratch, "probe")); err != nil {
		t.Skipf("symlinks unsupported on this platform (Windows without the privilege): %v", err)
	}
	dir := seedLedger(t, true)
	storePath := filepath.Join(dir, state.WorkingDirName, MilestonesFile)
	injectWriters(t, ledgerWriters{writeBacklog: func(context.Context, *backlogUpdate) error {
		return errors.Join(errInjected, os.Remove(storePath), os.Symlink(target, storePath))
	}})
	_, err := CreateMilestone(context.Background(), dir, "v2", "", nil)
	if !errors.Is(err, errInjected) || !strings.Contains(err.Error(), "could not be checked") {
		t.Fatalf("error = %v, want the fault with the unreadable store reported", err)
	}
	if data, readErr := os.ReadFile(target); readErr != nil || string(data) != "{}" {
		t.Fatalf("the restore wrote through the planted link: %q, %v", data, readErr)
	}
}

// Boundary: an absent store and a present empty one are different snapshots, and a
// snapshot holds only its exact bytes.
func TestStoreSnapshot_Boundary_AbsenceAndBytes(t *testing.T) {
	absent := storeSnapshot{}
	empty := storeSnapshot{data: []byte{}, exists: true}
	full := storeSnapshot{data: []byte("{}"), exists: true}
	if absent.same(empty) || empty.same(absent) || !empty.same(storeSnapshot{exists: true}) {
		t.Fatal("absence and an empty store must differ, and two empty stores must agree")
	}
	if absent.holds(nil) || !empty.holds(nil) || !full.holds([]byte("{}")) || full.holds([]byte("{} ")) {
		t.Fatal("holds must require existence and exact bytes")
	}
	cause := errors.New("cause")
	err := restoreStore(context.Background(), seedLedger(t, false), absent, []byte("{}"), cause)
	if !errors.Is(err, cause) || err.Error() != cause.Error() {
		t.Fatalf("a store still at its prior state must return the cause unchanged: %v", err)
	}
}
