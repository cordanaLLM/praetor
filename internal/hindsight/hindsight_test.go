// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package hindsight

import (
	"context"
	"strings"
	"testing"
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

// TestHindsight_Negative verifies error handling on nil or cancelled contexts.
func TestHindsight_Negative(t *testing.T) {
	if _, err := DistillWorkspace(nil, "/tmp"); err == nil {
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
