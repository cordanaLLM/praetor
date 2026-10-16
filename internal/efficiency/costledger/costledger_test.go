// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package costledger_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/efficiency"
	"github.com/cordanaLLM/praetor/internal/efficiency/costledger"
)

func TestCostLedger_Positive(t *testing.T) {
	fixturePath := filepath.Join("testdata", "positive.json")
	ledger, err := costledger.Load(fixturePath)
	if err != nil {
		t.Fatalf("load positive ledger: %v", err)
	}

	report, err := costledger.ComputeReport(ledger)
	if err != nil {
		t.Fatalf("compute report: %v", err)
	}

	verifyPositiveAllocation(t, report)
	verifyPositiveMechanisms(t, report)
	verifyPositiveRender(t, report)
	verifyPositiveRoundTrip(t, report)
}

func verifyPositiveAllocation(t *testing.T, rep *costledger.Report) {
	t.Helper()
	if len(rep.Sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(rep.Sessions))
	}
	verifyAllocationSums(t, rep)
	verifyFirstSessionAllocation(t, rep.Sessions[0])
}

func verifyAllocationSums(t *testing.T, rep *costledger.Report) {
	t.Helper()
	var sumRuleTok, sumSensTok int64
	var sumRuleSpend, sumSensSpend costledger.SpendMicros
	for _, s := range rep.Sessions {
		if s.AllocatedShare == nil {
			t.Fatalf("session %s missing allocated share", s.Session)
		}
		sumRuleTok += s.AllocatedShare.Tokens
		sumRuleSpend += s.AllocatedShare.SpendMicros
		sumSensTok += s.AllocatedShare.SensitivityTokens
		sumSensSpend += s.AllocatedShare.SensitivitySpendMicros
	}

	verifyShareSums(t, sumRuleTok, sumRuleSpend, sumSensTok, sumSensSpend)
}

func verifyShareSums(
	t *testing.T,
	sumRuleTok int64,
	sumRuleSpend costledger.SpendMicros,
	sumSensTok int64,
	sumSensSpend costledger.SpendMicros,
) {
	t.Helper()
	if sumRuleTok != 9000 {
		t.Errorf("expected rule tokens sum 9000, got %v", sumRuleTok)
	}
	if sumRuleSpend != 450000 {
		t.Errorf("expected rule spend sum 450000 micros, got %v", sumRuleSpend)
	}
	if sumSensTok != 9000 {
		t.Errorf("expected sensitivity tokens sum 9000, got %v", sumSensTok)
	}
	if sumSensSpend != 450000 {
		t.Errorf("expected sensitivity spend sum 450000 micros, got %v", sumSensSpend)
	}
}

func verifyFirstSessionAllocation(t *testing.T, s1 costledger.SessionReport) {
	t.Helper()
	if s1.Session != "s1" || s1.AllocatedShare.Tokens != 1500 || s1.AllocatedShare.SensitivityTokens != 3000 {
		t.Errorf("unexpected s1 shares: %+v", s1.AllocatedShare)
	}
	if s1.AllocatedShare.DifferenceTokens != -1500 {
		t.Errorf("unexpected s1 diff tokens: %v", s1.AllocatedShare.DifferenceTokens)
	}
}

func verifyPositiveMechanisms(t *testing.T, rep *costledger.Report) {
	t.Helper()
	if len(rep.Mechanisms) != 3 {
		t.Fatalf("expected 3 mechanisms, got %d", len(rep.Mechanisms))
	}
	verifyFirstMechanism(t, rep.Mechanisms[0])
	verifySecondMechanism(t, rep.Mechanisms[1])
	verifyThirdMechanism(t, rep.Mechanisms[2])
}

func verifyFirstMechanism(t *testing.T, m1 costledger.MechanismReport) {
	t.Helper()
	if m1.ID != "hook:commit-msg" || m1.BreakEvenNStar == nil || *m1.BreakEvenNStar != 4 || !m1.BreakEvenReached {
		t.Errorf("unexpected m1 break-even: %+v", m1)
	}
	if !m1.Cosmetic {
		t.Errorf("expected m1 cosmetic flag true, got false")
	}
}

func verifySecondMechanism(t *testing.T, m2 costledger.MechanismReport) {
	t.Helper()
	if m2.BreakEvenStatus != "no break-even" || m2.BreakEvenNStar != nil || m2.BreakEvenReached {
		t.Errorf("unexpected m2 break-even for saving 0: %+v", m2)
	}
}

func verifyThirdMechanism(t *testing.T, m3 costledger.MechanismReport) {
	t.Helper()
	if m3.BreakEvenNStar == nil || *m3.BreakEvenNStar != 3 || !m3.BreakEvenReached {
		t.Errorf("unexpected m3 exact N* reach: %+v", m3)
	}
}

func verifyPositiveRender(t *testing.T, rep *costledger.Report) {
	t.Helper()
	var buf bytes.Buffer
	if err := costledger.RenderTable(rep, &buf); err != nil {
		t.Fatalf("render table: %v", err)
	}
	out := buf.String()
	for _, expected := range []string{
		"=== Cost Ledger: Session Costs ===",
		"Shared Overhead (by_tokens)",
		"Sensitivity Share (by_sessions)",
		"=== Category Totals ===",
		"=== Efficiency Mechanisms ===",
		"hook:commit-msg",
		"yes (calls down, cost and latency not)",
		"no break-even",
		"$0.050000",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("rendered table missing expected text %q", expected)
		}
	}
}

func verifyPositiveRoundTrip(t *testing.T, rep *costledger.Report) {
	t.Helper()
	var jsonBuf bytes.Buffer
	if err := costledger.RenderJSON(rep, &jsonBuf); err != nil {
		t.Fatalf("render json: %v", err)
	}
	var decoded costledger.Report
	if err := json.Unmarshal(jsonBuf.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal json round-trip: %v", err)
	}
	if decoded.SchemaVersion != rep.SchemaVersion || len(decoded.Sessions) != len(rep.Sessions) {
		t.Errorf("round-trip report mismatch: got %d sessions, want %d", len(decoded.Sessions), len(rep.Sessions))
	}
	if decoded.GrandTotal.Tokens != rep.GrandTotal.Tokens || decoded.GrandTotal.SpendMicros != rep.GrandTotal.SpendMicros {
		t.Errorf("round-trip grand total mismatch: got %+v, want %+v", decoded.GrandTotal, rep.GrandTotal)
	}
}

func TestCostLedger_Negative_ValidationErrors(t *testing.T) {
	tests := []struct {
		name       string
		rawJSON    string
		wantErrSub []string
	}{
		{
			name: "missing category",
			rawJSON: `{"schema_version": 1, "entries": [
				{"id": "e_miss", "session": "s1", "tokens": 100, "spend": 0.01, "provenance": "measured"}
			]}`,
			wantErrSub: []string{"e_miss", "missing category"},
		},
		{
			name: "unknown category",
			rawJSON: `{"schema_version": 1, "entries": [
				{"id": "e_unk", "session": "s1", "category": "fantasy", "tokens": 100, "spend": 0.01, "provenance": "measured"}
			]}`,
			wantErrSub: []string{"e_unk", "unknown category", "fantasy"},
		},
		{
			name: "duplicate entry id",
			rawJSON: `{"schema_version": 1, "entries": [
				{"id": "dup1", "session": "s1", "category": "task", "tokens": 100, "spend": 0.01, "provenance": "measured"},
				{"id": "dup1", "session": "s2", "category": "review", "tokens": 200, "spend": 0.02, "provenance": "measured"}
			]}`,
			wantErrSub: []string{"dup1", "duplicate entry id"},
		},
		{
			name: "double counted cost",
			rawJSON: `{"schema_version": 1, "entries": [
				{"id": "e_first", "session": "s1", "category": "task", "tokens": 100, "spend": 0.01, "provenance": "measured"},
				{"id": "e_second", "session": "s1", "category": "task", "tokens": 100, "spend": 0.01, "provenance": "measured"}
			]}`,
			wantErrSub: []string{"double-counted cost", "e_first", "e_second"},
		},
		{
			name: "shared entry with session",
			rawJSON: `{"schema_version": 1, "entries": [
				{"id": "sh_bad", "session": "s1", "category": "rule_context", "tokens": 100, "spend": 0.01, "provenance": "measured", "shared": true}
			]}`,
			wantErrSub: []string{"sh_bad", "shared entry must not set session"},
		},
		{
			name: "non-shared entry missing session",
			rawJSON: `{"schema_version": 1, "entries": [
				{"id": "non_sh_bad", "session": "", "category": "task", "tokens": 100, "spend": 0.01, "provenance": "measured"}
			]}`,
			wantErrSub: []string{"non_sh_bad", "non-shared entry missing session"},
		},
		{
			name: "negative tokens",
			rawJSON: `{"schema_version": 1, "entries": [
				{"id": "neg_tok", "session": "s1", "category": "task", "tokens": -5, "spend": 0.01, "provenance": "measured"}
			]}`,
			wantErrSub: []string{"neg_tok", "negative tokens"},
		},
		{
			name: "negative spend",
			rawJSON: `{"schema_version": 1, "entries": [
				{"id": "neg_sp", "session": "s1", "category": "task", "tokens": 5, "spend": -0.01, "provenance": "measured"}
			]}`,
			wantErrSub: []string{"neg_sp", "negative spend"},
		},
		{
			name: "spend precision",
			rawJSON: `{"schema_version": 1, "entries": [
				{"id": "precise_bad", "session": "s1", "category": "task", "tokens": 5, "spend": 0.1234567, "provenance": "measured"}
			]}`,
			wantErrSub: []string{"precise_bad", "more than 6 decimal places"},
		},
		{
			name: "spend above bound",
			rawJSON: `{"schema_version": 1, "entries": [
				{"id": "spend_big", "session": "s1", "category": "task", "tokens": 5, "spend": 1000000000.000001, "provenance": "measured"}
			]}`,
			wantErrSub: []string{"spend_big", "exceeds bound"},
		},
		{
			name: "tokens above bound",
			rawJSON: `{"schema_version": 1, "entries": [
				{"id": "tokens_big", "session": "s1", "category": "task", "tokens": 1000000000001, "spend": 0.01, "provenance": "measured"}
			]}`,
			wantErrSub: []string{"tokens_big", "exceeds bound"},
		},
		{
			name: "unknown provenance",
			rawJSON: `{"schema_version": 1, "entries": [
				{"id": "unk_prov", "session": "s1", "category": "task", "tokens": 5, "spend": 0.01, "provenance": "guessed"}
			]}`,
			wantErrSub: []string{"unk_prov", "unknown provenance", "guessed"},
		},
		{
			name: "unknown allocation rule",
			rawJSON: `{"schema_version": 1, "allocation": {"rule": "by_magic", "sensitivity_rule": "by_sessions"}, "entries": [
				{"id": "e1", "session": "", "category": "rule_context", "tokens": 5, "spend": 0.01, "provenance": "measured", "shared": true}
			]}`,
			wantErrSub: []string{"unknown allocation rule", "by_magic"},
		},
		{
			name: "unknown sensitivity rule",
			rawJSON: `{"schema_version": 1, "allocation": {"rule": "by_tokens", "sensitivity_rule": "by_chance"}, "entries": [
				{"id": "e1", "session": "", "category": "rule_context", "tokens": 5, "spend": 0.01, "provenance": "measured", "shared": true}
			]}`,
			wantErrSub: []string{"unknown sensitivity rule", "by_chance"},
		},
		{
			name: "sensitivity rule equals rule",
			rawJSON: `{"schema_version": 1, "allocation": {"rule": "by_tokens", "sensitivity_rule": "by_tokens"}, "entries": [
				{"id": "e1", "session": "", "category": "rule_context", "tokens": 5, "spend": 0.01, "provenance": "measured", "shared": true}
			]}`,
			wantErrSub: []string{"sensitivity rule", "cannot equal allocation rule"},
		},
		{
			name: "shared entries present with no rule",
			rawJSON: `{"schema_version": 1, "entries": [
				{"id": "e1", "session": "", "category": "rule_context", "tokens": 5, "spend": 0.01, "provenance": "measured", "shared": true}
			]}`,
			wantErrSub: []string{"shared entries present with no allocation rule"},
		},
		{
			name: "unknown mechanism kind",
			rawJSON: `{"schema_version": 1, "entries": [], "mechanisms": [
				{"id": "m_unk", "kind": "magic_wand", "fixed_cost_tokens": 10, "saving_per_use_tokens": 5, "uses": 1}
			]}`,
			wantErrSub: []string{"m_unk", "unknown kind", "magic_wand"},
		},
		{
			name: "duplicate mechanism id",
			rawJSON: `{"schema_version": 1, "entries": [], "mechanisms": [
				{"id": "m_dup", "kind": "rule", "fixed_cost_tokens": 10, "saving_per_use_tokens": 5, "uses": 1},
				{"id": "m_dup", "kind": "gate", "fixed_cost_tokens": 10, "saving_per_use_tokens": 5, "uses": 1}
			]}`,
			wantErrSub: []string{"m_dup", "duplicate mechanism id"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := costledger.Parse([]byte(tc.rawJSON))
			if err == nil {
				t.Fatalf("expected error containing %v, got nil", tc.wantErrSub)
			}
			msg := err.Error()
			for _, sub := range tc.wantErrSub {
				if !strings.Contains(msg, sub) {
					t.Errorf("expected error to contain %q, got %q", sub, msg)
				}
			}
		})
	}
}

func TestCostLedger_Boundary(t *testing.T) {
	t.Run("zero shared entries requires no allocation rule", testZeroShared)
	t.Run("mechanism with saving zero is no break-even", testSavingZero)
	t.Run("mechanism uses exactly N star reached", testExactNStar)
	t.Run("entry bounds: 10000 pass and 10001 refused", testEntryCountBounds)
	t.Run("spend and token scalar bounds", testScalarBounds)
	t.Run("six decimal spend accepted", testSixDecimalSpend)
	t.Run("spend parsing is exact and shared", testExactSpendParsing)
}

func testZeroShared(t *testing.T) {
	ledger, err := costledger.Load(filepath.Join("testdata", "zero_shared.json"))
	if err != nil {
		t.Fatalf("expected zero_shared to be valid, got: %v", err)
	}
	rep, err := costledger.ComputeReport(ledger)
	if err != nil {
		t.Fatalf("compute report: %v", err)
	}
	if rep.SharedOverhead != nil {
		t.Errorf("expected nil shared overhead, got: %+v", rep.SharedOverhead)
	}
}

func testSavingZero(t *testing.T) {
	ledger, err := costledger.Load(filepath.Join("testdata", "saving_zero.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rep, err := costledger.ComputeReport(ledger)
	if err != nil {
		t.Fatalf("compute report: %v", err)
	}
	if len(rep.Mechanisms) != 1 || rep.Mechanisms[0].BreakEvenStatus != "no break-even" {
		t.Errorf("expected 'no break-even', got: %+v", rep.Mechanisms)
	}
}

func testExactNStar(t *testing.T) {
	ledger, err := costledger.Load(filepath.Join("testdata", "exact_nstar.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rep, err := costledger.ComputeReport(ledger)
	if err != nil {
		t.Fatalf("compute report: %v", err)
	}
	if len(rep.Mechanisms) != 1 || !rep.Mechanisms[0].BreakEvenReached || rep.Mechanisms[0].BreakEvenStatus != "reached" {
		t.Errorf("expected reached, got: %+v", rep.Mechanisms)
	}
}

func testEntryCountBounds(t *testing.T) {
	if err := costledger.Validate(makeBoundedLedger(10000)); err != nil {
		t.Fatalf("expected 10000 entries to pass, got: %v", err)
	}
	if err := costledger.Validate(makeBoundedLedger(10001)); err == nil {
		t.Fatal("expected 10001 entries to fail, got nil error")
	}
}

func testScalarBounds(t *testing.T) {
	raw := `{"schema_version":1,"entries":[{"id":"edge","session":"s1","category":"task","tokens":1000000000000,"spend":100000000,"provenance":"measured"}]}`
	ledger, err := costledger.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse accepted boundaries: %v", err)
	}
	if ledger.Entries[0].SpendMicros != 100_000_000_000_000 {
		t.Fatalf("spend micros = %d", ledger.Entries[0].SpendMicros)
	}
}

func testSixDecimalSpend(t *testing.T) {
	raw := `{"schema_version":1,"entries":[{"id":"six","session":"s1","category":"task","tokens":1,"spend":0.123456,"provenance":"measured"}]}`
	ledger, err := costledger.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse six decimal spend: %v", err)
	}
	if ledger.Entries[0].SpendMicros != 123456 {
		t.Fatalf("spend micros = %d", ledger.Entries[0].SpendMicros)
	}
}

func testExactSpendParsing(t *testing.T) {
	tests := []struct {
		raw       string
		want      costledger.SpendMicros
		wantError string
	}{
		{raw: "27387828.123646", want: 27_387_828_123_646},
		{raw: "54254437.412461", want: 54_254_437_412_461},
		{raw: "54591879.113174", want: 54_591_879_113_174},
		{raw: "1e-05", want: 10},
		{raw: "1.5e2", want: 150_000_000},
		{raw: "0.1234567", wantError: "more than 6 decimal places"},
		{raw: "1e-21", wantError: "exponent"},
		{raw: "11111111111111111111111111111111111111111", wantError: "longer than 40 bytes"},
		{raw: "-0.5", wantError: "negative spend"},
		{raw: "100000000", want: 100_000_000_000_000},
		{raw: "100000000.000001", wantError: "exceeds bound"},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			entrySpend, entryErr := parseSpendEntry(tc.raw)
			var directSpend costledger.SpendMicros
			directErr := json.Unmarshal([]byte(tc.raw), &directSpend)
			assertSpendParseResult(t, entrySpend, entryErr, directSpend, directErr, tc.want, tc.wantError)
		})
	}
}

func parseSpendEntry(raw string) (costledger.SpendMicros, error) {
	document := fmt.Sprintf(
		`{"schema_version":1,"entries":[{"id":"exact","session":"s1","category":"task","tokens":1,"spend":%s,"provenance":"measured"}]}`,
		raw,
	)
	ledger, err := costledger.Parse([]byte(document))
	if err != nil {
		return 0, err
	}
	return ledger.Entries[0].SpendMicros, nil
}

func assertSpendParseResult(
	t *testing.T,
	entrySpend costledger.SpendMicros,
	entryErr error,
	directSpend costledger.SpendMicros,
	directErr error,
	want costledger.SpendMicros,
	wantError string,
) {
	t.Helper()
	if wantError == "" {
		if entryErr != nil || directErr != nil {
			t.Fatalf("entry error = %v, direct error = %v", entryErr, directErr)
		}
		if entrySpend != want || directSpend != want || entrySpend != directSpend {
			t.Fatalf("entry = %d, direct = %d, want %d", entrySpend, directSpend, want)
		}
		return
	}
	if entryErr == nil || directErr == nil {
		t.Fatalf("entry error = %v, direct error = %v", entryErr, directErr)
	}
	if !strings.Contains(entryErr.Error(), "exact") || !strings.Contains(entryErr.Error(), wantError) {
		t.Fatalf("entry error = %q, want entry name and %q", entryErr, wantError)
	}
	if !strings.Contains(directErr.Error(), wantError) {
		t.Fatalf("direct error = %q, want %q", directErr, wantError)
	}
}

func makeBoundedLedger(count int) *costledger.Ledger {
	entries := make([]costledger.Entry, count)
	for i := 0; i < count; i++ {
		entries[i] = costledger.Entry{
			ID:          fmt.Sprintf("e_%d", i),
			Session:     fmt.Sprintf("s_%d", i),
			Category:    costledger.CategoryTask,
			Tokens:      int64(i + 1),
			SpendMicros: costledger.SpendMicros(i+1) * 1000,
			Provenance:  efficiency.ProvenanceMeasured,
		}
	}
	return &costledger.Ledger{
		SchemaVersion: 1,
		Entries:       entries,
	}
}
