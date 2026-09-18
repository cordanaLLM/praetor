// Package caveman keeps agent-facing text terse and measurable.
//
// Agent-facing text (AGENTS.md, personas, skills, MCP descriptions, prompts, hook messages,
// ledger entries) is written caveman by hand at the source; this package keeps it that way.
// Check lints a text, Floor proves a rewrite lost no fact, Compress applies the cleanups that
// cannot change meaning, and EstimateTokens is the one token estimator of the repository.
//
// Prose is never rewritten here. A deterministic prose compressor measured 1-3% savings on
// AGENTS.md and inverted the meaning of one sentence in 12 KB, so the saving comes from a
// hand rewrite kept honest by Check and Floor (ADR-0010).
//
// The package is a leaf: standard library only, so every package that emits agent text can
// import it without a cycle.
package caveman

import "strings"

// TokensPerWord is the ratio behind EstimateTokens. Callers that invert the estimate (a word
// budget from a token budget) divide by it rather than restating the number.
const TokensPerWord = 1.3

// EstimateTokens approximates the BPE token count of text as whitespace-separated words
// times TokensPerWord. It is an estimate: no tokenizer ships with the binary, and the ratio
// was chosen to approximate code-heavy agent text. Every token figure praetor prints comes
// from this function, so two reports never disagree on the same input.
func EstimateTokens(text string) int {
	return int(float64(len(strings.Fields(text))) * TokensPerWord)
}
