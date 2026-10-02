package main

import (
	"fmt"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/util"
)

// cavemanMeasure is the size of one text in the three units `caveman estimate` prints. The
// token figure is caveman.EstimateTokens, the one estimator (ADR-0010 decision 9).
type cavemanMeasure struct {
	bytes, lines, tokens int
}

// measureCavemanText measures text for estimate, estimate --base and compress, so the three
// reports cannot disagree on one input.
func measureCavemanText(text string) cavemanMeasure {
	return cavemanMeasure{bytes: len(text), lines: util.CountLines(text), tokens: caveman.EstimateTokens(text)}
}

func (m cavemanMeasure) plus(other cavemanMeasure) cavemanMeasure {
	return cavemanMeasure{bytes: m.bytes + other.bytes, lines: m.lines + other.lines, tokens: m.tokens + other.tokens}
}

// cavemanDelta is one text measured before and after a change: a compression, or a rewrite
// against its git baseline.
type cavemanDelta struct {
	name          string
	before, after cavemanMeasure
}

// row renders the delta under the keys of the plain estimate line, each value as
// "<before>-><after> (<signed difference>)".
func (d cavemanDelta) row() string {
	return fmt.Sprintf("%s: bytes=%s lines=%s tokens_est=%s", d.name,
		cavemanChange(d.before.bytes, d.after.bytes), cavemanChange(d.before.lines, d.after.lines),
		cavemanChange(d.before.tokens, d.after.tokens))
}

// cavemanDeltaTotal renders the total of count deltas under the keys of estimate's total line.
func cavemanDeltaTotal(count int, before, after cavemanMeasure) string {
	return fmt.Sprintf("total: inputs=%d bytes=%s tokens_est=%s", count,
		cavemanChange(before.bytes, after.bytes), cavemanChange(before.tokens, after.tokens))
}

func cavemanChange(before, after int) string {
	return fmt.Sprintf("%d->%d (%+d)", before, after, after-before)
}
