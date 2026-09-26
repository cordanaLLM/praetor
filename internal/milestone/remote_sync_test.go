package milestone

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cordanaLLM/praetor/internal/state"
)

// fakeForge is a stub GitHub milestone API holding one mutable remote milestone listing.
type fakeForge struct {
	mu       sync.Mutex
	remotes  map[int]RemoteMilestone
	patches  []string
	ignoreWr bool
}

func newFakeForge(t *testing.T, remotes ...RemoteMilestone) (*fakeForge, *httptest.Server) {
	t.Helper()
	forge := &fakeForge{remotes: make(map[int]RemoteMilestone, len(remotes))}
	for _, rm := range remotes {
		forge.remotes[rm.Number] = rm
	}
	srv := httptest.NewServer(http.HandlerFunc(forge.serve(t)))
	t.Cleanup(srv.Close)
	return forge, srv
}

func (f *fakeForge) serve(t *testing.T) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var out any
		switch {
		case r.URL.Path == "/repos/owner/repo/milestones":
			list := make([]RemoteMilestone, 0, len(f.remotes))
			for number := 1; number <= 100; number++ {
				if rm, ok := f.remotes[number]; ok {
					list = append(list, rm)
				}
			}
			out = list
		case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/milestones/"):
			out = f.single(t, r)
		default:
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(out); err != nil {
			t.Errorf("encode fake forge response: %v", err)
		}
	}
}

// single serves GET and PATCH of one milestone, recording every PATCH body.
func (f *fakeForge) single(t *testing.T, r *http.Request) RemoteMilestone {
	number, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/repos/owner/repo/milestones/"))
	if err != nil {
		t.Errorf("fake forge milestone path %q: %v", r.URL.Path, err)
	}
	rm := f.remotes[number]
	if r.Method == http.MethodPatch {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read patch body: %v", err)
		}
		f.patches = append(f.patches, string(body))
		var patch map[string]string
		if err := json.Unmarshal(body, &patch); err != nil {
			t.Errorf("decode patch body: %v", err)
		}
		if !f.ignoreWr {
			rm.State = patch["state"]
			f.remotes[number] = rm
		}
	}
	return rm
}

func storeRows(t *testing.T, dir string) []Milestone {
	t.Helper()
	rows, err := ListMilestones(context.Background(), dir, "all")
	if err != nil {
		t.Fatalf("list milestones: %v", err)
	}
	return rows
}

// Positive: a remote rename updates the row bound to that remote number instead of adding
// a duplicate, and a title swap between two remotes keeps both bindings (BUG-795).
func TestMerge_RemoteNumberBindsRenamesAndSwaps(t *testing.T) {
	store := &MilestoneStore{Milestones: []Milestone{
		{Number: 1, Title: "alpha", RemoteNumber: 11, State: StateOpen},
		{Number: 2, Title: "beta", RemoteNumber: 12, State: StateOpen},
		{Number: 3, Title: "old name", RemoteNumber: 13, State: StateOpen},
	}}
	result, err := mergeRemoteMilestones(store, []RemoteMilestone{
		{Number: 11, Title: "beta", State: StateOpen},
		{Number: 12, Title: "alpha", State: StateOpen},
		{Number: 13, Title: "new name", State: StateOpen},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Milestones) != 3 {
		t.Fatalf("a rename or swap duplicated a row: %+v", store.Milestones)
	}
	want := map[int]struct {
		title  string
		remote int
	}{1: {"beta", 11}, 2: {"alpha", 12}, 3: {"new name", 13}}
	for _, m := range store.Milestones {
		if got := want[m.Number]; m.Title != got.title || m.RemoteNumber != got.remote {
			t.Errorf("local #%d = %q bound to #%d, want %q bound to #%d", m.Number, m.Title, m.RemoteNumber, got.title, got.remote)
		}
	}
	if len(result.StaleBindings) != 0 || len(result.PendingCloses) != 0 {
		t.Errorf("a clean merge reported drift: %+v", result)
	}
}

// Negative: rows the old title-keyed merge duplicated on a rename share one remote number.
// The row carrying the remote's current title keeps the binding; the stale row is unbound
// and reported, never deleted, and no third row is added.
func TestMerge_RepairsStaleDuplicateBindings(t *testing.T) {
	store := &MilestoneStore{Milestones: []Milestone{
		{Number: 1, Title: "v1 draft", RemoteNumber: 5, State: StateOpen},
		{Number: 2, Title: "v1 final", RemoteNumber: 5, State: StateOpen},
	}}
	result, err := mergeRemoteMilestones(store, []RemoteMilestone{{Number: 5, Title: "v1 final", State: StateOpen}})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Milestones) != 2 {
		t.Fatalf("the repair must keep both rows and add none: %+v", store.Milestones)
	}
	if store.Milestones[0].RemoteNumber != 0 || store.Milestones[0].Title != "v1 draft" {
		t.Errorf("stale row must be unbound with its data kept: %+v", store.Milestones[0])
	}
	if store.Milestones[1].RemoteNumber != 5 {
		t.Errorf("row matching the remote title must keep the binding: %+v", store.Milestones[1])
	}
	want := StaleBinding{Local: 1, Remote: 5, Kept: 2}
	if len(result.StaleBindings) != 1 || result.StaleBindings[0] != want {
		t.Errorf("stale binding not reported: %+v", result.StaleBindings)
	}
}

// Boundary: only an unbound row is matched by title. A remote with an unknown number whose
// title equals a bound row's title becomes a new row rather than stealing the binding, and
// a swap at exact capacity still fits because it appends nothing.
func TestMerge_TitleFallbackOnlyForUnboundRows(t *testing.T) {
	store := &MilestoneStore{Milestones: []Milestone{
		{Number: 1, Title: "shared", RemoteNumber: 3, State: StateOpen},
		{Number: 2, Title: "local", State: StateOpen},
	}}
	if _, err := mergeRemoteMilestones(store, []RemoteMilestone{
		{Number: 3, Title: "shared", State: StateOpen},
		{Number: 9, Title: "SHARED", State: StateOpen},
		{Number: 8, Title: "Local", State: StateOpen},
	}); err != nil {
		t.Fatal(err)
	}
	if len(store.Milestones) != 3 || store.Milestones[0].RemoteNumber != 3 {
		t.Fatalf("an unknown remote number rebound a bound row: %+v", store.Milestones)
	}
	if store.Milestones[1].RemoteNumber != 8 || store.Milestones[2].RemoteNumber != 9 || store.Milestones[2].Number != 3 {
		t.Fatalf("unbound title match must bind, unknown remote must append: %+v", store.Milestones)
	}

	full := milestoneLimitFixture(MaxMilestonesLimit)
	full.Milestones[0].RemoteNumber, full.Milestones[1].RemoteNumber = 1, 2
	swap := []RemoteMilestone{{Number: 1, Title: "milestone-2"}, {Number: 2, Title: "milestone-1"}}
	if _, err := mergeRemoteMilestones(full, swap); err != nil || len(full.Milestones) != MaxMilestonesLimit {
		t.Fatalf("a swap at exact capacity must merge in place: %v", err)
	}
}

// Positive: close --publish PATCHes the bound remote to closed, reads it back and clears
// the pending flag; the next sync keeps the milestone closed (BUG-796).
func TestPublishClose_PatchesAndReadsBack(t *testing.T) {
	ctx := context.Background()
	isolateForge(t)
	dir := setupTestDir(t)
	forge, srv := newFakeForge(t, RemoteMilestone{Number: 7, Title: "v2", State: StateOpen, OpenIssues: 1, ClosedIssues: 3})
	writeStoreFixture(t, dir, &MilestoneStore{Milestones: []Milestone{{Number: 1, Title: "v2", RemoteNumber: 7, State: StateOpen}}})

	m, err := CloseMilestone(ctx, dir, "1")
	if err != nil || !m.PendingRemoteClose {
		t.Fatalf("a local close must be pending until the forge confirms it: %+v, %v", m, err)
	}
	if err := PublishClose(ctx, dir, "owner", "repo", "test-token", srv.URL, m); err != nil {
		t.Fatalf("PublishClose: %v", err)
	}
	if len(forge.patches) != 1 || !strings.Contains(forge.patches[0], `"state":"closed"`) {
		t.Fatalf("expected one state=closed PATCH, got %q", forge.patches)
	}
	rows := storeRows(t, dir)
	if rows[0].PendingRemoteClose || rows[0].State != StateClosed || rows[0].ClosedIssues != 3 {
		t.Fatalf("confirmed close not persisted: %+v", rows[0])
	}
	result, err := SyncWithGitHub(ctx, dir, "owner", "repo", "test-token", srv.URL)
	if err != nil || len(result.PendingCloses) != 0 || result.Milestones[0].State != StateClosed {
		t.Fatalf("sync after a published close must stay closed without drift: %+v, %v", result, err)
	}
}

// Negative: a forge that reports the milestone still open after the PATCH leaves the close
// pending, and an unpublished milestone is refused before any request.
func TestPublishClose_RefusesUnconfirmedAndUnbound(t *testing.T) {
	ctx := context.Background()
	isolateForge(t)
	dir := setupTestDir(t)
	forge, srv := newFakeForge(t, RemoteMilestone{Number: 7, Title: "v2", State: StateOpen})
	forge.ignoreWr = true
	writeStoreFixture(t, dir, &MilestoneStore{Milestones: []Milestone{
		{Number: 1, Title: "v2", RemoteNumber: 7, State: StateOpen},
		{Number: 2, Title: "local only", State: StateOpen},
	}})

	m, err := CloseMilestone(ctx, dir, "1")
	if err != nil {
		t.Fatal(err)
	}
	if err := PublishClose(ctx, dir, "owner", "repo", "test-token", srv.URL, m); err == nil || !strings.Contains(err.Error(), "stays pending") {
		t.Fatalf("an unconfirmed close must be reported: %v", err)
	}
	if rows := storeRows(t, dir); !rows[0].PendingRemoteClose {
		t.Fatalf("an unconfirmed close must stay pending: %+v", rows[0])
	}

	local, err := CloseMilestone(ctx, dir, "2")
	if err != nil {
		t.Fatal(err)
	}
	before := len(forge.patches)
	if err := PublishClose(ctx, dir, "owner", "repo", "test-token", srv.URL, local); err == nil {
		t.Fatal("an unpublished milestone has no remote to close")
	}
	if len(forge.patches) != before {
		t.Fatal("an unpublished milestone reached the forge")
	}
	if err := PublishClose(ctx, dir, "owner", "repo", "test-token", srv.URL, nil); err == nil {
		t.Fatal("a nil milestone must be refused")
	}
}

// Boundary: a sync no longer reopens a locally closed milestone the forge still reports
// open. It keeps the close, reports it once, and clears it once the forge closes too.
func TestSync_KeepsPendingLocalClose(t *testing.T) {
	ctx := context.Background()
	isolateForge(t)
	dir := setupTestDir(t)
	forge, srv := newFakeForge(t, RemoteMilestone{Number: 7, Title: "v2", State: StateOpen})
	writeStoreFixture(t, dir, &MilestoneStore{Milestones: []Milestone{{Number: 1, Title: "v2", RemoteNumber: 7, State: StateOpen}}})
	if _, err := CloseMilestone(ctx, dir, "1"); err != nil {
		t.Fatal(err)
	}

	result, err := SyncWithGitHub(ctx, dir, "owner", "repo", "test-token", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if result.Milestones[0].State != StateClosed || len(result.PendingCloses) != 1 || result.PendingCloses[0] != 1 {
		t.Fatalf("sync reverted or hid the pending local close: %+v", result)
	}
	if !strings.Contains(readBacklog(t, dir), "Closed") {
		t.Fatal("BACKLOG.md must keep rendering the pending close as closed")
	}

	forge.mu.Lock()
	forge.remotes[7] = RemoteMilestone{Number: 7, Title: "v2", State: StateClosed}
	forge.mu.Unlock()
	result, err = SyncWithGitHub(ctx, dir, "owner", "repo", "test-token", srv.URL)
	if err != nil || len(result.PendingCloses) != 0 || result.Milestones[0].PendingRemoteClose {
		t.Fatalf("a close the forge confirmed must clear the pending flag: %+v, %v", result, err)
	}

	// Closing an already closed milestone does not mark it pending again.
	m, err := CloseMilestone(ctx, dir, "1")
	if err != nil || m.PendingRemoteClose {
		t.Fatalf("re-closing a confirmed milestone must not flag it pending: %+v, %v", m, err)
	}
}

// Negative: BACKLOG.md is written with compare-and-swap. An archive block appended by the
// state ledger between render and write fails the milestone write instead of being
// overwritten (BUG-867).
func TestWriteBacklog_ConcurrentArchiveIsNotLost(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)
	store := &MilestoneStore{Milestones: []Milestone{{Number: 1, Title: "v1", State: StateOpen}}}
	update, err := prepareBacklog(ctx, dir, store)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, state.WorkingDirName, BacklogFile)
	archived := readBacklog(t, dir) + "\n### Discharged Tasks [now, commit `abc`]\n- [x] concurrent\n"
	if err := os.WriteFile(path, []byte(archived), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeBacklog(ctx, update); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("a stale render must be refused: %v", err)
	}
	if got := readBacklog(t, dir); got != archived {
		t.Fatalf("the concurrent archive write was lost:\n%s", got)
	}

	// Re-rendering from the new snapshot keeps the archive and adds the block.
	if err := SyncToBacklog(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if got := readBacklog(t, dir); !strings.Contains(got, "- [x] concurrent") || !strings.Contains(got, milestoneSectionStart) {
		t.Fatalf("re-render dropped the archive or the block:\n%s", got)
	}
}

// Boundary: a missing BACKLOG.md is created, but one created by another writer after the
// render is not replaced.
func TestWriteBacklog_AbsentSnapshotBoundary(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)
	path := filepath.Join(dir, state.WorkingDirName, BacklogFile)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	update, err := prepareBacklog(ctx, dir, &MilestoneStore{})
	if err != nil || update.exists {
		t.Fatalf("absent BACKLOG.md must render from the default: %+v, %v", update, err)
	}
	if err := os.WriteFile(path, []byte("# Someone else\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeBacklog(ctx, update); err == nil {
		t.Fatal("a BACKLOG.md created after the render must not be replaced")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := writeBacklog(ctx, update); err != nil {
		t.Fatalf("an absent BACKLOG.md must be created: %v", err)
	}
	if !strings.Contains(readBacklog(t, dir), milestoneSectionStart) {
		t.Fatal("created BACKLOG.md lacks the milestone block")
	}
}
