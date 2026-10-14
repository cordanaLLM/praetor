package agentcontext

import (
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Markers of the text the engine writes into an adopted AGENTS.md. They live here, below
// internal/config, internal/compiler and internal/adopt, because the line budget has to tell
// engine text from adopter text; those packages alias them (HISS-19).
const (
	// HarnessTitleSuffix ends the H1 of every harness praetor adoption writes.
	HarnessTitleSuffix = "Agent Operating Harness"
	// HarnessLintDisable opens the markdownlint comment every harness writes on the line
	// above its H1.
	HarnessLintDisable = "<!-- markdownlint-disable"
	// HarnessEndMarker terminates the harness. Everything after it is the repository's text.
	HarnessEndMarker = "<!-- praetor:harness:end -->"
	// RegisterBlockHeading is the H2 above the rendered text register block.
	RegisterBlockHeading = "## Text Register"
	// RegisterBlockStart and RegisterBlockEnd delimit the rendered text register block.
	RegisterBlockStart = "<!-- praetor:register:start -->"
	RegisterBlockEnd   = "<!-- praetor:register:end -->"
)

// EngineBlockReserve is the share of MaxLineBudget an adopted AGENTS.md pays for the engine
// block: the harness H1 through HarnessEndMarker, plus a text register section
// compile-context appended outside it. 75 = engine block of lusoris/sveltesentio at adoption
// (2026-09-15), before the register section (#204) and the band markers (#938) grew it to 104
// (#1232). Adopter text keeps MaxLineBudget - EngineBlockReserve lines whatever the engine
// block grows to, up to EngineBlockMaxLines.
const EngineBlockReserve = 75

// EngineBlockMaxLines is the engine block's own allowance. Lines above EngineBlockReserve and
// up to this bound are not charged to adopter text; lines above it are. Every harness the
// engine renders must fit (internal/adopt TestHarnessFitsEngineBlockAllowance).
const EngineBlockMaxLines = 120

// lineBudget is the line budget of one context file compiled from one canonical AGENTS.md.
type lineBudget struct {
	limit  int // the file budget, MaxLineBudget in production
	engine int // engine-written lines in the canonical source; 0 without a harness
}

// newLineBudget measures the engine block of the LF canonical text content.
func newLineBudget(maxLines int, content string) lineBudget {
	return lineBudget{limit: maxLines, engine: EngineOwnedLines(content)}
}

// exempt returns the engine lines above EngineBlockReserve, capped at EngineBlockMaxLines:
// growth of the engine block that adopter text does not pay for.
func (b lineBudget) exempt() int {
	return max(0, min(b.engine, EngineBlockMaxLines)-EngineBlockReserve)
}

// allows reports whether a file of count lines fits the budget.
func (b lineBudget) allows(count int) bool {
	return count-b.exempt() <= b.limit
}

// parts names both shares of a file of count lines; empty without a harness.
func (b lineBudget) parts(count int) string {
	if b.engine == 0 {
		return ""
	}
	return fmt.Sprintf("; engine block %d lines (reserve %d, allowance %d), adopter text %d lines, adopter allowance %d",
		b.engine, EngineBlockReserve, EngineBlockMaxLines, count-b.engine, b.limit+b.exempt()-b.engine)
}

// CheckContextBudget reports an error wrapping util.ErrMarkedBlockBudget when the LF canonical
// AGENTS.md text content exceeds MaxLineBudget, less the engine block growth (exempt). The
// error names both shares when content carries a harness.
func CheckContextBudget(content string) error {
	budget := newLineBudget(MaxLineBudget, content)
	count := util.BudgetLineCount(content)
	if budget.allows(count) {
		return nil
	}
	return fmt.Errorf("%w: %d lines, budget %d%s", util.ErrMarkedBlockBudget, count, MaxLineBudget, budget.parts(count))
}

// EngineOwnedLines counts the engine-written lines of the LF canonical text content: the
// harness from its H1 (and the lint comment above it) through HarnessEndMarker, plus a text
// register section outside that span. Content without a harness H1 followed by the end marker
// has none: the engine's own AGENTS.md and an unadopted one pay for every line.
func EngineOwnedLines(content string) int {
	lines := strings.Split(content, "\n")
	first, last := harnessSpan(lines)
	if first < 0 {
		return 0
	}
	owned := last - first + 1
	start, end, err := util.FindMarkedBlock(content, RegisterBlockStart, RegisterBlockEnd)
	if err != nil || start < 0 || (start >= first && end <= last) {
		return owned
	}
	return owned + end - start + 1 + registerPrefixLines(lines, start)
}

// harnessSpan returns the line indexes of the harness start and of HarnessEndMarker outside
// fenced code, or (-1, -1) when content carries no harness H1 followed by the marker.
func harnessSpan(lines []string) (first, last int) {
	var fence util.MarkdownFence
	first = -1
	for i := 0; i < len(lines) && i < maxCanonicalLines; i++ {
		trimmed := strings.TrimSpace(lines[i])
		switch {
		case fence.Inside(trimmed):
		case first < 0 && strings.HasPrefix(trimmed, "# ") && strings.HasSuffix(trimmed, HarnessTitleSuffix):
			first = i
		case first >= 0 && trimmed == HarnessEndMarker:
			if first > 0 && strings.HasPrefix(strings.TrimSpace(lines[first-1]), HarnessLintDisable) {
				first--
			}
			return first, i
		}
	}
	return -1, -1
}

// registerPrefixLines counts the section heading above the register block at line start and
// the blank lines around it, as compile-context writes them; 0 without the heading.
func registerPrefixLines(lines []string, start int) int {
	k := start - 1
	if k >= 0 && strings.TrimSpace(lines[k]) == "" {
		k--
	}
	if k < 0 || strings.TrimSpace(lines[k]) != RegisterBlockHeading {
		return 0
	}
	if k > 0 && strings.TrimSpace(lines[k-1]) == "" {
		k--
	}
	return start - k
}
