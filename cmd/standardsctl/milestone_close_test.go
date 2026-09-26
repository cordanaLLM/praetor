package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cordanaLLM/praetor/internal/milestone"
	"github.com/cordanaLLM/praetor/internal/state"
)

// milestoneCloseFixture writes a store holding one milestone bound to remote #7 and one
// local-only milestone, and returns a stub forge that counts its requests.
func milestoneCloseFixture(t *testing.T) (string, *httptest.Server, *atomic.Int32) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	dir := t.TempDir()
	store := milestone.MilestoneStore{Milestones: []milestone.Milestone{
		{Number: 1, Title: "v2", RemoteNumber: 7, State: milestone.StateOpen},
		{Number: 2, Title: "local", State: milestone.StateOpen},
	}}
	data, err := json.Marshal(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, state.WorkingDirName), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, state.WorkingDirName, milestone.MilestonesFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/repos/owner/repo/milestones/7" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(milestone.RemoteMilestone{Number: 7, Title: "v2", State: milestone.StateClosed}); err != nil {
			t.Errorf("encode stub milestone: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return dir, srv, &hits
}

func milestoneCloseArgs(selector, dir, endpoint string, publish bool) []string {
	args := []string{"close", selector, "--dir=" + dir, "--owner=owner", "--repo=repo", "--token=test-token", "--endpoint=" + endpoint}
	if publish {
		args = append(args, "--publish")
	}
	return args
}

// Positive: close --publish PATCHes and reads back the bound remote milestone.
func TestMilestoneClosePublishReachesForge(t *testing.T) {
	dir, srv, hits := milestoneCloseFixture(t)
	if err := runMilestone(milestoneCloseArgs("1", dir, srv.URL, true)); err != nil {
		t.Fatalf("milestone close --publish: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected PATCH and readback, got %d requests", hits.Load())
	}
	items, err := milestone.ListMilestones(t.Context(), dir, "closed")
	if err != nil || len(items) != 1 || items[0].PendingRemoteClose {
		t.Fatalf("published close not recorded: %+v, %v", items, err)
	}
}

// Negative: publishing the close of a local-only milestone fails after the local close and
// never reaches the forge.
func TestMilestoneClosePublishRefusesUnbound(t *testing.T) {
	dir, srv, hits := milestoneCloseFixture(t)
	err := runMilestone(milestoneCloseArgs("2", dir, srv.URL, true))
	if err == nil || !strings.Contains(err.Error(), "closed locally, but the GitHub close failed") {
		t.Fatalf("expected the unpublished milestone to be reported: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("a local-only milestone reached the forge: %d requests", hits.Load())
	}
}

// Boundary: without --publish the close stays local and pending; no request is made even
// though forge flags are present.
func TestMilestoneCloseWithoutPublishStaysLocal(t *testing.T) {
	dir, srv, hits := milestoneCloseFixture(t)
	if err := runMilestone(milestoneCloseArgs("1", dir, srv.URL, false)); err != nil {
		t.Fatalf("milestone close: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("a local close reached the forge: %d requests", hits.Load())
	}
	items, err := milestone.ListMilestones(t.Context(), dir, "closed")
	if err != nil || len(items) != 1 || !items[0].PendingRemoteClose {
		t.Fatalf("local close must stay pending: %+v, %v", items, err)
	}
}
