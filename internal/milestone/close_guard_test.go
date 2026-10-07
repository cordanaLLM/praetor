package milestone

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/state"
)

// Positive: a milestone whose issues are all closed closes, with progress from the counts;
// a forced close of one with open issues records the real, partial progress.
func TestCloseMilestoneWith_Positive_ProgressFromCounts(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)
	writeStoreFixture(t, dir, &MilestoneStore{Milestones: []Milestone{
		{Number: 1, Title: "done", State: StateOpen, ClosedIssues: 4},
		{Number: 2, Title: "busy", State: StateOpen, OpenIssues: 1, ClosedIssues: 3},
	}})

	done, err := CloseMilestone(ctx, dir, "1")
	if err != nil || done.State != StateClosed || done.Progress != 100 {
		t.Fatalf("a finished milestone must close at 100%%: %+v, %v", done, err)
	}
	forced, err := CloseMilestoneWith(ctx, dir, "2", CloseOptions{Force: true})
	if err != nil || forced.State != StateClosed || forced.Progress != 75 || !forced.PendingRemoteClose {
		t.Fatalf("a forced close must record 75%% and stay pending: %+v, %v", forced, err)
	}
	if rows := storeRows(t, dir); rows[1].Progress != 75 || rows[1].OpenIssues != 1 {
		t.Fatalf("forced close not persisted from the counts: %+v", rows[1])
	}
}

// Negative: open issues in the cached store or on the forge refuse the close and leave the
// store as it was; the forge's counts win over the cached ones.
func TestCloseMilestoneWith_Negative_RefusesOpenIssues(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)
	before := writeStoreFixture(t, dir, &MilestoneStore{Milestones: []Milestone{
		{Number: 1, Title: "cached", State: StateOpen, OpenIssues: 2, ClosedIssues: 1},
		{Number: 2, Title: "stale", State: StateOpen, ClosedIssues: 5, RemoteNumber: 9},
	}})

	_, err := CloseMilestone(ctx, dir, "1")
	if !errors.Is(err, ErrOpenIssues) || !strings.Contains(err.Error(), "cached store") {
		t.Fatalf("cached open issues must refuse the close: %v", err)
	}
	remote := &RemoteMilestone{Number: 9, State: StateOpen, OpenIssues: 1, ClosedIssues: 5}
	_, err = CloseMilestoneWith(ctx, dir, "2", CloseOptions{Remote: remote})
	if !errors.Is(err, ErrOpenIssues) || !strings.Contains(err.Error(), "the forge") {
		t.Fatalf("an open issue on the forge must refuse the close the cache allows: %v", err)
	}
	assertStoreBytes(t, dir, before)
}

// Boundary: zero open issues on the forge closes a milestone the stale cache calls busy,
// and an empty milestone closes with zero progress.
func TestCloseMilestoneWith_Boundary_ForgeCountsAndEmpty(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)
	writeStoreFixture(t, dir, &MilestoneStore{Milestones: []Milestone{
		{Number: 1, Title: "stale", State: StateOpen, OpenIssues: 3, RemoteNumber: 4},
		{Number: 2, Title: "empty", State: StateOpen},
	}})

	m, err := CloseMilestoneWith(ctx, dir, "1", CloseOptions{Remote: &RemoteMilestone{Number: 4, State: StateOpen, ClosedIssues: 3}})
	if err != nil || m.OpenIssues != 0 || m.ClosedIssues != 3 || m.Progress != 100 {
		t.Fatalf("forge counts must replace the cache: %+v, %v", m, err)
	}
	empty, err := CloseMilestone(ctx, dir, "2")
	if err != nil || empty.Progress != 0 {
		t.Fatalf("an empty milestone closes with no progress: %+v, %v", empty, err)
	}
}

// Positive: the active milestone is the open, published milestone due first.
func TestActiveMilestone_Positive_DueFirst(t *testing.T) {
	dir := setupTestDir(t)
	early := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	late := early.AddDate(0, 1, 0)
	writeStoreFixture(t, dir, &MilestoneStore{Milestones: []Milestone{
		{Number: 1, Title: "undated", State: StateOpen, RemoteNumber: 1},
		{Number: 2, Title: "late", State: StateOpen, RemoteNumber: 2, DueOn: &late},
		{Number: 3, Title: "early", State: StateOpen, RemoteNumber: 3, DueOn: &early},
		{Number: 4, Title: "closed", State: StateClosed, RemoteNumber: 4, DueOn: &early},
	}})
	active, found, err := ActiveMilestone(context.Background(), dir)
	if err != nil || !found || active.Title != "early" {
		t.Fatalf("active = %+v found=%t err=%v, want early", active, found, err)
	}
}

// Negative: local-only and closed milestones are never active, and an unreadable store is
// an error rather than "no milestone".
func TestActiveMilestone_Negative_LocalOnlyClosedAndCorrupt(t *testing.T) {
	dir := setupTestDir(t)
	writeStoreFixture(t, dir, &MilestoneStore{Milestones: []Milestone{
		{Number: 1, Title: "local", State: StateOpen},
		{Number: 2, Title: "closed", State: StateClosed, RemoteNumber: 2},
	}})
	if active, found, err := ActiveMilestone(context.Background(), dir); err != nil || found {
		t.Fatalf("no milestone may be active: %+v found=%t err=%v", active, found, err)
	}
	corrupt := setupTestDir(t)
	path := filepath.Join(corrupt, state.WorkingDirName, MilestonesFile)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ActiveMilestone(context.Background(), corrupt); err == nil {
		t.Fatal("a corrupt store must be an error")
	}
}

// Boundary: an absent store has no active milestone, and equal due dates fall back to the
// lowest local number, undated milestones last.
func TestActiveMilestone_Boundary_AbsentStoreAndTies(t *testing.T) {
	if _, found, err := ActiveMilestone(context.Background(), t.TempDir()); err != nil || found {
		t.Fatalf("absent store: found=%t err=%v", found, err)
	}
	dir := setupTestDir(t)
	due := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	writeStoreFixture(t, dir, &MilestoneStore{Milestones: []Milestone{
		{Number: 5, Title: "five", State: StateOpen, RemoteNumber: 5, DueOn: &due},
		{Number: 3, Title: "three", State: StateOpen, RemoteNumber: 3, DueOn: &due},
		{Number: 1, Title: "undated", State: StateOpen, RemoteNumber: 1},
	}})
	if active, _, err := ActiveMilestone(context.Background(), dir); err != nil || active.Number != 3 {
		t.Fatalf("tie must pick local #3, got %+v (%v)", active, err)
	}
}

// Positive, negative and boundary of the remote reads and the remote close the planning
// sync and milestone close share.
func TestRemoteMilestoneReadsAndClose_3D(t *testing.T) {
	ctx := context.Background()
	isolateForge(t)
	forge, srv := newFakeForge(t,
		RemoteMilestone{Number: 1, Title: "done", State: StateOpen, ClosedIssues: 2},
		RemoteMilestone{Number: 2, Title: "busy", State: StateOpen, OpenIssues: 1})

	all, err := FetchRemoteMilestones(ctx, "owner", "repo", "test-token", srv.URL)
	if err != nil || len(all) != 2 {
		t.Fatalf("FetchRemoteMilestones = %+v, %v", all, err)
	}
	one, err := FetchRemoteMilestone(ctx, "owner", "repo", "test-token", srv.URL, 2)
	if err != nil || one.OpenIssues != 1 {
		t.Fatalf("FetchRemoteMilestone = %+v, %v", one, err)
	}
	closed, err := CloseRemoteMilestone(ctx, "owner", "repo", "test-token", srv.URL, 1)
	if err != nil || closed.State != StateClosed || len(forge.patches) != 1 {
		t.Fatalf("CloseRemoteMilestone = %+v, %v (patches %q)", closed, err, forge.patches)
	}

	forge.ignoreWr = true
	if _, err := CloseRemoteMilestone(ctx, "owner", "repo", "test-token", srv.URL, 2); err == nil {
		t.Fatal("a close the forge does not confirm must fail")
	}
	before := len(forge.patches)
	for _, number := range []int{0, -3} {
		if _, err := FetchRemoteMilestone(ctx, "owner", "repo", "test-token", srv.URL, number); err == nil {
			t.Errorf("FetchRemoteMilestone(%d) accepted", number)
		}
		if _, err := CloseRemoteMilestone(ctx, "owner", "repo", "test-token", srv.URL, number); err == nil {
			t.Errorf("CloseRemoteMilestone(%d) accepted", number)
		}
	}
	if _, err := FetchRemoteMilestones(ctx, "owner", "../repo", "test-token", srv.URL); err == nil {
		t.Fatal("an invalid repository identity must be refused")
	}
	if len(forge.patches) != before {
		t.Fatal("an invalid request reached the forge")
	}
}
