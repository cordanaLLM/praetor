// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/efficiency/costledger"
)

func TestRunEfficiencyCosts_Positive(t *testing.T) {
	fixture := filepath.Join("..", "..", "internal", "efficiency", "costledger", "testdata", "positive.json")

	t.Run("table format", func(t *testing.T) {
		var buf bytes.Buffer
		if err := runEfficiencyTo([]string{"costs", "--input", fixture}, &buf); err != nil {
			t.Fatalf("run efficiency costs table: %v", err)
		}
		out := buf.String()
		for _, want := range []string{
			"=== Cost Ledger: Session Costs ===",
			"Shared Overhead (by_tokens)",
			"Sensitivity Share (by_sessions)",
			"=== Category Totals ===",
			"=== Efficiency Mechanisms ===",
			"hook:commit-msg",
			"yes (calls down, cost and latency not)",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("table output missing %q", want)
			}
		}
	})

	t.Run("json format", func(t *testing.T) {
		var buf bytes.Buffer
		if err := runEfficiencyTo([]string{"costs", "--input", fixture, "--json"}, &buf); err != nil {
			t.Fatalf("run efficiency costs json: %v", err)
		}
		var rep costledger.Report
		if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
			t.Fatalf("unmarshal json: %v", err)
		}
		if rep.SchemaVersion != 1 || len(rep.Sessions) != 3 {
			t.Errorf("unexpected json report shape: %+v", rep)
		}
	})
}

func TestRunEfficiencyCosts_Negative(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing input flag",
			args:    []string{"costs"},
			wantErr: "requires --input",
		},
		{
			name:    "positional argument rejected",
			args:    []string{"costs", "extra-arg", "--input=test.json"},
			wantErr: "accepts no positional arguments",
		},
		{
			name:    "invalid format",
			args:    []string{"costs", "--input=test.json", "--format=xml"},
			wantErr: "invalid format",
		},
		{
			name:    "nonexistent input file",
			args:    []string{"costs", "--input=nonexistent-ledger-path.json"},
			wantErr: "read cost ledger",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := runEfficiencyTo(tc.args, &buf)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestRunEfficiencyCosts_Help(t *testing.T) {
	var buf bytes.Buffer
	err := runEfficiencyTo([]string{"costs", "-h"}, &buf)
	if !errors.Is(err, flag.ErrHelp) {
		t.Errorf("expected flag.ErrHelp, got %v", err)
	}
}
