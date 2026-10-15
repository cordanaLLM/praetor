// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package costledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"os"
	"slices"
	"sort"

	"github.com/cordanaLLM/praetor/internal/efficiency"
	"github.com/cordanaLLM/praetor/internal/strictjson"
)

const maxLedgerJSONBytes = 32 * 1024 * 1024 // 32MB bounds JSON input (HISS-02)

var ledgerJSONOptions = strictjson.Options{
	MaxBytes:  maxLedgerJSONBytes,
	MaxDepth:  8,
	UseNumber: true,
}

type ledgerInput struct {
	SchemaVersion int               `json:"schema_version"`
	Allocation    *AllocationConfig `json:"allocation,omitempty"`
	Entries       []entryInput      `json:"entries"`
	Mechanisms    []Mechanism       `json:"mechanisms,omitempty"`
}

type entryInput struct {
	ID         string                `json:"id"`
	Session    string                `json:"session"`
	Category   string                `json:"category"`
	Tokens     int64                 `json:"tokens"`
	Spend      json.Number           `json:"spend"`
	Provenance efficiency.Provenance `json:"provenance"`
	Shared     bool                  `json:"shared,omitempty"`
}

// Load reads and parses a cost ledger file from the filesystem.
func Load(path string) (*Ledger, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cost ledger %s: %w", path, err)
	}
	return Parse(data)
}

// Parse decodes strict JSON bytes into a validated Ledger.
func Parse(data []byte) (*Ledger, error) {
	if len(data) == 0 {
		return nil, errors.New("empty JSON data")
	}
	var input ledgerInput
	if err := strictjson.Decode(data, &input, ledgerJSONOptions); err != nil {
		return nil, fmt.Errorf("decode cost ledger JSON: %w", err)
	}
	ledger, err := convertLedger(input)
	if err != nil {
		return nil, err
	}
	if err := Validate(ledger); err != nil {
		return nil, err
	}
	return ledger, nil
}

func convertLedger(input ledgerInput) (*Ledger, error) {
	entries := make([]Entry, 0, len(input.Entries))
	for i := 0; i < len(input.Entries) && i < MaxEntries+1; i++ {
		raw := input.Entries[i]
		spend, err := parseSpendMicros(raw.ID, raw.Spend.String(), false, maxEntrySpendMicros)
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{
			ID: raw.ID, Session: raw.Session, Category: raw.Category,
			Tokens: raw.Tokens, SpendMicros: spend,
			Provenance: raw.Provenance, Shared: raw.Shared,
		})
	}
	return &Ledger{
		SchemaVersion: input.SchemaVersion,
		Allocation:    input.Allocation,
		Entries:       entries,
		Mechanisms:    input.Mechanisms,
	}, nil
}

type categoryAccumulator struct {
	tokens      int64
	spendMicros SpendMicros
	mix         efficiency.ProvenanceMix
}

func (a *categoryAccumulator) add(tokens int64, spend SpendMicros, prov efficiency.Provenance) {
	a.tokens += tokens
	a.spendMicros += spend
	a.mix.Add(prov)
}

func (a *categoryAccumulator) toCost() CategoryCost {
	label, mix := ProvenanceSummary(a.mix)
	return CategoryCost{
		Tokens:      a.tokens,
		SpendMicros: a.spendMicros,
		Provenance:  label,
		Mix:         mix,
	}
}

type sessionAccumulator struct {
	session        string
	categories     map[string]*categoryAccumulator
	ownTokens      int64
	ownSpendMicros SpendMicros
	ownMix         efficiency.ProvenanceMix
}

func newSessionAccumulator(name string) *sessionAccumulator {
	cats := make(map[string]*categoryAccumulator, len(ValidCategories))
	for _, c := range ValidCategories {
		cats[c] = &categoryAccumulator{}
	}
	return &sessionAccumulator{
		session:    name,
		categories: cats,
	}
}

// ComputeReport compiles the complete cost ledger report from a validated ledger.
func ComputeReport(ledger *Ledger) (*Report, error) {
	if ledger == nil {
		return nil, errors.New("cannot compute report for nil ledger")
	}
	sessions, shared, totals := aggregateEntries(ledger.Entries)
	sessionNames := slices.Sorted(slices.Values(makeSessionKeySlice(sessions)))

	rule, sensRule := "", ""
	if ledger.Allocation != nil {
		rule = ledger.Allocation.Rule
		sensRule = ledger.Allocation.SensitivityRule
	}

	sessionReports, err := buildSessionReports(sessionNames, sessions, shared, rule, sensRule)
	if err != nil {
		return nil, err
	}

	catTotals, grandTotal := buildCategoryTotals(totals)

	var sharedReport *SharedReport
	if shared.tokens > 0 || shared.spendMicros > 0 || shared.mix.Total() > 0 {
		sharedReport = buildSharedReport(shared)
	}

	report := &Report{
		SchemaVersion:   ledger.SchemaVersion,
		Rule:            rule,
		SensitivityRule: sensRule,
		Sessions:        sessionReports,
		SharedOverhead:  sharedReport,
		CategoryTotals:  catTotals,
		GrandTotal:      grandTotal,
		Mechanisms:      buildMechanismReports(ledger.Mechanisms),
	}
	if err := checkTotals(ledger.Entries, report); err != nil {
		return nil, err
	}
	return report, nil
}

func makeSessionKeySlice(sessions map[string]*sessionAccumulator) []string {
	keys := make([]string, 0, len(sessions))
	for k := range sessions {
		keys = append(keys, k)
	}
	return keys
}

func aggregateEntries(entries []Entry) (map[string]*sessionAccumulator, *categoryAccumulator, map[string]*categoryAccumulator) {
	sessions := make(map[string]*sessionAccumulator)
	shared := &categoryAccumulator{}
	totals := make(map[string]*categoryAccumulator, len(ValidCategories))
	for _, c := range ValidCategories {
		totals[c] = &categoryAccumulator{}
	}

	for i := 0; i < len(entries) && i < MaxEntries+1; i++ {
		e := entries[i]
		totals[e.Category].add(e.Tokens, e.SpendMicros, e.Provenance)
		if e.Shared {
			shared.add(e.Tokens, e.SpendMicros, e.Provenance)
		} else {
			s, exists := sessions[e.Session]
			if !exists {
				s = newSessionAccumulator(e.Session)
				sessions[e.Session] = s
			}
			s.categories[e.Category].add(e.Tokens, e.SpendMicros, e.Provenance)
			s.ownTokens += e.Tokens
			s.ownSpendMicros += e.SpendMicros
			s.ownMix.Add(e.Provenance)
		}
	}
	return sessions, shared, totals
}

func buildSessionReports(
	names []string,
	sessions map[string]*sessionAccumulator,
	shared *categoryAccumulator,
	rule string,
	sensRule string,
) ([]SessionReport, error) {
	hasShared := shared.tokens > 0 || shared.spendMicros > 0 || shared.mix.Total() > 0
	if hasShared && len(names) == 0 {
		return nil, errors.New("cannot allocate shared overhead: no sessions present in ledger")
	}

	var ruleTokens, ruleSpend, sensTokens, sensSpend []int64
	if hasShared {
		var err error
		ruleTokens, ruleSpend, err = computeRuleShares(
			rule,
			names,
			sessions,
			shared.tokens,
			shared.spendMicros,
		)
		if err != nil {
			return nil, err
		}
		sensTokens, sensSpend, err = computeRuleShares(
			sensRule,
			names,
			sessions,
			shared.tokens,
			shared.spendMicros,
		)
		if err != nil {
			return nil, err
		}
	}

	reports := make([]SessionReport, 0, len(names))
	for i, name := range names {
		s := sessions[name]
		rep := makeSingleSessionReport(
			s,
			i,
			hasShared,
			rule,
			sensRule,
			ruleTokens,
			ruleSpend,
			sensTokens,
			sensSpend,
		)
		reports = append(reports, rep)
	}
	return reports, nil
}

func computeRuleShares(
	rule string,
	names []string,
	sessions map[string]*sessionAccumulator,
	sharedTokens int64,
	sharedSpend SpendMicros,
) ([]int64, []int64, error) {
	var totalOwnTokens int64
	var totalOwnSpend int64
	for _, name := range names {
		s := sessions[name]
		totalOwnTokens += s.ownTokens
		totalOwnSpend += int64(s.ownSpendMicros)
	}
	return allocateShares(
		rule,
		names,
		sessions,
		totalOwnTokens,
		totalOwnSpend,
		sharedTokens,
		int64(sharedSpend),
	)
}

func allocateShares(
	rule string,
	names []string,
	sessions map[string]*sessionAccumulator,
	totalTokens int64,
	totalSpend int64,
	sharedTokens int64,
	sharedSpend int64,
) ([]int64, []int64, error) {
	weights, totalWeight, err := allocationWeights(rule, names, sessions, totalTokens, totalSpend)
	if err != nil {
		return nil, nil, err
	}
	tokenShares := hamiltonShares(names, weights, totalWeight, sharedTokens)
	spendShares := hamiltonShares(names, weights, totalWeight, sharedSpend)
	return tokenShares, spendShares, nil
}

func allocationWeights(
	rule string,
	names []string,
	sessions map[string]*sessionAccumulator,
	totalTokens int64,
	totalSpend int64,
) ([]int64, int64, error) {
	weights := make([]int64, len(names))
	switch rule {
	case AllocationByTokens:
		if totalTokens == 0 {
			return nil, 0, errors.New("cannot allocate by_tokens: total session tokens is 0")
		}
		for i, name := range names {
			weights[i] = sessions[name].ownTokens
		}
		return weights, totalTokens, nil
	case AllocationBySpend:
		if totalSpend == 0 {
			return nil, 0, errors.New("cannot allocate by_spend: total session spend is 0")
		}
		for i, name := range names {
			weights[i] = int64(sessions[name].ownSpendMicros)
		}
		return weights, totalSpend, nil
	case AllocationBySessions:
		if len(names) == 0 {
			return nil, 0, errors.New("cannot allocate by_sessions: no sessions present")
		}
		for i := range names {
			weights[i] = 1
		}
		return weights, int64(len(names)), nil
	default:
		return nil, 0, fmt.Errorf("unknown allocation rule %q", rule)
	}
}

type allocationRemainder struct {
	index     int
	name      string
	remainder uint64
}

func hamiltonShares(names []string, weights []int64, totalWeight, shared int64) []int64 {
	shares := make([]int64, len(names))
	remainders := make([]allocationRemainder, 0, len(names))
	var floorSum int64
	for i, weight := range weights {
		hi, lo := bits.Mul64(uint64(shared), uint64(weight))
		// The quotient is at most shared because weight <= totalWeight, so Div64 cannot panic.
		quotient, remainder := bits.Div64(hi, lo, uint64(totalWeight))
		shares[i] = int64(quotient)
		floorSum += shares[i]
		remainders = append(remainders, allocationRemainder{
			index: i, name: names[i], remainder: remainder,
		})
	}
	sort.Slice(remainders, func(i, j int) bool {
		if remainders[i].remainder == remainders[j].remainder {
			return remainders[i].name < remainders[j].name
		}
		return remainders[i].remainder > remainders[j].remainder
	})
	leftover := shared - floorSum
	for i := 0; i < int(leftover) && i < len(remainders); i++ {
		shares[remainders[i].index]++
	}
	return shares
}

func makeSingleSessionReport(
	s *sessionAccumulator,
	idx int,
	hasShared bool,
	rule string,
	sensRule string,
	rTok []int64,
	rSpend []int64,
	sTok []int64,
	sSpend []int64,
) SessionReport {
	cats := make(map[string]CategoryCost, len(ValidCategories))
	for _, c := range ValidCategories {
		cats[c] = s.categories[c].toCost()
	}
	ownLabel, ownMix := ProvenanceSummary(s.ownMix)
	rep := SessionReport{
		Session:          s.session,
		Categories:       cats,
		OwnTokens:        s.ownTokens,
		OwnSpendMicros:   s.ownSpendMicros,
		OwnProvenance:    ownLabel,
		OwnProvenanceMix: ownMix,
		TotalTokens:      s.ownTokens,
		TotalSpendMicros: s.ownSpendMicros,
	}
	if hasShared {
		diffTok := rTok[idx] - sTok[idx]
		diffSpend := rSpend[idx] - sSpend[idx]
		rep.AllocatedShare = &AllocatedShare{
			Rule:                   rule,
			Tokens:                 rTok[idx],
			SpendMicros:            SpendMicros(rSpend[idx]),
			SensitivityRule:        sensRule,
			SensitivityTokens:      sTok[idx],
			SensitivitySpendMicros: SpendMicros(sSpend[idx]),
			DifferenceTokens:       diffTok,
			DifferenceSpendMicros:  SignedSpendMicros(diffSpend),
		}
		rep.TotalTokens += rTok[idx]
		rep.TotalSpendMicros += SpendMicros(rSpend[idx])
	}
	return rep
}

func buildSharedReport(shared *categoryAccumulator) *SharedReport {
	label, mix := ProvenanceSummary(shared.mix)
	return &SharedReport{
		Tokens:      shared.tokens,
		SpendMicros: shared.spendMicros,
		Provenance:  label,
		Mix:         mix,
	}
}

func buildCategoryTotals(totals map[string]*categoryAccumulator) (map[string]CategoryCost, GrandTotal) {
	catTotals := make(map[string]CategoryCost, len(ValidCategories))
	var grandTokens int64
	var grandSpend SpendMicros
	var grandMix efficiency.ProvenanceMix

	for _, c := range ValidCategories {
		acc := totals[c]
		catTotals[c] = acc.toCost()
		grandTokens += acc.tokens
		grandSpend += acc.spendMicros
		grandMix.Measured += acc.mix.Measured
		grandMix.Modeled += acc.mix.Modeled
		grandMix.Cited += acc.mix.Cited
		grandMix.Interval += acc.mix.Interval
	}

	label, mix := ProvenanceSummary(grandMix)
	return catTotals, GrandTotal{
		Tokens:      grandTokens,
		SpendMicros: grandSpend,
		Provenance:  label,
		Mix:         mix,
	}
}

func checkTotals(entries []Entry, report *Report) error {
	var directTokens int64
	var directSpend SpendMicros
	for i := 0; i < len(entries) && i < MaxEntries+1; i++ {
		directTokens += entries[i].Tokens
		directSpend += entries[i].SpendMicros
	}
	if report.GrandTotal.Tokens != directTokens || report.GrandTotal.SpendMicros != directSpend {
		return fmt.Errorf(
			"internal error: grand total (%d, %s) != entry total (%d, %s)",
			report.GrandTotal.Tokens, formatSpendMicros(report.GrandTotal.SpendMicros),
			directTokens, formatSpendMicros(directSpend),
		)
	}
	var partitionTokens int64
	var partitionSpend SpendMicros
	for _, session := range report.Sessions {
		partitionTokens += session.OwnTokens
		partitionSpend += session.OwnSpendMicros
	}
	if report.SharedOverhead != nil {
		partitionTokens += report.SharedOverhead.Tokens
		partitionSpend += report.SharedOverhead.SpendMicros
	}
	if partitionTokens != directTokens || partitionSpend != directSpend {
		return fmt.Errorf(
			"internal error: own plus shared total (%d, %s) != entry total (%d, %s)",
			partitionTokens, formatSpendMicros(partitionSpend),
			directTokens, formatSpendMicros(directSpend),
		)
	}
	return nil
}

func buildMechanismReports(mechanisms []Mechanism) []MechanismReport {
	reports := make([]MechanismReport, 0, len(mechanisms))
	for i := 0; i < len(mechanisms) && i < MaxMechanisms+1; i++ {
		m := mechanisms[i]
		rep := evaluateMechanism(m)
		reports = append(reports, rep)
	}
	return reports
}

func evaluateMechanism(m Mechanism) MechanismReport {
	var nStar *int64
	var status string
	var reached bool

	if m.SavingPerUseTokens <= 0 {
		status = "no break-even"
	} else {
		val := int64(0)
		if m.FixedCostTokens > 0 {
			val = (m.FixedCostTokens + m.SavingPerUseTokens - 1) / m.SavingPerUseTokens
		}
		nStar = &val
		reached = m.Uses >= val
		if reached {
			status = "reached"
		} else {
			status = "not reached"
		}
	}

	cosmetic := m.CallsDelta < 0 && m.CostDelta >= 0 && m.LatencyDeltaS >= 0

	return MechanismReport{
		ID:                 m.ID,
		Kind:               m.Kind,
		FixedCostTokens:    m.FixedCostTokens,
		SavingPerUseTokens: m.SavingPerUseTokens,
		Uses:               m.Uses,
		BreakEvenNStar:     nStar,
		BreakEvenStatus:    status,
		BreakEvenReached:   reached,
		CallsDelta:         m.CallsDelta,
		CostDelta:          m.CostDelta,
		LatencyDeltaS:      m.LatencyDeltaS,
		Cosmetic:           cosmetic,
	}
}
