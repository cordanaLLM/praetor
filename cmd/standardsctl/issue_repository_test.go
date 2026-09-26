// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// countingIssueForge answers every request with one open issue and counts the requests.
func countingIssueForge(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		payload := []map[string]any{{"number": 7, "title": "Tracked", "state": "open", "labels": []map[string]string{{"name": "blocked"}}}}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Errorf("encode fake listing: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func TestLoadFleetIssues_Positive_ValidCoordinateIsLoaded(t *testing.T) {
	srv, requests := countingIssueForge(t)
	labels := newIssueLabelIndex()
	engine := forge.NewReconcileEngine("acme")
	if err := loadFleetIssues(context.Background(), "fixture-token", srv.URL, []string{"acme/widgets"}, engine, labels); err != nil {
		t.Fatalf("loadFleetIssues: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("expected one listing request, got %d", requests.Load())
	}
	if _, known := labels.mergedReadyLabels("acme/widgets", 7); !known {
		t.Fatal("listed issue was not recorded")
	}
}

func TestLoadFleetIssues_Negative_TraversalCoordinateRefusedBeforeRequest(t *testing.T) {
	srv, requests := countingIssueForge(t)
	for _, coordinate := range []string{"acme/..", "../widgets", "acme/widgets/../../x", "acme/w?x=1"} {
		err := loadFleetIssues(context.Background(), "fixture-token", srv.URL, []string{coordinate},
			forge.NewReconcileEngine("acme"), newIssueLabelIndex())
		if err == nil || !strings.Contains(err.Error(), coordinate) {
			t.Fatalf("coordinate %q accepted or unnamed in error: %v", coordinate, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid coordinates reached the forge %d times", requests.Load())
	}
}

func TestLoadFleetIssues_Boundary_CoordinateWithoutSlashIsNotSkipped(t *testing.T) {
	srv, requests := countingIssueForge(t)
	// Silently skipping a malformed entry reported a clean run for a repository that was
	// never read; the entry after it must not be reached either.
	err := loadFleetIssues(context.Background(), "fixture-token", srv.URL, []string{"widgets", "acme/widgets"},
		forge.NewReconcileEngine("acme"), newIssueLabelIndex())
	if err == nil {
		t.Fatal("a coordinate without an owner was skipped silently")
	}
	if requests.Load() != 0 {
		t.Fatalf("expected no request after the malformed entry, got %d", requests.Load())
	}
}

func TestApplyUnblockTransitions_Negative_InvalidCoordinateFailsWithoutRequest(t *testing.T) {
	srv, requests := countingIssueForge(t)
	idx := newIssueLabelIndex()
	idx.record("acme/..", forge.IssueSpec{ID: 42})
	applied, failed := applyUnblockTransitions(context.Background(), "fixture-token", srv.URL,
		[]forge.UnblockAction{{Repo: "acme/..", IssueNumber: 42}}, idx)
	if applied != 0 || failed != 1 {
		t.Fatalf("invalid coordinate transitioned: %d/%d", applied, failed)
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid coordinate reached the forge %d times", requests.Load())
	}
}
