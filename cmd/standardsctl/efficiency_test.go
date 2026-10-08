// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunEfficiency_Positive(t *testing.T) {
	dir := t.TempDir()
	prsPath := filepath.Join(dir, "prs.json")
	if err := os.WriteFile(prsPath, []byte(`[{"number": 1, "head_branch": "feat/main", "title": "Main", "created_at": "2026-10-01T10:00:00Z", "merged_at": "2026-10-01T11:00:00Z"}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Test table format
	if err := runEfficiency([]string{"--forge-records=" + prsPath, "--format=table"}); err != nil {
		t.Fatalf("unexpected error running efficiency table: %v", err)
	}

	// Test json format
	if err := runEfficiency([]string{"--forge-records=" + prsPath, "--format=json"}); err != nil {
		t.Fatalf("unexpected error running efficiency json: %v", err)
	}

	// Test --json alias
	if err := runEfficiency([]string{"--forge-records=" + prsPath, "--json"}); err != nil {
		t.Fatalf("unexpected error running efficiency --json: %v", err)
	}
}

func TestRunEfficiency_Negative_InvalidArgs(t *testing.T) {
	// Positional arguments refused
	if err := runEfficiency([]string{"unexpected-arg"}); err == nil {
		t.Error("expected error when positional arguments are supplied")
	}

	// Invalid format
	if err := runEfficiency([]string{"--format=xml"}); err == nil {
		t.Error("expected error for invalid format 'xml'")
	}
}

func TestRunEfficiency_Boundary_EmptySource(t *testing.T) {
	dir := t.TempDir()
	prsPath := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(prsPath, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runEfficiency([]string{"--forge-records=" + prsPath, "--json"}); err != nil {
		t.Fatalf("unexpected error running efficiency with empty PR list: %v", err)
	}
}

func TestCommandTableRegistersEfficiency(t *testing.T) {
	table := commandTable()
	if table["efficiency"] == nil {
		t.Error("efficiency command missing from command table")
	}
	if table["efficiency-ledger"] == nil {
		t.Error("efficiency-ledger command missing from command table")
	}
}

func TestPrintUsageCarriesEfficiency(t *testing.T) {
	table := coreCommandTable()
	if _, ok := table["efficiency"]; !ok {
		t.Error("efficiency missing from coreCommandTable")
	}
}
