// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package costledger

import (
	"errors"
	"fmt"
	"math"

	"github.com/cordanaLLM/praetor/internal/efficiency"
)

type doubleCountKey struct {
	session  string
	category string
	tokens   int64
	spend    SpendMicros
}

// Validate checks the ledger against all high-integrity constraints.
func Validate(ledger *Ledger) error {
	if ledger == nil {
		return errors.New("cannot validate nil ledger")
	}
	if err := validateBounds(ledger); err != nil {
		return err
	}
	hasShared, err := validateEntries(ledger.Entries)
	if err != nil {
		return err
	}
	if err := validateMechanisms(ledger.Mechanisms); err != nil {
		return err
	}
	return validateAllocation(ledger.Allocation, hasShared)
}

func validateBounds(ledger *Ledger) error {
	if ledger.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schema_version %d (expected 1)", ledger.SchemaVersion)
	}
	if len(ledger.Entries) > MaxEntries {
		return fmt.Errorf("entry count %d exceeds bound %d", len(ledger.Entries), MaxEntries)
	}
	if len(ledger.Mechanisms) > MaxMechanisms {
		return fmt.Errorf("mechanism count %d exceeds bound %d", len(ledger.Mechanisms), MaxMechanisms)
	}
	return nil
}

func validateEntryFields(e Entry) error {
	if err := validateEntryIdentity(e); err != nil {
		return err
	}
	return validateEntryValues(e)
}

func validateEntryIdentity(e Entry) error {
	if e.ID == "" {
		return errors.New("entry missing id")
	}
	if e.Category == "" {
		return fmt.Errorf("entry %q: missing category (unassigned cost)", e.ID)
	}
	if !IsValidCategory(e.Category) {
		return fmt.Errorf("entry %q: unknown category %q (unassigned cost)", e.ID, e.Category)
	}
	if e.Shared && e.Session != "" {
		return fmt.Errorf("entry %q: shared entry must not set session (got %q)", e.ID, e.Session)
	}
	if !e.Shared && e.Session == "" {
		return fmt.Errorf("entry %q: non-shared entry missing session", e.ID)
	}
	return nil
}

func validateEntryValues(e Entry) error {
	if e.Tokens < 0 {
		return fmt.Errorf("entry %q: negative tokens %d", e.ID, e.Tokens)
	}
	if e.Tokens > MaxEntryTokens {
		return fmt.Errorf("entry %q: tokens %d exceeds bound %d", e.ID, e.Tokens, MaxEntryTokens)
	}
	if e.SpendMicros < 0 {
		return fmt.Errorf("entry %q: negative spend %s", e.ID, formatSpendMicros(e.SpendMicros))
	}
	if e.SpendMicros > maxEntrySpendMicros {
		return fmt.Errorf("entry %q: spend %s exceeds bound %d", e.ID, formatSpendMicros(e.SpendMicros), MaxEntrySpend)
	}
	if !efficiency.ValidProvenance(e.Provenance) {
		return fmt.Errorf("entry %q: unknown provenance %q", e.ID, e.Provenance)
	}
	return nil
}

func validateEntries(entries []Entry) (bool, error) {
	seenIDs := make(map[string]bool, len(entries))
	seenTuples := make(map[doubleCountKey]string, len(entries))
	var hasShared bool
	var totalSpend SpendMicros

	for i := 0; i < len(entries) && i < MaxEntries+1; i++ {
		e := entries[i]
		if err := validateEntryFields(e); err != nil {
			return false, err
		}
		if seenIDs[e.ID] {
			return false, fmt.Errorf("entry %q: duplicate entry id", e.ID)
		}
		seenIDs[e.ID] = true

		if e.SpendMicros > SpendMicros(math.MaxInt64)-totalSpend {
			return false, fmt.Errorf("entry %q: cumulative spend exceeds int64 micro-unit range", e.ID)
		}
		totalSpend += e.SpendMicros

		key := doubleCountKey{
			session: e.Session, category: e.Category,
			tokens: e.Tokens, spend: e.SpendMicros,
		}
		if prevID, exists := seenTuples[key]; exists {
			return false, fmt.Errorf("double-counted cost between entry %q and %q: same session, category, tokens, spend", prevID, e.ID)
		}
		seenTuples[key] = e.ID

		if e.Shared {
			hasShared = true
		}
	}
	return hasShared, nil
}

func validateMechanisms(mechanisms []Mechanism) error {
	seenIDs := make(map[string]bool, len(mechanisms))
	for i := 0; i < len(mechanisms) && i < MaxMechanisms+1; i++ {
		m := mechanisms[i]
		if m.ID == "" {
			return errors.New("mechanism missing id")
		}
		if seenIDs[m.ID] {
			return fmt.Errorf("mechanism %q: duplicate mechanism id", m.ID)
		}
		seenIDs[m.ID] = true
		if !IsValidMechanismKind(m.Kind) {
			return fmt.Errorf("mechanism %q: unknown kind %q", m.ID, m.Kind)
		}
		if m.FixedCostTokens < 0 {
			return fmt.Errorf("mechanism %q: negative fixed_cost_tokens %d", m.ID, m.FixedCostTokens)
		}
		if m.Uses < 0 {
			return fmt.Errorf("mechanism %q: negative uses %d", m.ID, m.Uses)
		}
	}
	return nil
}

func validateAllocation(alloc *AllocationConfig, hasShared bool) error {
	if hasShared {
		return validateSharedAllocation(alloc)
	}
	return validateOptionalAllocation(alloc)
}

func validateSharedAllocation(alloc *AllocationConfig) error {
	if alloc == nil || alloc.Rule == "" {
		return errors.New("shared entries present with no allocation rule")
	}
	if !IsValidAllocationRule(alloc.Rule) {
		return fmt.Errorf("unknown allocation rule %q", alloc.Rule)
	}
	if alloc.SensitivityRule == "" {
		return errors.New("shared entries present with no sensitivity rule")
	}
	if !IsValidAllocationRule(alloc.SensitivityRule) {
		return fmt.Errorf("unknown sensitivity rule %q", alloc.SensitivityRule)
	}
	if alloc.Rule == alloc.SensitivityRule {
		return fmt.Errorf("sensitivity rule %q cannot equal allocation rule %q", alloc.SensitivityRule, alloc.Rule)
	}
	return nil
}

func validateOptionalAllocation(alloc *AllocationConfig) error {
	if alloc == nil {
		return nil
	}
	if alloc.Rule != "" && !IsValidAllocationRule(alloc.Rule) {
		return fmt.Errorf("unknown allocation rule %q", alloc.Rule)
	}
	if alloc.SensitivityRule != "" && !IsValidAllocationRule(alloc.SensitivityRule) {
		return fmt.Errorf("unknown sensitivity rule %q", alloc.SensitivityRule)
	}
	if alloc.Rule != "" && alloc.SensitivityRule != "" && alloc.Rule == alloc.SensitivityRule {
		return fmt.Errorf("sensitivity rule %q cannot equal allocation rule %q", alloc.SensitivityRule, alloc.Rule)
	}
	return nil
}
