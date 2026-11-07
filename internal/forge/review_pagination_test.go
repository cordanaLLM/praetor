package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestReviewPagePassesNonNullVariables checks every required query variable, including defaulted ones.
func TestReviewPagePassesNonNullVariables(t *testing.T) {
	header, _, ok := strings.Cut(reviewReadQuery, "{")
	if !ok {
		t.Fatal("review query has no declaration header")
	}
	declaration := regexp.MustCompile(`\$([_A-Za-z][_0-9A-Za-z]*)\s*:\s*([_A-Za-z][_0-9A-Za-z]*|\[[\s_0-9A-Za-z!\[\]]+\])\s*(!?)`)
	variables := declaration.FindAllStringSubmatch(header, len(header))
	if len(variables) == 0 {
		t.Fatal("review query has no variable declarations")
	}
	for _, kind := range []string{"", "threads", "reviews", "checks"} {
		name := kind
		if name == "" {
			name = "first"
		}
		t.Run(name, func(t *testing.T) {
			args := reviewPageArgs(reviewReadRequest{owner: "example", name: "widget", number: 7}, kind, "next")
			fields := reviewPageFields(args)
			for _, variable := range variables {
				if _, passed := fields[variable[1]]; variable[3] == "!" && !passed {
					t.Errorf("missing non-null variable $%s", variable[1])
				}
			}
		})
	}
}

// reviewPageFields reads only field arguments, excluding variable references inside the query body.
func reviewPageFields(args []string) map[string]string {
	fields := make(map[string]string)
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-f" || args[i] == "-F" {
			key, value, ok := strings.Cut(args[i+1], "=")
			if ok {
				fields[key] = value
			}
			i++
		}
	}
	return fields
}

// TestReviewPageFirstCallPassesBooleans records the real call boundary's first-page typed fields.
func TestReviewPageFirstCallPassesBooleans(t *testing.T) {
	var calls [][]string
	reader := GHReviewReader{Run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, slices.Clone(args))
		return reviewPageFixture(t, "threads", 0, 1, false, ""), nil
	}}
	if _, err := reader.Read(t.Context(), "example/widget", 7); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("first-page calls = %d, want 1", len(calls))
	}
	for _, connection := range []string{"threads", "reviews", "checks"} {
		index := slices.Index(calls[0], connection+"=true")
		if index < 1 || calls[0][index-1] != "-F" {
			t.Errorf("first call missing -F %s=true", connection)
		}
	}
}

// reviewPageFixture constructs complete envelopes with a selected connection page.
func reviewPageFixture(t *testing.T, kind string, count, page int, more bool, cursor string) []byte {
	t.Helper()
	nodes := make([]any, 0, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("page-%d-item-%d", page, i)
		switch kind {
		case "threads":
			nodes = append(nodes, map[string]any{"id": id, "path": id + ".go", "isResolved": false,
				"comments": map[string]any{"nodes": []any{map[string]any{"createdAt": "2026-11-01T18:00:00Z"}}}})
		case "reviews":
			nodes = append(nodes, map[string]any{"id": id, "state": "COMMENTED"})
		case "checks":
			nodes = append(nodes, map[string]any{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SUCCESS"})
		}
	}
	empty := map[string]any{"nodes": []any{}, "pageInfo": map[string]any{"hasNextPage": false}}
	connection := map[string]any{"nodes": nodes, "pageInfo": map[string]any{"hasNextPage": more, "endCursor": cursor}}
	pull := map[string]any{"number": 7, "state": "OPEN", "reviewThreads": empty, "reviews": empty}
	checks := any(empty)
	switch kind {
	case "threads":
		pull["reviewThreads"] = connection
	case "reviews":
		pull["reviews"] = connection
	case "checks":
		checks = connection
	}
	pull["commits"] = map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{
		"statusCheckRollup": map[string]any{"state": "SUCCESS", "contexts": checks}}}}}
	data, err := json.Marshal(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": pull}}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestReviewConnectionPagination plants item 101 and pins empty, page-size and page-ceiling boundaries.
func TestReviewConnectionPagination(t *testing.T) {
	for _, kind := range []string{"threads", "reviews", "checks"} {
		for _, count := range []int{0, 100, 101, 1000, 1001} {
			t.Run(fmt.Sprintf("%s/%d", kind, count), func(t *testing.T) {
				testReviewConnectionCase(t, kind, count)
			})
		}
	}
}

// testReviewConnectionCase verifies one connection size through the public reader.
func testReviewConnectionCase(t *testing.T, kind string, count int) {
	t.Helper()
	calls := 0
	reader := GHReviewReader{Run: func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("page read lacks deadline")
		}
		if calls > 0 && !strings.Contains(strings.Join(args, " "), fmt.Sprintf("%sAfter=cursor-%d", kind, calls)) {
			t.Fatalf("page cursor not forwarded: %q", args)
		}
		if calls > 0 {
			assertReviewPageSelection(t, kind, args)
		}
		remaining := count - calls*100
		calls++
		return reviewPageFixture(t, kind, min(100, remaining), calls, remaining > 100, fmt.Sprintf("cursor-%d", calls)), nil
	}}
	snapshot, err := reader.Read(t.Context(), "example/widget", 7)
	if count > 1000 {
		if err == nil || calls != 10 || snapshot.Number != 0 {
			t.Fatalf("page ceiling accepted partial snapshot: %+v calls=%d err=%v", snapshot, calls, err)
		}
		return
	}
	assertReviewPageCount(t, kind, snapshot, count, calls, err)
}

// assertReviewPageSelection pins independent cursors and excludes completed connections from later requests.
func assertReviewPageSelection(t *testing.T, kind string, args []string) {
	t.Helper()
	joined := strings.Join(args, " ")
	for _, connection := range []string{"threads", "reviews", "checks"} {
		if !strings.Contains(joined, fmt.Sprintf("-F %s=%t", connection, kind == connection)) {
			t.Fatalf("wrong continuation selection for %s: %q", kind, args)
		}
	}
}

// assertReviewPageCount verifies every page contributes once to the returned snapshot.
func assertReviewPageCount(t *testing.T, kind string, snapshot ReviewSnapshot, count, calls int, err error) {
	t.Helper()
	if err != nil || calls != max(1, (count+99)/100) {
		t.Fatalf("pagination incomplete: kind=%s count=%d calls=%d err=%v", kind, count, calls, err)
	}
	if kind == "checks" {
		if snapshot.ChecksTerminal != (count > 0) {
			t.Fatalf("check terminal state wrong: %+v", snapshot)
		}
		return
	}
	ids := reviewSnapshotIDs(kind, snapshot)
	if len(ids) != count {
		t.Fatalf("%s missing or duplicated: %d", kind, len(ids))
	}
	if count > 100 && ids[100] != "page-2-item-0" {
		t.Fatalf("later %s missing: %s", kind, ids[100])
	}
}

// reviewSnapshotIDs selects decoded identifiers for completeness assertions.
func reviewSnapshotIDs(kind string, snapshot ReviewSnapshot) []string {
	var ids []string
	if kind == "threads" {
		for _, thread := range snapshot.Threads {
			ids = append(ids, thread.ID)
		}
		return ids
	}
	for _, review := range snapshot.Reviews {
		ids = append(ids, review.ID)
	}
	return ids
}

// TestReviewPaginationRejectsBrokenContinuation rejects missing, cyclic and malformed later pages.
func TestReviewPaginationRejectsBrokenContinuation(t *testing.T) {
	for _, kind := range []string{"threads", "reviews", "checks"} {
		for _, failure := range []string{"missing-cursor", "repeated-cursor", "terminal-repeat", "cycle", "oversized", "command", "malformed", "identity", "changed-head", "changed-state", "missing-nodes", "missing-page-info", "cancelled"} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				calls := 0
				reader := GHReviewReader{Run: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
					calls++
					return brokenReviewPage(t, ctx, kind, failure, calls)
				}}
				snapshot, err := reader.Read(t.Context(), "example/widget", 7)
				if err == nil || snapshot.Number != 0 || calls > 3 {
					t.Fatalf("broken continuation accepted: snapshot=%+v calls=%d err=%v", snapshot, calls, err)
				}
				if failure != "missing-cursor" && failure != "oversized" && calls < 2 {
					t.Fatalf("continuation never exercised: calls=%d err=%v", calls, err)
				}
			})
		}
	}
}

// brokenReviewPage plants a specific failure after a valid connection's first page.
func brokenReviewPage(t *testing.T, ctx context.Context, kind, failure string, calls int) ([]byte, error) {
	t.Helper()
	cursor := "one"
	if failure == "missing-cursor" {
		cursor = ""
	}
	if failure == "cycle" && calls == 2 {
		cursor = "two"
	}
	if failure == "oversized" {
		return reviewPageFixture(t, kind, 101, calls, false, cursor), nil
	}
	if calls > 1 {
		return brokenReviewContinuation(t, ctx, kind, failure, calls, cursor)
	}
	return reviewPageFixture(t, kind, 1, calls, true, cursor), nil
}

// brokenReviewContinuation corrupts a subsequent page without changing a valid first page.
func brokenReviewContinuation(t *testing.T, ctx context.Context, kind, failure string, calls int, cursor string) ([]byte, error) {
	t.Helper()
	switch failure {
	case "command":
		return nil, errors.New("planted page failure")
	case "malformed":
		return []byte(`{`), nil
	case "cancelled":
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return nil, cancelled.Err()
	}
	more := failure == "repeated-cursor" || failure == "cycle"
	data := reviewPageFixture(t, kind, 1, calls, more, cursor)
	replacements := map[string][2]string{
		"identity":          {`"number":7`, `"number":8`},
		"changed-head":      {`"number":7`, `"headRefOid":"different","number":7`},
		"changed-state":     {`"state":"OPEN"`, `"state":"CLOSED"`},
		"missing-nodes":     {`"nodes":`, `"absentNodes":`},
		"missing-page-info": {`"pageInfo":`, `"absentPageInfo":`},
	}
	if replacement, ok := replacements[failure]; ok {
		data = []byte(strings.ReplaceAll(string(data), replacement[0], replacement[1]))
	}
	return data, nil
}

// TestReviewPaginationIncludesPendingLastCheck prevents an early terminal check from hiding later work.
func TestReviewPaginationIncludesPendingLastCheck(t *testing.T) {
	calls := 0
	reader := GHReviewReader{Run: func(context.Context, string, ...string) ([]byte, error) {
		calls++
		if calls == 1 {
			return reviewPageFixture(t, "checks", 100, calls, true, "one"), nil
		}
		data := reviewPageFixture(t, "checks", 1, calls, false, "two")
		return []byte(strings.Replace(string(data), `"status":"COMPLETED"`, `"status":"IN_PROGRESS"`, 1)), nil
	}}
	snapshot, err := reader.Read(t.Context(), "example/widget", 7)
	if err != nil || snapshot.ChecksTerminal || calls != 2 {
		t.Fatalf("pending last check hidden: snapshot=%+v calls=%d err=%v", snapshot, calls, err)
	}
}
