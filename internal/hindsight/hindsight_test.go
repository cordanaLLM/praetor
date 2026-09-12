// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package hindsight

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestHindsight_Positive verifies workspace distillation, page synthesis, and recall.
func TestHindsight_Positive(t *testing.T) {
	tmpDir := t.TempDir()

	report, err := DistillWorkspace(context.Background(), tmpDir)
	if err != nil || report == nil {
		t.Fatalf("unexpected error distilling workspace: %v", err)
	}

	pages := SynthesizeKnowledgePages(tmpDir, report.Facts)
	if len(pages) != 5 {
		t.Errorf("expected 5 knowledge pages, got %d", len(pages))
	}

	facts := []MemoryFact{
		{
			ID:        "f1",
			Category:  CategoryFlavor,
			Subject:   "go-service",
			Statement: "Repository uses flavor go-service requiring go 1.24+.",
			Evidence:  "internal/flavor",
			Tags:      []string{"flavor", "go"},
		},
		{
			ID:        "f2",
			Category:  CategoryDependencyDoc,
			Subject:   "gopkg.in/yaml.v3",
			Statement: "Package yaml.v3 provides YAML encoding and decoding.",
			Evidence:  ".workingdir/docs/catalog.json",
			Tags:      []string{"package", "yaml"},
		},
	}

	if err := SaveLocalCache(tmpDir, facts); err != nil {
		t.Fatalf("failed saving local cache: %v", err)
	}

	recalled := RecallLocalFacts(tmpDir, "yaml", CategoryDependencyDoc)
	if len(recalled) != 1 || recalled[0].Subject != "gopkg.in/yaml.v3" {
		t.Fatalf("unexpected recalled fact for 'yaml': %+v", recalled)
	}
}

// TestHindsight_Positive_ClientIngestReachesServer verifies the wire format of a fact
// ingestion against a local test server: path, bearer token and JSON payload.
func TestHindsight_Positive_ClientIngestReachesServer(t *testing.T) {
	var hits atomic.Int32
	var gotPath, gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := DefaultClientConfig()
	cfg.BaseURL = server.URL
	cfg.Token = "tok"
	cfg.RateLimitRPM = 6000
	client := NewClient(cfg)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fact := MemoryFact{ID: "f1", Category: CategoryFlavor, Subject: "go-service", Statement: "s", Evidence: "e"}
	if err := client.IngestFact(ctx, "bank-1", fact); err != nil {
		t.Fatalf("IngestFact: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hits = %d, want 1", hits.Load())
	}
	if gotPath != "/v1/default/banks/bank-1/document-transfer" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("authorization = %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"document_id":"fact-f1"`) {
		t.Errorf("body lacks document id: %s", gotBody)
	}
}

// TestHindsight_Negative verifies error handling on nil or cancelled contexts.
func TestHindsight_Negative(t *testing.T) {
	if _, err := DistillWorkspace(nil, t.TempDir()); err == nil { //nolint:staticcheck // the nil-context guard is the behaviour under test
		t.Errorf("expected error with nil context")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := NewClient(DefaultClientConfig())
	defer client.Close()

	if err := client.IngestFact(ctx, "test-bank", MemoryFact{ID: "f1"}); err == nil {
		t.Errorf("expected error on cancelled context")
	}
}

// Failed online ingestion must remain visible to callers; explicit offline mode
// is the only path that omits transmission successfully.
func TestHindsight_Negative_ClientReportsTransportFailures(t *testing.T) {
	var hits atomic.Int32
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer failing.Close()

	cfg := DefaultClientConfig()
	cfg.BaseURL = failing.URL
	cfg.RateLimitRPM = 6000
	cfg.TimeoutSeconds = 1
	client := NewClient(cfg)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.IngestFact(ctx, "bank", MemoryFact{ID: "f500"}); err == nil {
		t.Error("500 response must report failed ingestion")
	}
	if hits.Load() != 1 {
		t.Errorf("server hits = %d, want 1", hits.Load())
	}

	// A closed port must return a bounded transport error.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	refusedURL := "http://" + ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	cfg.BaseURL = refusedURL
	refused := NewClient(cfg)
	defer refused.Close()
	start := time.Now()
	if err := refused.IngestFact(ctx, "bank", MemoryFact{ID: "fref"}); err == nil {
		t.Error("refused connection must report failed ingestion")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("refused connection took %v, the client timeout is 1s", time.Since(start))
	}
}

// TestHindsight_Boundary verifies zero matches and empty query handling.
func TestHindsight_Boundary(t *testing.T) {
	tmpDir := t.TempDir()
	if recalled := RecallLocalFacts(tmpDir, "nonexistent-query", ""); len(recalled) != 0 {
		t.Errorf("expected 0 matches, got %d", len(recalled))
	}

	facts := []MemoryFact{
		{ID: "1", Subject: "arch", Statement: "Architecture statement"},
	}
	if err := SaveLocalCache(tmpDir, facts); err != nil {
		t.Fatalf("failed to save local cache: %v", err)
	}

	recalled := RecallLocalFacts(tmpDir, "", "")
	if len(recalled) != 1 || !strings.Contains(recalled[0].Statement, "Architecture") {
		t.Errorf("unexpected boundary recall result: %+v", recalled)
	}
}

// TestHindsight_Boundary_OfflineClientNeverDials verifies OfflineOnly short-circuits
// before any network activity, even against an unreachable base URL.
func TestHindsight_Boundary_OfflineClientNeverDials(t *testing.T) {
	cfg := DefaultClientConfig()
	cfg.BaseURL = "http://127.0.0.1:1"
	cfg.OfflineOnly = true
	client := NewClient(cfg)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	if err := client.IngestFact(ctx, "bank", MemoryFact{ID: "off"}); err != nil {
		t.Errorf("offline ingest returned %v", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Errorf("offline ingest waited on the rate limiter or network: %v", time.Since(start))
	}
}
