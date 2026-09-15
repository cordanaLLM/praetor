// Package hisscoverage records which HISS invariants are actually enforced, per language,
// and verifies each claim by replaying a fixture corpus.
//
// The HISS weakness audit of 2026-09-14 found the same defect fourteen times: a matrix cell
// asserting a mechanism no executable check implements. HISS-01 advertised an "AST
// call-graph analyzer" and matched only `goto`; HISS-12 named gitleaks and ran it nowhere;
// HISS-03, HISS-05 and HISS-06 named mechanisms that cannot decide their axiom. Nothing in
// the repository compared a declared claim against observed behaviour, so the claims drifted
// freely.
//
// This package makes the comparison mechanical. A claim is only as good as the fixture that
// demonstrates it, and a claim of *absence* is held to the same standard as a claim of
// enforcement: a rule declared unsupported must leave its gap fixtures undetected, so
// silently gaining coverage fails the gate exactly as silently losing it does.
package hisscoverage

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// State is the evidence state of one invariant in one language.
type State string

const (
	// StateEnforced claims every shape the corpus describes is reported.
	StateEnforced State = "enforced"
	// StatePartial claims some shapes are reported and names the rest as gaps.
	StatePartial State = "partial"
	// StateUnsupported claims nothing is reported for this language. Its gap fixtures
	// record what goes undetected, so the absence is documented rather than implied.
	StateUnsupported State = "unsupported"
	// StateNotApplicable claims the axiom does not apply to the language at all.
	StateNotApplicable State = "not_applicable"
	// StateManual claims the invariant is upheld by review rather than by a runnable
	// check. It is not a gate and must never be reported as one.
	StateManual State = "manual"
)

// ErrUnknownState reports a state outside the declared vocabulary.
var ErrUnknownState = errors.New("hisscoverage: unknown evidence state")

// ErrInvalidCatalog reports a catalog that cannot be trusted to describe enforcement.
var ErrInvalidCatalog = errors.New("hisscoverage: invalid catalog")

// validStates is the closed vocabulary. A state outside it is a catalog error rather than a
// silently ignored value, because an unrecognised state would otherwise read as coverage.
var validStates = map[State]struct{}{
	StateEnforced:      {},
	StatePartial:       {},
	StateUnsupported:   {},
	StateNotApplicable: {},
	StateManual:        {},
}

// Valid reports whether s is a declared evidence state.
func (s State) Valid() bool {
	_, ok := validStates[s]
	return ok
}

// ClaimsDetection reports whether the state promises that a positive fixture is reported.
// Only enforced and partial do; the rest describe an absence of enforcement and are verified
// by their gap fixtures going undetected.
func (s State) ClaimsDetection() bool {
	return s == StateEnforced || s == StatePartial
}

// ReplayedHere reports whether this package can replay the claim's verdicts directly, which
// is true only for the HISS scanner. A delegated claim is still checked, but for attribution.
func (c Coverage) ReplayedHere() bool {
	return c.Runner == "" || c.Runner == RunnerHISS
}

// RunnerHISS is the default runner: the HISS scanner itself, which this package replays.
const RunnerHISS = "hiss"

// Coverage is one invariant's evidence in one language.
type Coverage struct {
	Language string `yaml:"language" json:"language"`
	State    State  `yaml:"state" json:"state"`
	// Runner names the tool that decides this rule. Empty means the HISS scanner, whose
	// verdicts this package replays directly. Any other value -- golangci-lint, gitleaks,
	// the forge commit check, a CI step -- is a delegated claim: the fixtures are still
	// replayed, but against the opposite expectation, because what can be verified here is
	// the *attribution* rather than the enforcement. If the scanner reports a fixture whose
	// rule is attributed elsewhere, the attribution is wrong and the catalog says so.
	Runner string `yaml:"runner,omitempty" json:"runner,omitempty"`
	// Mechanism names what actually decides the rule: internal/hiss, golangci-lint,
	// semgrep, a Makefile target, or none. A state above unsupported without a mechanism
	// is the exact defect this package exists to catch.
	Mechanism string `yaml:"mechanism" json:"mechanism"`
	// Rationale is the measurement that justifies the state, in one sentence.
	Rationale string `yaml:"rationale" json:"rationale"`
}

// Rule is one invariant's declared evidence across languages.
type Rule struct {
	ID       string     `yaml:"id" json:"id"`
	Title    string     `yaml:"title" json:"title"`
	Coverage []Coverage `yaml:"coverage" json:"coverage"`
}

// Catalog is the declared enforcement evidence for every invariant.
type Catalog struct {
	Version int    `yaml:"version" json:"version"`
	Rules   []Rule `yaml:"rules" json:"rules"`
}

// Validate refuses a catalog that could misreport enforcement.
func (c *Catalog) Validate() error {
	if c == nil {
		return fmt.Errorf("%w: catalog is absent", ErrInvalidCatalog)
	}
	if c.Version != 1 {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidCatalog, c.Version)
	}
	if len(c.Rules) == 0 {
		return fmt.Errorf("%w: no rules declared", ErrInvalidCatalog)
	}
	seen := make(map[string]struct{}, len(c.Rules))
	for i := 0; i < len(c.Rules); i++ {
		if err := validateRule(&c.Rules[i], seen); err != nil {
			return err
		}
	}
	return nil
}

// validateRule checks one rule's identity and its per-language evidence.
func validateRule(rule *Rule, seen map[string]struct{}) error {
	if strings.TrimSpace(rule.ID) == "" {
		return fmt.Errorf("%w: a rule has no id", ErrInvalidCatalog)
	}
	if _, dup := seen[rule.ID]; dup {
		return fmt.Errorf("%w: rule %s declared twice", ErrInvalidCatalog, rule.ID)
	}
	seen[rule.ID] = struct{}{}
	if len(rule.Coverage) == 0 {
		return fmt.Errorf("%w: rule %s declares no coverage", ErrInvalidCatalog, rule.ID)
	}
	langs := make(map[string]struct{}, len(rule.Coverage))
	for i := 0; i < len(rule.Coverage); i++ {
		if err := validateCoverage(rule.ID, &rule.Coverage[i], langs); err != nil {
			return err
		}
	}
	return nil
}

// validateCoverage checks one language entry. A claim of enforcement without a named
// mechanism is refused: that combination is how a matrix cell comes to advertise a gate that
// does not exist.
func validateCoverage(ruleID string, cov *Coverage, langs map[string]struct{}) error {
	if strings.TrimSpace(cov.Language) == "" {
		return fmt.Errorf("%w: rule %s has coverage with no language", ErrInvalidCatalog, ruleID)
	}
	if _, dup := langs[cov.Language]; dup {
		return fmt.Errorf("%w: rule %s declares %s twice", ErrInvalidCatalog, ruleID, cov.Language)
	}
	langs[cov.Language] = struct{}{}
	if !cov.State.Valid() {
		return fmt.Errorf("%w: rule %s language %s: %w %q",
			ErrInvalidCatalog, ruleID, cov.Language, ErrUnknownState, cov.State)
	}
	if cov.State.ClaimsDetection() && strings.TrimSpace(cov.Mechanism) == "" {
		return fmt.Errorf("%w: rule %s language %s claims %s without naming a mechanism",
			ErrInvalidCatalog, ruleID, cov.Language, cov.State)
	}
	if strings.TrimSpace(cov.Rationale) == "" {
		return fmt.Errorf("%w: rule %s language %s states %s without a rationale",
			ErrInvalidCatalog, ruleID, cov.Language, cov.State)
	}
	return nil
}

// RuleIDs returns the declared invariant identifiers in ascending order.
func (c *Catalog) RuleIDs() []string {
	if c == nil {
		return nil
	}
	ids := make([]string, 0, len(c.Rules))
	for i := 0; i < len(c.Rules); i++ {
		ids = append(ids, c.Rules[i].ID)
	}
	sort.Strings(ids)
	return ids
}

// Lookup returns the coverage declared for one invariant in one language.
func (c *Catalog) Lookup(ruleID, language string) (Coverage, bool) {
	if c == nil {
		return Coverage{}, false
	}
	for i := 0; i < len(c.Rules); i++ {
		if c.Rules[i].ID != ruleID {
			continue
		}
		for j := 0; j < len(c.Rules[i].Coverage); j++ {
			if c.Rules[i].Coverage[j].Language == language {
				return c.Rules[i].Coverage[j], true
			}
		}
		return Coverage{}, false
	}
	return Coverage{}, false
}

// Summary counts declared languages by state, for a report that does not imply more
// enforcement than the catalog claims.
func (c *Catalog) Summary() map[State]int {
	counts := make(map[State]int, len(validStates))
	if c == nil {
		return counts
	}
	for i := 0; i < len(c.Rules); i++ {
		for j := 0; j < len(c.Rules[i].Coverage); j++ {
			counts[c.Rules[i].Coverage[j].State]++
		}
	}
	return counts
}
