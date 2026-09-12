package milestone

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/state"
)

func setupTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	wDir := filepath.Join(dir, state.WorkingDirName)
	if err := os.MkdirAll(wDir, 0o750); err != nil {
		t.Fatalf("failed creating test workingdir: %v", err)
	}
	backlogPath := filepath.Join(wDir, BacklogFile)
	if err := os.WriteFile(backlogPath, []byte("# Project Backlog\n\n## Future Epics\n\n- Epic 1\n"), 0o600); err != nil {
		t.Fatalf("failed initializing BACKLOG.md: %v", err)
	}
	return dir
}

func readBacklog(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, state.WorkingDirName, BacklogFile))
	if err != nil {
		t.Fatalf("read BACKLOG.md failed: %v", err)
	}
	return string(data)
}

// isolateForge points PATH at an empty directory and pins an explicit token so that no
// test can reach a real forge or execute the developer's `gh` binary.
func isolateForge(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("GH_TOKEN", "")
}

// ============================================================================
// 1. POSITIVE
// ============================================================================

func TestMilestone_Positive_Lifecycle(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)

	dueDate := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	m1, err := CreateMilestone(ctx, dir, "v1.0.0-GA", "General availability release", &dueDate)
	if err != nil {
		t.Fatalf("CreateMilestone failed: %v", err)
	}
	if m1.Number != 1 || m1.State != StateOpen || m1.Title != "v1.0.0-GA" {
		t.Errorf("unexpected milestone fields: %+v", m1)
	}

	m2, err := CreateMilestone(ctx, dir, "v1.1.0-Features", "Next iteration", nil)
	if err != nil {
		t.Fatalf("CreateMilestone 2 failed: %v", err)
	}
	if m2.Number != 2 || m2.DueOn != nil {
		t.Errorf("unexpected milestone 2: %+v", m2)
	}

	list, err := ListMilestones(ctx, dir, "open")
	if err != nil {
		t.Fatalf("ListMilestones failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 open milestones, got %d", len(list))
	}

	closed, err := CloseMilestone(ctx, dir, "1")
	if err != nil {
		t.Fatalf("CloseMilestone failed: %v", err)
	}
	if closed.State != StateClosed || closed.Progress != 100.0 {
		t.Errorf("expected closed milestone state, got %+v", closed)
	}

	openList, err := ListMilestones(ctx, dir, "open")
	if err != nil {
		t.Fatalf("ListMilestones after close failed: %v", err)
	}
	if len(openList) != 1 || openList[0].Number != 2 {
		t.Errorf("expected only milestone 2 open, got %+v", openList)
	}

	backlog := readBacklog(t, dir)
	if !strings.Contains(backlog, milestoneHeading) {
		t.Errorf("BACKLOG.md does not contain Active Milestones section: %s", backlog)
	}
	if !strings.Contains(backlog, "v1.0.0-GA") || !strings.Contains(backlog, "v1.1.0-Features") {
		t.Errorf("BACKLOG.md missing milestone titles: %s", backlog)
	}
	if strings.Count(backlog, milestoneSectionStart) != 1 || strings.Count(backlog, milestoneSectionEnd) != 1 {
		t.Errorf("expected exactly one delimited milestone block: %s", backlog)
	}
	if !strings.Contains(backlog, "## Future Epics") {
		t.Errorf("sync dropped an unrelated section: %s", backlog)
	}
}

func TestMilestone_Positive_SyncWithGitHubPaginates(t *testing.T) {
	ctx := context.Background()
	isolateForge(t)
	dir := setupTestDir(t)

	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		page := r.URL.Query().Get("page")
		var batch []RemoteMilestone
		switch page {
		case "1":
			for i := 1; i <= milestonePageSize; i++ {
				batch = append(batch, RemoteMilestone{Number: i, Title: fmt.Sprintf("remote-%d", i), State: StateOpen})
			}
		case "2":
			batch = []RemoteMilestone{{Number: 101, Title: "remote-101", State: StateClosed, ClosedIssues: 2}}
		default:
			batch = []RemoteMilestone{}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(batch); err != nil {
			t.Errorf("encode fake milestones: %v", err)
		}
	}))
	defer srv.Close()

	synced, err := SyncWithGitHub(ctx, dir, "cordanaLLM", "praetor", "test-token", srv.URL)
	if err != nil {
		t.Fatalf("SyncWithGitHub failed: %v", err)
	}
	if pages != 2 {
		t.Errorf("expected the Link-less pagination loop to request 2 pages, got %d", pages)
	}
	if len(synced) != milestonePageSize+1 {
		t.Fatalf("expected %d synchronized milestones, got %d", milestonePageSize+1, len(synced))
	}
	if synced[len(synced)-1].RemoteNumber != 101 {
		t.Errorf("remote number not recorded: %+v", synced[len(synced)-1])
	}
}

func TestMilestone_Positive_PublishPersistsRemoteNumber(t *testing.T) {
	ctx := context.Background()
	isolateForge(t)
	dir := setupTestDir(t)

	m, err := CreateMilestone(ctx, dir, "v2.0", "", nil)
	if err != nil {
		t.Fatalf("CreateMilestone failed: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, werr := w.Write([]byte(`{"number":7,"title":"v2.0","state":"open"}`)); werr != nil {
			t.Errorf("write fake response: %v", werr)
		}
	}))
	defer srv.Close()

	if err := PublishMilestone(ctx, dir, "cordanaLLM", "praetor", "test-token", srv.URL, m); err != nil {
		t.Fatalf("PublishMilestone failed: %v", err)
	}
	if m.RemoteNumber != 7 || m.Number != 1 {
		t.Errorf("expected local #1 / remote #7, got %+v", m)
	}

	stored, err := ListMilestones(ctx, dir, "all")
	if err != nil {
		t.Fatalf("ListMilestones failed: %v", err)
	}
	if len(stored) != 1 || stored[0].RemoteNumber != 7 {
		t.Errorf("remote number was not persisted to the store: %+v", stored)
	}
}

// ============================================================================
// 2. NEGATIVE
// ============================================================================

func TestMilestone_Negative_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)

	if _, err := CreateMilestone(ctx, dir, "", "empty title", nil); err == nil {
		t.Fatalf("expected error for empty title, got nil")
	}

	if _, err := CreateMilestone(ctx, dir, "Duplicate", "first", nil); err != nil {
		t.Fatalf("first creation failed: %v", err)
	}
	if _, err := CreateMilestone(ctx, dir, "duplicate", "second", nil); err == nil {
		t.Fatalf("expected error for duplicate title, got nil")
	}

	if _, err := CloseMilestone(ctx, dir, "999"); err == nil {
		t.Fatalf("expected error closing non-existent milestone, got nil")
	}
	if _, err := CloseMilestone(ctx, dir, "   "); err == nil {
		t.Fatalf("expected error for an empty selector, got nil")
	}

	corruptPath := filepath.Join(dir, state.WorkingDirName, MilestonesFile)
	if err := os.WriteFile(corruptPath, []byte("NOT_JSON"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	if _, err := ListMilestones(ctx, dir, "all"); err == nil {
		t.Fatalf("expected error loading corrupted milestones store, got nil")
	}
}

func TestMilestone_Negative_NumericSelectorNeverMatchesTitle(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)

	if _, err := CreateMilestone(ctx, dir, "v1.0", "", nil); err != nil {
		t.Fatalf("CreateMilestone failed: %v", err)
	}
	if _, err := CreateMilestone(ctx, dir, "v2.0", "", nil); err != nil {
		t.Fatalf("CreateMilestone failed: %v", err)
	}

	// "3" parses as a number and must not fall through to a substring match on "v1.0".
	if _, err := CloseMilestone(ctx, dir, "3"); err == nil {
		t.Fatalf("expected a numeric selector with no matching number to fail, got nil")
	}

	items, err := ListMilestones(ctx, dir, "open")
	if err != nil {
		t.Fatalf("ListMilestones failed: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("a failed selector closed a milestone: %+v", items)
	}

	// An ambiguous textual selector is refused rather than closing the first hit.
	if _, err := CloseMilestone(ctx, dir, "v"); err == nil {
		t.Fatalf("expected an ambiguous selector to be refused, got nil")
	}
}

func TestMilestone_Negative_RemoteFailuresAndSymlinkedLedger(t *testing.T) {
	ctx := context.Background()
	isolateForge(t)
	dir := setupTestDir(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		if _, werr := w.Write([]byte(strings.Repeat("X", 200*1024))); werr != nil {
			t.Errorf("write fake response: %v", werr)
		}
	}))
	defer srv.Close()

	_, err := SyncWithGitHub(ctx, dir, "cordanaLLM", "praetor", "test-token", srv.URL)
	if err == nil {
		t.Fatalf("expected HTTP 403 to be reported, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(err.Error()) > maxErrorBodyBytes+4096 {
		t.Errorf("error body was not bounded: %d bytes", len(err.Error()))
	}

	if _, err := SyncWithGitHub(ctx, dir, "", "praetor", "test-token", srv.URL); err == nil {
		t.Fatalf("expected an empty owner to be rejected, got nil")
	}

	// A store shipped as a symlink must never be written through.
	victim := filepath.Join(t.TempDir(), "victim.json")
	if err := os.WriteFile(victim, []byte("do not touch"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	storePath := filepath.Join(dir, state.WorkingDirName, MilestonesFile)
	if err := os.Symlink(victim, storePath); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	if _, err := CreateMilestone(ctx, dir, "hostile", "", nil); err == nil {
		t.Fatalf("expected a symlinked ledger to be refused, got nil")
	}
	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("read victim: %v", err)
	}
	if string(data) != "do not touch" {
		t.Errorf("write followed the symlink: %q", string(data))
	}
}

func TestMilestone_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := setupTestDir(t)

	if _, err := ListMilestones(ctx, dir, "all"); err == nil {
		t.Fatalf("expected a cancelled context to abort the ledger read, got nil")
	}
	if err := SyncToBacklog(ctx, dir); err == nil {
		t.Fatalf("expected a cancelled context to abort the backlog sync, got nil")
	}
}

// ============================================================================
// 3. BOUNDARY
// ============================================================================

func TestMilestone_Boundary_EmptyAndSelectors(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)

	emptyList, err := ListMilestones(ctx, dir, "all")
	if err != nil {
		t.Fatalf("ListMilestones on empty store failed: %v", err)
	}
	if len(emptyList) != 0 {
		t.Errorf("expected 0 milestones in new store, got %d", len(emptyList))
	}

	emptyMD := RenderMilestonesMarkdown(nil)
	if !strings.Contains(emptyMD, "No tracked milestones") {
		t.Errorf("unexpected empty markdown: %s", emptyMD)
	}

	if _, err := CreateMilestone(ctx, dir, "Security Hardening Q4", "Audit and pentest", nil); err != nil {
		t.Fatalf("CreateMilestone failed: %v", err)
	}
	closed, err := CloseMilestone(ctx, dir, "Hardening")
	if err != nil {
		t.Fatalf("CloseMilestone with substring selector failed: %v", err)
	}
	if closed.Title != "Security Hardening Q4" {
		t.Errorf("expected closed title 'Security Hardening Q4', got '%s'", closed.Title)
	}
}

func TestMilestone_Boundary_SyncPreservesTrailingLedgerBlocks(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)

	backlogPath := filepath.Join(dir, state.WorkingDirName, BacklogFile)
	original := "# Project Backlog\n\n## Active Milestones\n\n| # | Title |\n| :- | :- |\n| 1 | old |\n\n" +
		"### Discharged Tasks [2026-09-01]\n\n- [x] task one\n\n### Discharged Tasks [2026-09-02]\n\n- [x] task two\n"
	if err := os.WriteFile(backlogPath, []byte(original), 0o600); err != nil {
		t.Fatalf("seed BACKLOG.md: %v", err)
	}

	if _, err := CreateMilestone(ctx, dir, "v3.0", "", nil); err != nil {
		t.Fatalf("CreateMilestone failed: %v", err)
	}

	backlog := readBacklog(t, dir)
	if strings.Count(backlog, "### Discharged Tasks") != 2 {
		t.Fatalf("milestone sync destroyed the task-discharge ledger:\n%s", backlog)
	}
	if !strings.Contains(backlog, "- [x] task one") || !strings.Contains(backlog, "- [x] task two") {
		t.Errorf("discharged task entries were lost:\n%s", backlog)
	}
	if strings.Contains(backlog, "| 1 | old |") {
		t.Errorf("the stale milestone table was not replaced:\n%s", backlog)
	}

	// A second sync must not accumulate a second block.
	if _, err := CreateMilestone(ctx, dir, "v3.1", "", nil); err != nil {
		t.Fatalf("second CreateMilestone failed: %v", err)
	}
	backlog = readBacklog(t, dir)
	if strings.Count(backlog, milestoneHeading) != 1 {
		t.Errorf("expected exactly one milestone section after two syncs:\n%s", backlog)
	}
	if strings.Count(backlog, "### Discharged Tasks") != 2 {
		t.Errorf("second sync destroyed the task-discharge ledger:\n%s", backlog)
	}
}

func TestMilestone_Boundary_TitleSanitization(t *testing.T) {
	ctx := context.Background()
	dir := setupTestDir(t)

	if _, err := CreateMilestone(ctx, dir, "v4\n## Notes | col", "", nil); err != nil {
		t.Fatalf("CreateMilestone failed: %v", err)
	}

	backlog := readBacklog(t, dir)
	if strings.Contains(backlog, "\n## Notes") {
		t.Errorf("an embedded heading reached the ledger:\n%s", backlog)
	}
	if !strings.Contains(backlog, `\|`) {
		t.Errorf("a pipe in the title was not escaped:\n%s", backlog)
	}
	if strings.Count(backlog, milestoneHeading) != 1 {
		t.Errorf("expected exactly one milestone heading:\n%s", backlog)
	}
}

func TestMilestone_Boundary_MergeRemoteMilestones(t *testing.T) {
	store := &MilestoneStore{Milestones: []Milestone{
		{Number: 1, Title: "v1.0", State: StateOpen},
		{Number: 2, Title: "local-only", State: StateOpen},
	}}

	mergeRemoteMilestones(store, []RemoteMilestone{
		{Number: 7, Title: "v1.0", State: StateClosed, ClosedIssues: 3, OpenIssues: 1},
		{Number: 2, Title: "remote-only", State: StateOpen},
	})

	if len(store.Milestones) != 3 {
		t.Fatalf("expected 3 milestones after merge, got %d", len(store.Milestones))
	}
	if store.Milestones[0].Number != 1 || store.Milestones[0].RemoteNumber != 7 {
		t.Errorf("title match must keep the local number and record the remote one: %+v", store.Milestones[0])
	}
	if store.Milestones[0].Progress < 74.9 || store.Milestones[0].Progress > 75.1 {
		t.Errorf("unexpected progress: %+v", store.Milestones[0])
	}
	if store.Milestones[2].Number != 3 || store.Milestones[2].RemoteNumber != 2 {
		t.Errorf("remote-only milestone must get a fresh local number: %+v", store.Milestones[2])
	}

	seen := make(map[int]bool, len(store.Milestones))
	for _, m := range store.Milestones {
		if seen[m.Number] {
			t.Fatalf("duplicate local milestone number %d after merge", m.Number)
		}
		seen[m.Number] = true
	}

	// Merging nothing changes nothing.
	before := len(store.Milestones)
	mergeRemoteMilestones(store, nil)
	if len(store.Milestones) != before {
		t.Errorf("merging an empty remote list changed the store")
	}
}

func TestMilestone_Boundary_BacklogSectionHelpers(t *testing.T) {
	kept := dropMarkedBlock([]string{"a", milestoneSectionStart, "x", milestoneSectionEnd, "b"})
	if strings.Join(kept, "|") != "a|b" {
		t.Errorf("unexpected marker strip result: %v", kept)
	}

	kept = dropLegacySection([]string{"# Top", milestoneHeading, "| t |", "### Later", "- [x] keep"})
	if strings.Join(kept, "|") != "# Top|### Later|- [x] keep" {
		t.Errorf("unexpected legacy strip result: %v", kept)
	}

	if got := dropMarkedBlock(nil); len(got) != 0 {
		t.Errorf("expected empty result for empty input, got %v", got)
	}
	if got := dropLegacySection([]string{""}); len(got) != 1 {
		t.Errorf("expected a single blank line to survive, got %v", got)
	}
}
