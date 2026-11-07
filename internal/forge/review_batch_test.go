package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// reviewBatchFixture reuses the per-PR fixture and raw decoder for aliased batch responses.
func reviewBatchFixture(t *testing.T, numbers []int, more bool) []byte {
	t.Helper()
	pulls := make(map[string]*reviewPullRaw, len(numbers))
	for _, number := range numbers {
		pull, err := decodeReviewPull(reviewPageFixture(t, "threads", 1, 1, more, "one"), 7)
		if err != nil {
			t.Fatal(err)
		}
		pull.Number, pull.HeadRefOID = number, "fixture-head"
		pulls[fmt.Sprintf("pr%d", number)] = pull
	}
	data, err := json.Marshal(map[string]any{"data": map[string]any{"repository": pulls}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestReviewBatchReadsWholeInventory pins zero, thirty and maximum inventories to at most one GraphQL call.
func TestReviewBatchReadsWholeInventory(t *testing.T) {
	for _, count := range []int{0, 30, MaxReviewPullRequests} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			numbers := make([]int, count)
			for i := range numbers {
				numbers[i] = i + 1
			}
			data := reviewBatchFixture(t, numbers, false)
			calls := 0
			reader := GHReviewReader{Run: func(ctx context.Context, _ string, args ...string) ([]byte, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Error("batch request lacks deadline")
				}
				assertReviewBatchQuery(t, args, numbers)
				return data, nil
			}}
			result, err := reader.ReadBatch(t.Context(), "example/widget", numbers)
			if err != nil || len(result) != count || calls != min(1, count) {
				t.Fatalf("incomplete batch: snapshots=%d calls=%d err=%v", len(result), calls, err)
			}
			assertReviewBatchSnapshots(t, result, numbers)
		})
	}
}

// assertReviewBatchSnapshots verifies identities, head commits and decoded thread fields.
func assertReviewBatchSnapshots(t *testing.T, result map[int]ReviewSnapshot, numbers []int) {
	t.Helper()
	for _, number := range numbers {
		p := result[number]
		if p.Number != number || p.HeadRefOID != "fixture-head" || len(p.Threads) != 1 {
			t.Fatalf("snapshot fields lost for #%d: %+v", number, p)
		}
		thread := p.Threads[0]
		if thread.Resolved || thread.CreatedAt.IsZero() || thread.Path == "" || thread.Author == "" {
			t.Errorf("thread fields lost for #%d: %+v", number, thread)
		}
	}
}

// assertReviewBatchQuery pins both bound non-null variables and every selected thread field.
func assertReviewBatchQuery(t *testing.T, args []string, numbers []int) {
	t.Helper()
	fields := reviewPageFields(args)
	if fields["owner"] != "example" || fields["name"] != "widget" {
		t.Fatalf("non-null variables not bound: %v", fields)
	}
	for _, field := range []string{"$owner:String!", "$name:String!", "number state headRefOid", "reviewThreads(first:100)",
		"pageInfo{hasNextPage endCursor}", "id isResolved path line", "comments(first:1){nodes{author{login} createdAt}}"} {
		if !strings.Contains(fields["query"], field) {
			t.Errorf("batch query missing %q", field)
		}
	}
	for _, number := range numbers {
		if !strings.Contains(fields["query"], fmt.Sprintf("pr%d:pullRequest(number:%d)", number, number)) {
			t.Errorf("query omitted #%d", number)
		}
	}
}

// TestReviewBatchRejectsInvalidSelection plants invalid identities, duplicate aliases and inventory overflow.
func TestReviewBatchRejectsInvalidSelection(t *testing.T) {
	for _, tc := range []struct {
		name, repo string
		numbers    []int
	}{
		{"repo", "invalid", []int{7}}, {"zero", "example/widget", []int{0}},
		{"negative", "example/widget", []int{-1}}, {"duplicate", "example/widget", []int{7, 7}},
		{"overflow", "example/widget", make([]int, MaxReviewPullRequests+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := GHReviewReader{Run: func(context.Context, string, ...string) ([]byte, error) {
				t.Error("invalid selection reached forge")
				return nil, errors.New("unexpected call")
			}}
			if result, err := reader.ReadBatch(t.Context(), tc.repo, tc.numbers); err == nil || len(result) != 0 {
				t.Fatalf("invalid selection accepted: result=%v err=%v", result, err)
			}
		})
	}
}

// TestReviewBatchRejectsUnreadNodes plants partial envelopes and malformed nodes alongside a valid PR.
func TestReviewBatchRejectsUnreadNodes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		neighbor bool
	}{
		{"command", false}, {"graphql", false}, {"json", false}, {"repository", false},
		{"node", true}, {"identity", true}, {"head", true}, {"state", true},
		{"threads", true}, {"page-info", true}, {"resolution", true}, {"comment", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := tc.name
			data := brokenReviewBatch(t, failure)
			reader := GHReviewReader{Run: func(context.Context, string, ...string) ([]byte, error) {
				if failure == "command" {
					return nil, errors.New("planted batch failure")
				}
				return data, nil
			}}
			result, err := reader.ReadBatch(t.Context(), "example/widget", []int{7, 8})
			if err == nil || result[7].Number != 0 {
				t.Fatalf("unread node accepted: result=%v err=%v", result, err)
			}
			if tc.neighbor && result[8].Number != 8 {
				t.Fatalf("valid neighbor discarded: %v err=%v", result, err)
			}
		})
	}
}

// brokenReviewBatch damages one node or the whole response while retaining a valid neighbor where possible.
func brokenReviewBatch(t *testing.T, failure string) []byte {
	t.Helper()
	if failure == "json" {
		return []byte(`{`)
	}
	if failure == "graphql" {
		return []byte(`{"errors":[{"message":"planted partial response"}],"data":{}}`)
	}
	if failure == "repository" {
		return []byte(`{"data":{"repository":null}}`)
	}
	var response struct {
		Data struct{ Repository map[string]*reviewPullRaw }
	}
	if err := json.Unmarshal(reviewBatchFixture(t, []int{7, 8}, false), &response); err != nil {
		t.Fatal(err)
	}
	corruptReviewBatchNode(response.Data.Repository, failure)
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// corruptReviewBatchNode plants one missing or malformed connection in an otherwise valid batch.
func corruptReviewBatchNode(pulls map[string]*reviewPullRaw, failure string) {
	pull := pulls["pr7"]
	switch failure {
	case "node":
		pulls["pr7"] = nil
	case "identity":
		pull.Number = 9
	case "head":
		pull.HeadRefOID = ""
	case "state":
		pull.State = "UNKNOWN"
	case "threads":
		pull.ReviewThreads.Nodes = nil
	case "page-info":
		pull.ReviewThreads.PageInfo = nil
	case "resolution":
		pull.ReviewThreads.Nodes[0].IsResolved = nil
	case "comment":
		pull.ReviewThreads.Nodes[0].Comments.Nodes = nil
	}
}

// TestReviewBatchTruncatedThreadsUseRead pins 101-thread fallback and shared cancellation without elapsed-time assertions.
func TestReviewBatchTruncatedThreadsUseRead(t *testing.T) {
	for _, failure := range []string{"", "command", "head", "cancelled"} {
		t.Run("fallback/"+failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := atomic.Int32{}
			reader := GHReviewReader{Run: reviewBatchFallbackRun(t, failure, &calls, cancel)}
			result, err := reader.ReadBatch(ctx, "example/widget", []int{7})
			if failure != "" {
				if err == nil || len(result) != 0 {
					t.Fatalf("partial fallback accepted: %v err=%v", result, err)
				}
				return
			}
			if err != nil || len(result[7].Threads) != 101 || calls.Load() != 3 {
				t.Fatalf("fallback incomplete: threads=%d calls=%d err=%v", len(result[7].Threads), calls.Load(), err)
			}
		})
	}
}

// reviewBatchFallbackRun supplies a truncated batch and bounded per-PR pages with planted failures.
func reviewBatchFallbackRun(t *testing.T, failure string, calls *atomic.Int32, cancel context.CancelFunc) func(context.Context, string, ...string) ([]byte, error) {
	t.Helper()
	return func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		call := calls.Add(1)
		if call == 1 {
			return reviewBatchFixture(t, []int{7}, true), nil
		}
		if failure == "command" {
			return nil, errors.New("planted fallback failure")
		}
		if failure == "cancelled" {
			cancel()
			return nil, ctx.Err()
		}
		if call > 2 {
			assertReviewPageSelection(t, "threads", args)
		}
		data := reviewPageFixture(t, "threads", 100, 1, true, "one")
		if call > 2 {
			data = reviewPageFixture(t, "threads", 1, 2, false, "two")
		}
		head := "fixture-head"
		if failure == "head" {
			head = "changed-head"
		}
		return []byte(strings.Replace(string(data), `"number":7`, `"headRefOid":"`+head+`","number":7`, 1)), nil
	}
}

// TestReviewBatchFallbackConcurrency pins eight joined workers against a real reader's offline call boundary.
func TestReviewBatchFallbackConcurrency(t *testing.T) {
	numbers := make([]int, 16)
	for i := range numbers {
		numbers[i] = i + 1
	}
	data := reviewBatchFixture(t, numbers, true)
	ready := make(chan struct{})
	var mu sync.Mutex
	active, peak, reads := 0, 0, 0
	released := false
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	reader := GHReviewReader{Run: func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		number := reviewPageFields(args)["number"]
		if number == "" {
			return data, nil
		}
		mu.Lock()
		active++
		reads++
		peak = max(peak, active)
		if active == reviewBatchConcurrency && !released {
			released = true
			close(ready)
		}
		mu.Unlock()
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ready:
		}
		page := reviewPageFixture(t, "threads", 0, 1, false, "")
		return []byte(strings.Replace(string(page), `"number":7`, `"headRefOid":"fixture-head","number":`+number, 1)), nil
	}}
	result, err := reader.ReadBatch(ctx, "example/widget", numbers)
	got := [4]int{len(result), reads, peak, active}
	want := [4]int{len(numbers), len(numbers), reviewBatchConcurrency, 0}
	if err != nil || got != want {
		t.Fatalf("fallback workers: snapshots=%d reads=%d peak=%d active=%d err=%v", len(result), reads, peak, active, err)
	}
}
