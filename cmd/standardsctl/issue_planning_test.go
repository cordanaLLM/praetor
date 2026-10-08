package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// planningForgeServer is a stateful stand-in for one GitHub repository's issues and
// milestones. It applies every PATCH and logs it, so a test reads back what the planning
// sync wrote.
type planningForgeServer struct {
	mu         sync.Mutex
	issues     map[int]map[string]any
	milestones map[int]map[string]any
	writes     []string
}

func newPlanningForgeServer(t *testing.T) (*httptest.Server, *planningForgeServer) {
	t.Helper()
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	state := &planningForgeServer{
		issues: map[int]map[string]any{
			1: {"number": 1, "title": "Epic", "state": "open", "body": "- [ ] #2\n- [ ] #3\n- [x] #4",
				"labels": []map[string]string{{"name": "epic"}}},
			2: {"number": 2, "title": "closed child", "state": "closed"},
			3: {"number": 3, "title": "closed child", "state": "closed"},
			4: {"number": 4, "title": "open child", "state": "open"},
			5: {"number": 5, "title": "Tracking", "state": "open", "body": "- [ ] #2", "labels": []map[string]string{{"name": "tracking"}}},
		},
		milestones: map[int]map[string]any{
			1: {"number": 1, "title": "Done", "state": "open", "open_issues": 0, "closed_issues": 2},
			2: {"number": 2, "title": "Busy", "state": "open", "open_issues": 1, "closed_issues": 2},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(state.serve(t)))
	t.Cleanup(srv.Close)
	return srv, state
}

func (s *planningForgeServer) serve(t *testing.T) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		kind, number := planningPath(r.URL.Path)
		table := s.issues
		if kind == "milestones" {
			table = s.milestones
		}
		switch {
		case kind == "" || (number != 0 && table[number] == nil):
			http.NotFound(w, r)
		case number == 0:
			writeInventoryResponse(t, w, sortedRows(table))
		case r.Method == http.MethodPatch:
			s.patch(t, r, kind, table[number])
			writeInventoryResponse(t, w, table[number])
		default:
			writeInventoryResponse(t, w, table[number])
		}
	}
}

// planningPath splits /repos/acme/widgets/<kind>[/<number>].
func planningPath(path string) (string, int) {
	rest, ok := strings.CutPrefix(path, "/repos/acme/widgets/")
	if !ok {
		return "", 0
	}
	kind, num, _ := strings.Cut(rest, "/")
	if kind != "issues" && kind != "milestones" {
		return "", 0
	}
	number, err := strconv.Atoi(num)
	if num != "" && err != nil {
		return "", 0
	}
	return kind, number
}

func (s *planningForgeServer) patch(t *testing.T, r *http.Request, kind string, row map[string]any) {
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Errorf("decode patch: %v", err)
	}
	keys := make([]string, 0, len(payload))
	for key, value := range payload {
		row[key] = value
		keys = append(keys, key)
	}
	s.writes = append(s.writes, fmt.Sprintf("%s %v %v", kind, row["number"], keys))
}

func sortedRows(table map[int]map[string]any) []map[string]any {
	rows := make([]map[string]any, 0, len(table))
	for number := 1; number <= 10; number++ {
		if row, ok := table[number]; ok {
			rows = append(rows, row)
		}
	}
	return rows
}

func reconcileArgs(srv *httptest.Server, extra ...string) []string {
	return append([]string{"reconcile", "--repos=acme/widgets", "--token=fixture", "--endpoint=" + srv.URL}, extra...)
}

// Positive: --apply ticks exactly the closed children, closes the tracking parent whose
// children are all closed and the milestone without open issues, and logs every write.
func TestIssueReconcilePlanning_Positive_ApplyWritesAndLogs(t *testing.T) {
	srv, state := newPlanningForgeServer(t)
	out, err := captureStdout(t, func() error { return dispatchCommand("issue", reconcileArgs(srv, "--apply")) })
	if err != nil {
		t.Fatalf("issue reconcile --apply: %v\n%s", err, out)
	}
	if got := state.issues[1]["body"]; got != "- [x] #2\n- [x] #3\n- [x] #4" {
		t.Fatalf("epic body after the sync: %q", got)
	}
	if state.issues[1]["state"] != "open" || state.issues[5]["state"] != "closed" {
		t.Fatalf("epic with an open child must stay open, tracking parent must close: %v / %v",
			state.issues[1]["state"], state.issues[5]["state"])
	}
	if state.milestones[1]["state"] != "closed" || state.milestones[2]["state"] != "open" {
		t.Fatalf("milestones after the sync: %v / %v", state.milestones[1]["state"], state.milestones[2]["state"])
	}
	mustContain(t, out,
		"[APPLIED] acme/widgets#1 tick the boxes of closed children acme/widgets#2, acme/widgets#3",
		"[APPLIED] acme/widgets#5 tick the boxes of closed children acme/widgets#2",
		`[APPLIED] acme/widgets#5 close parent "Tracking"`,
		`[APPLIED] acme/widgets milestone #1 close "Done"`)
	if len(state.writes) != 4 {
		t.Fatalf("expected four writes, got %v", state.writes)
	}
}

// Negative: the default run and an explicit --dry-run write nothing and print every write
// --apply would make; --apply with --dry-run and an out-of-range cap are refused before
// any read.
func TestIssueReconcilePlanning_Negative_DryRunWritesNothing(t *testing.T) {
	srv, state := newPlanningForgeServer(t)
	for _, extra := range [][]string{nil, {"--dry-run"}} {
		out, err := captureStdout(t, func() error { return dispatchCommand("issue", reconcileArgs(srv, extra...)) })
		if err != nil {
			t.Fatalf("dry run %v: %v", extra, err)
		}
		mustContain(t, out, "Planning Sync (dry run, nothing written): 4 writes",
			"[PLAN] acme/widgets#1 tick the boxes of closed children acme/widgets#2, acme/widgets#3",
			`[PLAN] acme/widgets#5 close parent "Tracking"`,
			`[PLAN] acme/widgets milestone #1 close "Done"`,
			"[DRIFT] acme/widgets#1 ticked-open-child: box ticked for acme/widgets#4, which is open; left ticked")
	}
	if len(state.writes) != 0 || state.issues[1]["body"] != "- [ ] #2\n- [ ] #3\n- [x] #4" {
		t.Fatalf("a dry run wrote: %v", state.writes)
	}
	for _, extra := range [][]string{{"--apply", "--dry-run"}, {"--max-planning-writes=0"}, {"--max-planning-writes=501"}} {
		if err := dispatchCommand("issue", reconcileArgs(srv, extra...)); err == nil {
			t.Errorf("%v was accepted", extra)
		}
	}
}

// Boundary: a write cap of one makes one write and reports every other as deferred; the
// older --dry-run=false still applies.
func TestIssueReconcilePlanning_Boundary_WriteCapDefersTheRest(t *testing.T) {
	srv, state := newPlanningForgeServer(t)
	out, err := captureStdout(t, func() error {
		return dispatchCommand("issue", reconcileArgs(srv, "--dry-run=false", "--max-planning-writes=1"))
	})
	if err != nil {
		t.Fatalf("capped run: %v\n%s", err, out)
	}
	if len(state.writes) != 1 {
		t.Fatalf("a cap of one made %v", state.writes)
	}
	mustContain(t, out, "[APPLIED] acme/widgets#1 tick", "[DEFERRED] acme/widgets#5 tick",
		`[DEFERRED] acme/widgets milestone #1 close "Done": 0 open, 2 closed issues (write cap 1 reached)`,
		"3 planning writes are deferred by the write cap")
}
