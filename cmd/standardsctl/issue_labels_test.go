// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

func TestIssueLabelIndex_Positive_MergePreservesOtherLabels(t *testing.T) {
	idx := newIssueLabelIndex()
	idx.record("cordanaLLM/praetor", forge.IssueSpec{
		ID:     42,
		Labels: []string{"status/blocked", "type/bug", "area/hiss", "priority/P1"},
	})

	merged, known := idx.mergedReadyLabels("cordanaLLM/praetor", 42)
	if !known {
		t.Fatal("expected the recorded issue to be known")
	}
	joined := strings.Join(merged, ",")
	for _, keep := range []string{"type/bug", "area/hiss", "priority/P1", readyLabel} {
		if !strings.Contains(joined, keep) {
			t.Errorf("label %q must survive the transition, got %v", keep, merged)
		}
	}
	if strings.Contains(joined, "status/blocked") {
		t.Errorf("status/blocked must be removed, got %v", merged)
	}
}

func TestApplyUnblockTransitions_Positive_PreservesConcurrentLabels(t *testing.T) {
	var mu sync.Mutex
	labels := map[string]bool{"status/blocked": true, "blocked": true, "type/bug": true}
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method)
		if r.Method == http.MethodPost {
			var payload struct {
				Labels []string `json:"labels"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			labels["area/new-since-scan"] = true
			for _, label := range payload.Labels {
				labels[label] = true
			}
			w.WriteHeader(http.StatusCreated)
			return
		}
		if r.Method == http.MethodDelete {
			delete(labels, strings.TrimPrefix(r.URL.Path, "/repos/acme/widgets/issues/42/labels/"))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "unexpected replacing update", http.StatusMethodNotAllowed)
	}))
	t.Cleanup(srv.Close)
	idx := newIssueLabelIndex()
	idx.record("acme/widgets", forge.IssueSpec{ID: 42, Labels: []string{"status/blocked", "type/bug"}})
	actions := []forge.UnblockAction{{Repo: "acme/widgets", IssueNumber: 42}}
	applied, failed := applyUnblockTransitions(context.Background(), "test-fixture", srv.URL, actions, idx)
	if applied != 1 || failed != 0 {
		t.Fatalf("transition result: applied=%d failed=%d", applied, failed)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(calls, ",") != "POST,DELETE,DELETE" {
		t.Fatalf("unexpected methods: %v", calls)
	}
	for _, label := range []string{"type/bug", "area/new-since-scan", readyLabel} {
		if !labels[label] {
			t.Errorf("label %q was lost: %v", label, labels)
		}
	}
	if labels["blocked"] || labels["status/blocked"] {
		t.Fatalf("blocked labels remain: %v", labels)
	}
}

func TestApplyUnblockTransitions_Negative_PropagatesUpdateFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rejected", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	idx := newIssueLabelIndex()
	idx.record("acme/widgets", forge.IssueSpec{ID: 42})
	applied, failed := applyUnblockTransitions(context.Background(), "fixture-token", srv.URL,
		[]forge.UnblockAction{{Repo: "acme/widgets", IssueNumber: 42}}, idx)
	if applied != 0 || failed != 1 {
		t.Fatalf("API rejection reported as applied: %d/%d", applied, failed)
	}
}

func TestApplyUnblockTransitions_Boundary_RefusesUnobservedOrOversizedBatch(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	idx := newIssueLabelIndex()
	unknown := []forge.UnblockAction{{Repo: "acme/widgets", IssueNumber: 42}}
	if applied, failed := applyUnblockTransitions(context.Background(), "fixture-token", srv.URL, unknown, idx); applied != 0 || failed != 1 {
		t.Fatalf("unobserved issue accepted: %d/%d", applied, failed)
	}
	over := make([]forge.UnblockAction, maxUnblockTransitions+1)
	if applied, failed := applyUnblockTransitions(context.Background(), "fixture-token", srv.URL, over, idx); applied != 0 || failed != len(over) {
		t.Fatalf("oversized batch accepted: %d/%d", applied, failed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if applied, failed := applyUnblockTransitions(ctx, "fixture-token", srv.URL, unknown, idx); applied != 0 || failed != 1 {
		t.Fatalf("cancellation ignored: %d/%d", applied, failed)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Fatalf("refused transitions made %d requests", requests)
	}
}

func TestIssueLabelIndex_Negative_UnknownIssueIsNotTransitioned(t *testing.T) {
	idx := newIssueLabelIndex()

	if _, known := idx.mergedReadyLabels("cordanaLLM/praetor", 7); known {
		t.Error("an unobserved issue must not be reported as known, because a label PATCH replaces the whole set")
	}

	var nilIdx issueLabelIndex
	nilIdx.record("cordanaLLM/praetor", forge.IssueSpec{ID: 1}) // must not panic
	if _, known := nilIdx.mergedReadyLabels("cordanaLLM/praetor", 1); known {
		t.Error("a nil index must report nothing as known")
	}
}

func TestIssueLabelIndex_Boundary_EmptyAndAlreadyReady(t *testing.T) {
	idx := newIssueLabelIndex()
	idx.record("a/b", forge.IssueSpec{ID: 1, Labels: nil})
	idx.record("a/b", forge.IssueSpec{ID: 2, Labels: []string{"BLOCKED", readyLabel}})

	merged, known := idx.mergedReadyLabels("a/b", 1)
	if !known || len(merged) != 1 || merged[0] != readyLabel {
		t.Errorf("an issue with no labels must end up with exactly the ready label, got %v", merged)
	}

	merged, known = idx.mergedReadyLabels("a/b", 2)
	if !known || len(merged) != 1 || merged[0] != readyLabel {
		t.Errorf("case-insensitive blocked markers and a duplicate ready label must collapse, got %v", merged)
	}
}

func TestIsBlockedLabel_3D(t *testing.T) {
	if !isBlockedLabel("status/blocked") || !isBlockedLabel("BLOCKED") {
		t.Error("expected blocked markers to match case-insensitively")
	}
	if isBlockedLabel("type/bug") {
		t.Error("unrelated labels must not be treated as blocked markers")
	}
	if isBlockedLabel("") {
		t.Error("an empty label must not be treated as a blocked marker")
	}
}

func TestParseTargetRepos_3D(t *testing.T) {
	got := parseTargetRepos("praetor, golusoris/golusoris ,", "cordanaLLM")
	if len(got) != 2 || got[0] != "cordanaLLM/praetor" || got[1] != "golusoris/golusoris" {
		t.Fatalf("unexpected repo list: %v", got)
	}
	if len(parseTargetRepos("", "cordanaLLM")) != 0 {
		t.Error("an empty repos flag must yield no targets")
	}

	long := strings.Repeat("a/b,", maxReconciledRepos+50)
	if got := len(parseTargetRepos(long, "cordanaLLM")); got > maxReconciledRepos {
		t.Errorf("repo fan-out must respect the %d bound, got %d", maxReconciledRepos, got)
	}
}
