// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package costledger

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/efficiency"
)

const (
	// MaxEntries bounds the maximum entries accepted in a cost ledger (HISS-02).
	MaxEntries = 10000

	// MaxMechanisms bounds the maximum mechanisms accepted in a cost ledger (HISS-02).
	MaxMechanisms = 1000

	// MaxEntryTokens keeps 1e12 * 10,000 = 1e16 tokens below int64.
	MaxEntryTokens int64 = 1_000_000_000_000

	// MaxEntrySpend is 1e8 units = 1e14 micro-units; 1e14 * 10,000 = 1e18, below int64.
	MaxEntrySpend int64 = 100_000_000

	// Allocation products are shared <= 1e18 * weight <= 1e18 = 1e36, below 2^128.
	maxEntrySpendMicros  SpendMicros = 100_000_000_000_000
	maxLedgerSpendMicros SpendMicros = 1_000_000_000_000_000_000

	microsPerUnit       = 1_000_000
	maxSpendNumberBytes = 40
	maxSpendExponent    = 20
)

// SpendMicros stores currency in millionths of one unit.
type SpendMicros int64

// MarshalJSON preserves the public decimal currency representation.
func (m SpendMicros) MarshalJSON() ([]byte, error) {
	return []byte(formatSpendMicros(m)), nil
}

// UnmarshalJSON accepts the fixed-point form emitted by MarshalJSON.
func (m *SpendMicros) UnmarshalJSON(data []byte) error {
	parsed, err := parseSpendMicros("", string(data), false, maxEntrySpendMicros)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// SignedSpendMicros stores a signed currency difference in millionths of one unit.
type SignedSpendMicros int64

// MarshalJSON preserves the public decimal currency representation.
func (m SignedSpendMicros) MarshalJSON() ([]byte, error) {
	return []byte(formatSpendMicros(SpendMicros(m))), nil
}

// UnmarshalJSON accepts an exact signed fixed-point currency difference.
func (m *SignedSpendMicros) UnmarshalJSON(data []byte) error {
	parsed, err := parseSpendMicros("", string(data), true, maxLedgerSpendMicros)
	if err != nil {
		return err
	}
	*m = SignedSpendMicros(parsed)
	return nil
}

func formatSpendMicros(micros SpendMicros) string {
	value := int64(micros)
	negative := value < 0
	if negative {
		value = -value
	}
	formatted := fmt.Sprintf("%d.%06d", value/microsPerUnit, value%microsPerUnit)
	if negative {
		return "-" + formatted
	}
	return formatted
}

func parseSpendMicros(
	entryID string,
	raw string,
	allowNegative bool,
	maximum SpendMicros,
) (SpendMicros, error) {
	if raw == "" {
		return 0, nil
	}
	prefix := ""
	if entryID != "" {
		prefix = fmt.Sprintf("entry %q: ", entryID)
	}
	if err := validateSpendText(raw); err != nil {
		return 0, fmt.Errorf("%s%w", prefix, err)
	}
	value, ok := new(big.Rat).SetString(raw)
	if !ok {
		return 0, fmt.Errorf("%sinvalid decimal spend %q", prefix, raw)
	}
	if value.Sign() < 0 && !allowNegative {
		return 0, fmt.Errorf("%snegative spend %q", prefix, raw)
	}
	value.Mul(value, big.NewRat(microsPerUnit, 1))
	if !value.IsInt() {
		return 0, fmt.Errorf("%sspend %q has more than 6 decimal places", prefix, raw)
	}
	absValue := new(big.Rat).Abs(value)
	if absValue.Cmp(big.NewRat(int64(maximum), 1)) > 0 {
		return 0, fmt.Errorf("%sspend %q exceeds bound %d", prefix, raw, int64(maximum)/microsPerUnit)
	}
	return SpendMicros(value.Num().Int64()), nil
}

func validateSpendText(raw string) error {
	if len(raw) > maxSpendNumberBytes {
		return fmt.Errorf("spend number is longer than %d bytes", maxSpendNumberBytes)
	}
	exponentAt := strings.IndexAny(raw, "eE")
	if exponentAt < 0 {
		return nil
	}
	exponent, err := strconv.Atoi(raw[exponentAt+1:])
	if err != nil || exponent < -maxSpendExponent || exponent > maxSpendExponent {
		return fmt.Errorf("spend %q exponent exceeds absolute bound %d", raw, maxSpendExponent)
	}
	return nil
}

// Allowed categories (closed list).
const (
	CategoryTask           = "task"
	CategoryReview         = "review"
	CategoryRetry          = "retry"
	CategoryOrchestration  = "orchestration"
	CategoryMemoryRecall   = "memory_recall"
	CategoryMCPToolSchemas = "mcp_tool_schemas"
	CategoryRuleContext    = "rule_context"
)

// ValidCategories defines the closed list of categories in canonical display order.
var ValidCategories = [...]string{
	CategoryTask,
	CategoryReview,
	CategoryRetry,
	CategoryOrchestration,
	CategoryMemoryRecall,
	CategoryMCPToolSchemas,
	CategoryRuleContext,
}

// IsValidCategory reports whether cat belongs to the closed list of categories.
func IsValidCategory(cat string) bool {
	switch cat {
	case CategoryTask, CategoryReview, CategoryRetry, CategoryOrchestration,
		CategoryMemoryRecall, CategoryMCPToolSchemas, CategoryRuleContext:
		return true
	default:
		return false
	}
}

// Allowed allocation rules (closed list).
const (
	AllocationByTokens   = "by_tokens"
	AllocationBySpend    = "by_spend"
	AllocationBySessions = "by_sessions"
)

// IsValidAllocationRule reports whether rule is one of the three recognized rules.
func IsValidAllocationRule(rule string) bool {
	switch rule {
	case AllocationByTokens, AllocationBySpend, AllocationBySessions:
		return true
	default:
		return false
	}
}

// Allowed mechanism kinds (closed list).
const (
	MechanismKindRule  = "rule"
	MechanismKindGate  = "gate"
	MechanismKindHook  = "hook"
	MechanismKindSkill = "skill"
	MechanismKindCache = "cache"
)

// IsValidMechanismKind reports whether kind is one of the five recognized kinds.
func IsValidMechanismKind(kind string) bool {
	switch kind {
	case MechanismKindRule, MechanismKindGate, MechanismKindHook,
		MechanismKindSkill, MechanismKindCache:
		return true
	default:
		return false
	}
}

// Ledger is the top-level cost ledger input document.
type Ledger struct {
	SchemaVersion int               `json:"schema_version"`
	Allocation    *AllocationConfig `json:"allocation,omitempty"`
	Entries       []Entry           `json:"entries"`
	Mechanisms    []Mechanism       `json:"mechanisms,omitempty"`
}

// AllocationConfig defines the allocation rule and sensitivity rule for shared overhead.
type AllocationConfig struct {
	Rule            string `json:"rule"`
	SensitivityRule string `json:"sensitivity_rule"`
}

// Entry is one cost ledger entry recording tokens, spend, and provenance.
type Entry struct {
	ID          string                `json:"id"`
	Session     string                `json:"session"`
	Category    string                `json:"category"`
	Tokens      int64                 `json:"tokens"`
	SpendMicros SpendMicros           `json:"spend"`
	Provenance  efficiency.Provenance `json:"provenance"`
	Shared      bool                  `json:"shared,omitempty"`
}

// Mechanism models one governance or runtime efficiency mechanism.
type Mechanism struct {
	ID                 string  `json:"id"`
	Kind               string  `json:"kind"`
	FixedCostTokens    int64   `json:"fixed_cost_tokens"`
	SavingPerUseTokens int64   `json:"saving_per_use_tokens"`
	Uses               int64   `json:"uses"`
	CallsDelta         int64   `json:"calls_delta"`
	CostDelta          float64 `json:"cost_delta"`
	LatencyDeltaS      float64 `json:"latency_delta_s"`
}

// CategoryCost holds aggregated tokens and spend for one category with provenance.
type CategoryCost struct {
	Tokens      int64                     `json:"tokens"`
	SpendMicros SpendMicros               `json:"spend"`
	Provenance  string                    `json:"provenance"`
	Mix         *efficiency.ProvenanceMix `json:"provenance_mix,omitempty"`
}

// AllocatedShare represents the allocated share of shared overhead under two rules.
type AllocatedShare struct {
	Rule                   string            `json:"rule"`
	Tokens                 int64             `json:"tokens"`
	SpendMicros            SpendMicros       `json:"spend"`
	SensitivityRule        string            `json:"sensitivity_rule"`
	SensitivityTokens      int64             `json:"sensitivity_tokens"`
	SensitivitySpendMicros SpendMicros       `json:"sensitivity_spend"`
	DifferenceTokens       int64             `json:"difference_tokens"`
	DifferenceSpendMicros  SignedSpendMicros `json:"difference_spend"`
}

// SessionReport details category costs and allocated shared overhead for one session.
type SessionReport struct {
	Session          string                    `json:"session"`
	Categories       map[string]CategoryCost   `json:"categories"`
	OwnTokens        int64                     `json:"own_tokens"`
	OwnSpendMicros   SpendMicros               `json:"own_spend"`
	OwnProvenance    string                    `json:"own_provenance"`
	OwnProvenanceMix *efficiency.ProvenanceMix `json:"own_provenance_mix,omitempty"`
	AllocatedShare   *AllocatedShare           `json:"allocated_share,omitempty"`
	TotalTokens      int64                     `json:"total_tokens"`
	TotalSpendMicros SpendMicros               `json:"total_spend"`
}

// SharedReport details the unallocated pool of shared entries.
type SharedReport struct {
	Tokens      int64                     `json:"tokens"`
	SpendMicros SpendMicros               `json:"spend"`
	Provenance  string                    `json:"provenance"`
	Mix         *efficiency.ProvenanceMix `json:"provenance_mix,omitempty"`
	Categories  map[string]CategoryCost   `json:"categories,omitempty"`
}

// GrandTotal holds the ledger grand total over all entries.
type GrandTotal struct {
	Tokens      int64                     `json:"tokens"`
	SpendMicros SpendMicros               `json:"spend"`
	Provenance  string                    `json:"provenance"`
	Mix         *efficiency.ProvenanceMix `json:"provenance_mix,omitempty"`
}

// MechanismReport reports break-even N* and cosmetic savings status for a mechanism.
type MechanismReport struct {
	ID                 string  `json:"id"`
	Kind               string  `json:"kind"`
	FixedCostTokens    int64   `json:"fixed_cost_tokens"`
	SavingPerUseTokens int64   `json:"saving_per_use_tokens"`
	Uses               int64   `json:"uses"`
	BreakEvenNStar     *int64  `json:"break_even_n_star,omitempty"`
	BreakEvenStatus    string  `json:"break_even_status"`
	BreakEvenReached   bool    `json:"break_even_reached"`
	CallsDelta         int64   `json:"calls_delta"`
	CostDelta          float64 `json:"cost_delta"`
	LatencyDeltaS      float64 `json:"latency_delta_s"`
	Cosmetic           bool    `json:"cosmetic"`
}

// Report is the compiled cost ledger report.
type Report struct {
	SchemaVersion   int                     `json:"schema_version"`
	Rule            string                  `json:"rule,omitempty"`
	SensitivityRule string                  `json:"sensitivity_rule,omitempty"`
	Sessions        []SessionReport         `json:"sessions"`
	SharedOverhead  *SharedReport           `json:"shared_overhead,omitempty"`
	CategoryTotals  map[string]CategoryCost `json:"category_totals"`
	GrandTotal      GrandTotal              `json:"grand_total"`
	Mechanisms      []MechanismReport       `json:"mechanisms,omitempty"`
}

// ProvenanceSummary determines the provenance display label and optional mix.
func ProvenanceSummary(mix efficiency.ProvenanceMix) (string, *efficiency.ProvenanceMix) {
	if mix.Total() == 0 {
		return "not measured", nil
	}
	if mix.Distinct() == 1 {
		return string(mix.Single()), nil
	}
	mCopy := mix
	return fmt.Sprintf("mixed (%s)", mix.String()), &mCopy
}
