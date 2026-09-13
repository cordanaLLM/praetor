package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

func TestIssueReconcileRefusesIncompleteFleetBeforeMutation(t *testing.T) {
	for _, mode := range []string{"incomplete", "forbidden"} {
		t.Run(mode, func(t *testing.T) {
			var writes atomic.Int32
			srv := reconciliationInventoryServer(t, mode, &writes)
			output, err := captureStdout(t, func() error {
				return dispatchCommand("issue", []string{"reconcile", "--owner=example",
					"--repos=example/complete,example/unavailable", "--dry-run=false",
					"--token=fixture", "--endpoint=" + srv.URL})
			})
			if err == nil || writes.Load() != 0 {
				t.Fatalf("partial inventory admitted reconciliation: err=%v writes=%d output=%s", err, writes.Load(), output)
			}
			var incomplete *forge.IssueListIncompleteError
			if mode == "incomplete" && !errors.As(err, &incomplete) {
				t.Fatalf("incomplete-list error lost through CLI wrapping: %v", err)
			}
			if !strings.Contains(err.Error(), "example/unavailable") || strings.Contains(output, "[PASS]") {
				t.Fatalf("missing failed repository or premature success: err=%v output=%s", err, output)
			}
		})
	}
}

func reconciliationInventoryServer(t *testing.T, mode string, writes *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
			return
		}
		if strings.Contains(r.URL.Path, "/complete/") {
			writeInventoryResponse(t, w, []map[string]any{
				{"number": 1, "title": "prerequisite", "state": "closed"},
				{"number": 2, "title": "consumer", "state": "open", "body": "Depends-On: example/complete#1",
					"labels": []map[string]string{{"name": "status/blocked"}}},
			})
			return
		}
		if mode == "complete" {
			writeInventoryResponse(t, w, []map[string]any{})
			return
		}
		if mode == "forbidden" {
			http.Error(w, "fixture permission failure", http.StatusForbidden)
			return
		}
		page := make([]map[string]any, 100)
		for i := 0; i < 100; i++ {
			page[i] = map[string]any{"number": i + 1, "title": fmt.Sprintf("finding %d", i+1), "state": "open"}
		}
		writeInventoryResponse(t, w, page)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeInventoryResponse(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("write fixture inventory: %v", err)
	}
}

func TestIssueReconcileCompleteFleetPreservesApplyAndDryRun(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprint(dry), func(t *testing.T) {
			var writes atomic.Int32
			srv := reconciliationInventoryServer(t, "complete", &writes)
			_, err := captureStdout(t, func() error {
				return dispatchCommand("issue", []string{"reconcile", "--owner=example",
					"--repos=example/complete,example/unavailable", fmt.Sprintf("--dry-run=%t", dry),
					"--token=fixture", "--endpoint=" + srv.URL})
			})
			if err != nil {
				t.Fatal(err)
			}
			want := int32(3)
			if dry {
				want = 0
			}
			if writes.Load() != want {
				t.Fatalf("writes=%d, want %d", writes.Load(), want)
			}
		})
	}
}

func TestIssueReconcileRepositorySelectionBoundBeforeRead(t *testing.T) {
	for _, count := range []int{256, 257} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var reads atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				if r.Method != http.MethodGet {
					t.Errorf("unexpected mutation %s", r.Method)
				}
				writeInventoryResponse(t, w, []map[string]any{})
			}))
			t.Cleanup(srv.Close)
			repos := strings.Repeat("example/selected,", count-1) + "example/last"
			_, err := captureStdout(t, func() error {
				return dispatchCommand("issue", []string{"reconcile", "--repos=" + repos,
					"--token=fixture", "--endpoint=" + srv.URL})
			})
			if count == 256 {
				if err != nil || reads.Load() != 256 {
					t.Fatalf("supported selection failed: err=%v reads=%d", err, reads.Load())
				}
			} else if err == nil || reads.Load() != 0 {
				t.Fatalf("oversized selection was truncated: err=%v reads=%d", err, reads.Load())
			}
		})
	}
}
