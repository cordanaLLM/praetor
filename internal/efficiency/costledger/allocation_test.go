// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package costledger

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/efficiency"
)

func TestAllocateSharesHamilton(t *testing.T) {
	names := []string{"a", "b", "c"}
	tests := []struct {
		name       string
		rule       string
		tokens     []int64
		spend      []int64
		want       []int64
		totalToken int64
		totalSpend int64
	}{
		{
			name: "sessions tie goes to name",
			rule: AllocationBySessions, tokens: []int64{8, 4, 2},
			spend: []int64{7, 5, 3}, want: []int64{4, 3, 3},
			totalToken: 14, totalSpend: 15,
		},
		{
			name: "token tie goes to name",
			rule: AllocationByTokens, tokens: []int64{1, 1, 1},
			spend: []int64{7, 5, 3}, want: []int64{4, 3, 3},
			totalToken: 3, totalSpend: 15,
		},
		{
			name: "spend two largest remainders",
			rule: AllocationBySpend, tokens: []int64{7, 5, 3},
			spend: []int64{1, 2, 4}, want: []int64{1, 3, 6},
			totalToken: 15, totalSpend: 7,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sessions := testSessions(names, tc.tokens, tc.spend)
			tokens, spend, err := allocateShares(
				tc.rule, names, sessions, tc.totalToken, tc.totalSpend, 10, 10,
			)
			if err != nil {
				t.Fatalf("allocate shares: %v", err)
			}
			if !slices.Equal(tokens, tc.want) || !slices.Equal(spend, tc.want) {
				t.Fatalf("shares tokens=%v spend=%v, want %v", tokens, spend, tc.want)
			}
			if sumShares(tokens) != 10 || sumShares(spend) != 10 {
				t.Fatalf("shares do not sum exactly: tokens=%v spend=%v", tokens, spend)
			}
		})
	}
}

func TestAllocateSharesOverflowBoundary(t *testing.T) {
	names := []string{"a", "b", "c"}
	weights := []int64{MaxEntryTokens, MaxEntryTokens, MaxEntryTokens}
	sessions := testSessions(names, weights, weights)
	tokens, spend, err := allocateShares(
		AllocationByTokens,
		names,
		sessions,
		3*MaxEntryTokens,
		3*MaxEntryTokens,
		MaxEntryTokens,
		MaxEntryTokens,
	)
	if err != nil {
		t.Fatalf("allocate overflow boundary: %v", err)
	}
	want := []int64{333_333_333_334, 333_333_333_333, 333_333_333_333}
	if !slices.Equal(tokens, want) || !slices.Equal(spend, want) {
		t.Fatalf("shares tokens=%v spend=%v, want %v", tokens, spend, want)
	}
	if sumShares(tokens) != MaxEntryTokens || sumShares(spend) != MaxEntryTokens {
		t.Fatal("overflow boundary shares do not sum exactly")
	}
}

func TestReportOverflowBoundary(t *testing.T) {
	entries := makeMaxBoundEntries()
	ledger := &Ledger{
		SchemaVersion: 1,
		Allocation: &AllocationConfig{
			Rule: AllocationBySpend, SensitivityRule: AllocationByTokens,
		},
		Entries: entries,
	}
	report, err := ComputeReport(ledger)
	if err != nil {
		t.Fatalf("compute maximum-bound report: %v", err)
	}
	if err := checkTotals(entries, report); err != nil {
		t.Fatalf("check maximum-bound totals: %v", err)
	}
	assertMaximumBoundReport(t, report)
}

func makeMaxBoundEntries() []Entry {
	entries := make([]Entry, MaxEntries)
	for i := 0; i < MaxEntries; i++ {
		shared := i%4 == 3
		session := fmt.Sprintf("session-%d", i%3)
		if shared {
			session = ""
		}
		entries[i] = Entry{
			ID: fmt.Sprintf("max-%d", i), Session: session, Category: CategoryTask,
			Tokens: MaxEntryTokens, SpendMicros: maxEntrySpendMicros,
			Provenance: efficiency.ProvenanceMeasured, Shared: shared,
		}
	}
	return entries
}

func assertMaximumBoundReport(t *testing.T, report *Report) {
	t.Helper()
	if len(report.Sessions) != 3 {
		t.Fatalf("session count = %d, want 3", len(report.Sessions))
	}
	wantTokens := int64(MaxEntries) * MaxEntryTokens
	entrySpend := maxEntrySpendMicros
	wantSpend := SpendMicros(MaxEntries) * entrySpend
	if report.GrandTotal.Tokens != wantTokens || report.GrandTotal.SpendMicros != wantSpend {
		t.Fatalf("grand total = (%d, %d), want (%d, %d)",
			report.GrandTotal.Tokens, report.GrandTotal.SpendMicros, wantTokens, wantSpend)
	}
	var allocatedTokens int64
	var allocatedSpend SpendMicros
	var sensitivityTokens int64
	var sensitivitySpend SpendMicros
	for _, session := range report.Sessions {
		allocatedTokens += session.AllocatedShare.Tokens
		allocatedSpend += session.AllocatedShare.SpendMicros
		sensitivityTokens += session.AllocatedShare.SensitivityTokens
		sensitivitySpend += session.AllocatedShare.SensitivitySpendMicros
	}
	if report.SharedOverhead == nil ||
		allocatedTokens != report.SharedOverhead.Tokens ||
		allocatedSpend != report.SharedOverhead.SpendMicros ||
		sensitivityTokens != report.SharedOverhead.Tokens ||
		sensitivitySpend != report.SharedOverhead.SpendMicros {
		t.Fatalf("shares = (%d, %d) sensitivity = (%d, %d), shared = %+v",
			allocatedTokens, allocatedSpend, sensitivityTokens, sensitivitySpend, report.SharedOverhead)
	}
}

func TestAllocateSharesZeroWeight(t *testing.T) {
	names := []string{"a"}
	sessions := testSessions(names, []int64{0}, []int64{0})
	if _, _, err := allocateShares(
		AllocationByTokens, names, sessions, 0, 0, 10, 10,
	); err == nil || !strings.Contains(err.Error(), "total session tokens is 0") {
		t.Fatalf("zero token weight error = %v", err)
	}
	if _, _, err := allocateShares(
		AllocationBySpend, names, sessions, 0, 0, 10, 10,
	); err == nil || !strings.Contains(err.Error(), "total session spend is 0") {
		t.Fatalf("zero spend weight error = %v", err)
	}
}

func TestCheckTotals(t *testing.T) {
	entries := []Entry{
		{Tokens: 7, SpendMicros: 11},
		{Tokens: 5, SpendMicros: 13, Shared: true},
	}
	report := &Report{
		Sessions:       []SessionReport{{Session: "a", OwnTokens: 7, OwnSpendMicros: 11}},
		SharedOverhead: &SharedReport{Tokens: 5, SpendMicros: 13},
		GrandTotal:     GrandTotal{Tokens: 12, SpendMicros: 24},
	}
	if err := checkTotals(entries, report); err != nil {
		t.Fatalf("correct totals: %v", err)
	}

	wrongGrand := *report
	wrongGrand.GrandTotal.Tokens++
	if err := checkTotals(entries, &wrongGrand); err == nil || !strings.Contains(err.Error(), "grand total") {
		t.Fatalf("wrong grand token total error = %v", err)
	}

	wrongOwn := *report
	wrongOwn.Sessions = slices.Clone(report.Sessions)
	wrongOwn.Sessions[0].OwnSpendMicros++
	if err := checkTotals(entries, &wrongOwn); err == nil || !strings.Contains(err.Error(), "own plus shared") {
		t.Fatalf("wrong own spend total error = %v", err)
	}
}

func testSessions(names []string, tokens, spend []int64) map[string]*sessionAccumulator {
	sessions := make(map[string]*sessionAccumulator, len(names))
	for i, name := range names {
		sessions[name] = &sessionAccumulator{
			session: name, ownTokens: tokens[i], ownSpendMicros: SpendMicros(spend[i]),
		}
	}
	return sessions
}

func sumShares(shares []int64) int64 {
	var total int64
	for _, share := range shares {
		total += share
	}
	return total
}
