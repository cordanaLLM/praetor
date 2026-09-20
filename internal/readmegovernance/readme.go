// Package readmegovernance owns the bounded, marker-delimited governance block that
// Praetor writes into an adopter's README. Adoption and audit share this package so the
// renderer cannot drift from the verifier.
package readmegovernance

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// Start and End delimit the only README region Praetor owns.
	Start = "<!-- praetor:readme-governance:start -->"
	End   = "<!-- praetor:readme-governance:end -->"
)

var (
	// ErrMissing is returned when audit expects a managed block but none exists.
	ErrMissing = errors.New("managed README governance block is missing")
	// ErrStale is returned when the managed block or a legacy generated surface differs
	// from the current deterministic rendering.
	ErrStale = errors.New("managed README governance block is stale")
	// ErrInvalidState is returned for an impossible renderer input.
	ErrInvalidState = errors.New("README governance state is invalid")

	legacyBadge = regexp.MustCompile(`(?mi)^\[!\[HISS(?:-16)?[[:space:]]+(?:Compliant|Adopted)\]\(https://img\.shields\.io/badge/Standards-HISS(?:--16)?%20(?:Compliant|Adopted)(?:%20\([0-9]+%20baselined\))?-(?:brightgreen|yellow)\)\]\([^\r\n]+\)[[:space:]]*\r?\n?`)
	customBadge = regexp.MustCompile(`(?mi)^.*\[!\[[^\]]*HISS[^\]]*\]\(https://img\.shields\.io/badge/[^\r\n]*HISS[^\r\n]*\).*$`)
)

const legacyGovernanceCurrent = `

## Standards & Governance

This repository conforms to the High-Integrity Systems Standard (HISS)
and modernized NASA JPL Power-of-10 rules.

| Gate | Command | Description |
| :--- | :--- | :--- |
| **Verification** | ` + "`make verify-all`" + ` | Runs full audit, test suite, and context integrity check |
| **HISS Audit** | ` + "`praetorctl audit`" + ` | Enforces zero technical debt regression against baseline |
| **Context Sync** | ` + "`praetorctl compile-context`" + ` | Transpiles canonical ` + "`AGENTS.md`" + ` to all AI targets |
`

const legacyGovernanceHISS16 = `

## Standards & Governance

This repository conforms to High-Integrity Systems Standards (HISS-16)
and modernized NASA JPL Power-of-10 rules.

| Gate | Command | Description |
| :--- | :--- | :--- |
| **Verification** | ` + "`make verify-all`" + ` | Runs full audit, test suite, and context integrity check |
| **HISS Audit** | ` + "`standardsctl audit`" + ` | Enforces zero technical debt regression against baseline |
| **Context Sync** | ` + "`standardsctl compile-context`" + ` | Transpiles canonical ` + "`AGENTS.md`" + ` to all AI targets |
`

// State is the durable evidence adoption can truthfully render. A baseline is a debt
// anchor, not proof that the repository's full verification gate passed.
type State struct {
	BaselineKnown   bool
	LegacyDebtCount int
}

// Reconcile returns content with one current managed block. Human-authored content and
// custom HISS badges remain outside the block and are preserved.
func Reconcile(content string, state State) (string, bool, error) {
	if state.LegacyDebtCount < 0 {
		return "", false, fmt.Errorf("%w: negative legacy debt count", ErrInvalidState)
	}
	first, last, err := util.FindMarkedBlock(content, Start, End)
	if err != nil {
		return "", false, fmt.Errorf("README governance markers: %w", err)
	}
	var cleaned string
	if first >= 0 {
		cleaned = cleanOutsideManagedBlock(content, first, last)
	} else {
		cleaned = stripLegacyGenerated(content)
	}
	block := renderBlock(state, hasCustomBadgeOutsideBlock(cleaned))
	if first >= 0 {
		out, _, replaceErr := util.ReplaceMarkedBlock(cleaned, Start, End, block, util.MaxMarkedBlockLines)
		if replaceErr != nil {
			return "", false, fmt.Errorf("replace README governance block: %w", replaceErr)
		}
		return out, out != content, nil
	}
	out := insertManagedBlock(cleaned, block)
	return out, out != content, nil
}

// Verify accepts only the exact output Reconcile would produce for state.
func Verify(content string, state State) error {
	first, _, err := util.FindMarkedBlock(content, Start, End)
	if err != nil {
		return fmt.Errorf("README governance markers: %w", err)
	}
	if first < 0 {
		return ErrMissing
	}
	out, changed, err := Reconcile(content, state)
	if err != nil {
		return err
	}
	if changed || out != content {
		return ErrStale
	}
	return nil
}

func cleanOutsideManagedBlock(content string, first, last int) string {
	lines := strings.Split(content, "\n")
	prefix := stripLegacyGenerated(strings.Join(lines[:first], "\n"))
	suffix := stripLegacyGenerated(strings.Join(lines[last+1:], "\n"))
	parts := []string{prefix, Start, strings.Join(lines[first+1:last], "\n"), End, suffix}
	return strings.Join(parts, "\n")
}

func stripLegacyGenerated(content string) string {
	content = legacyBadge.ReplaceAllString(content, "")
	for _, legacy := range []string{legacyGovernanceCurrent, legacyGovernanceHISS16} {
		content = strings.ReplaceAll(content, strings.Trim(legacy, "\n"), "")
	}
	return content
}

func hasCustomBadgeOutsideBlock(content string) bool {
	first, last, err := util.FindMarkedBlock(content, Start, End)
	if err != nil {
		return false
	}
	if first >= 0 {
		lines := strings.Split(content, "\n")
		content = strings.Join(append(append([]string{}, lines[:first]...), lines[last+1:]...), "\n")
	}
	return customBadge.MatchString(content)
}

func renderBlock(state State, customHISSBadge bool) string {
	var lines []string
	lines = append(lines, Start)
	if !customHISSBadge {
		lines = append(lines, renderBadge(state), "")
	}
	lines = append(lines,
		"Praetor manages this repository's declared governance policy. This managed block records adoption state; it is not a verification certificate.",
		"",
		"| Gate | Command | Contract |",
		"| :--- | :--- | :--- |",
		"| **Verification** | `make verify-all` | Runs the repository's configured verification cascade |",
		"| **HISS Audit** | `praetorctl audit` | Enforces policy, generated-surface integrity, and the debt ratchet |",
		"| **Context Sync** | `praetorctl compile-context --verify` | Verifies every generated agent context against `AGENTS.md` |",
		fmt.Sprintf("| **Debt Baseline** | `.standards-baseline.json` | %s |", baselineDescription(state)),
		End,
	)
	return strings.Join(lines, "\n")
}

func renderBadge(state State) string {
	switch {
	case !state.BaselineKnown:
		return "[![HISS Adopted](https://img.shields.io/badge/Standards-HISS%20Adopted%20(baseline%20pending)-yellow)](AGENTS.md)"
	case state.LegacyDebtCount > 0:
		return fmt.Sprintf("[![HISS Adopted](https://img.shields.io/badge/Standards-HISS%%20Adopted%%20(%d%%20baselined)-yellow)](AGENTS.md)", state.LegacyDebtCount)
	default:
		return "[![HISS Adopted](https://img.shields.io/badge/Standards-HISS%20Adopted-blue)](AGENTS.md)"
	}
}

func baselineDescription(state State) string {
	if !state.BaselineKnown {
		return "Not recorded; verification is pending"
	}
	if state.LegacyDebtCount == 1 {
		return "1 recorded infraction; audit forbids growth"
	}
	return fmt.Sprintf("%d recorded infractions; audit forbids growth", state.LegacyDebtCount)
}

func insertManagedBlock(content, block string) string {
	trimmed := strings.TrimRight(content, "\r\n")
	if trimmed == "" {
		return block + "\n"
	}
	lineEnd := strings.Index(trimmed, "\n")
	if strings.HasPrefix(strings.TrimSpace(trimmed), "# ") && lineEnd >= 0 {
		return trimmed[:lineEnd+1] + "\n" + block + "\n" + trimmed[lineEnd+1:] + "\n"
	}
	return trimmed + "\n\n" + block + "\n"
}
