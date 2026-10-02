package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// What `caveman compress` did, or under --stats would do, with one input.
const (
	compressRewritten   = "rewritten"
	compressWouldChange = "would-change"
	compressUnchanged   = "unchanged"
	compressRefused     = "refused"
)

// cavemanCompression is one input after caveman.Compress: the text it would become, the
// library's own before/after figures, and the clarity floor's verdict on the pair.
type cavemanCompression struct {
	input cavemanInput
	text  string
	stats caveman.Stats
	floor caveman.Report
}

// compressCavemanInput runs caveman.Compress, the one implementation, and proves the result
// with caveman.Floor: Compress folds identical consecutive prose lines, which lowers the count
// of a MUST, a prohibition or a numbered rule that such a line carries. The floor compares
// against the input without its ANSI escapes (caveman.StripANSI), whose parameter digits are
// no number of the text.
func compressCavemanInput(input cavemanInput) cavemanCompression {
	text, stats := caveman.Compress(input.text)
	floor := caveman.Floor(caveman.StripANSI(input.text), text)
	return cavemanCompression{input: input, text: text, stats: stats, floor: floor}
}

// delta measures the input against what is kept: the compressed text, or under refused the
// input itself, which stays untouched.
func (c cavemanCompression) delta(status string) cavemanDelta {
	before := cavemanMeasure{bytes: c.stats.BytesIn, lines: util.CountLines(c.input.text), tokens: c.stats.TokensEstIn}
	if status == compressRefused {
		return cavemanDelta{name: c.input.name, before: before, after: before}
	}
	after := cavemanMeasure{bytes: c.stats.BytesOut, lines: util.CountLines(c.text), tokens: c.stats.TokensEstOut}
	return cavemanDelta{name: c.input.name, before: before, after: after}
}

// refusal is the error for a compression the clarity floor fails, with its bounded findings.
func (c cavemanCompression) refusal() error {
	var findings strings.Builder
	appendCavemanFindings(&findings, c.input.name, c.floor.Findings)
	return fmt.Errorf("caveman compress: %s refused: compression would lose %d fact(s) the clarity floor holds; nothing written\n%s",
		c.input.name, len(c.floor.Findings), strings.TrimRight(findings.String(), "\n"))
}

// cavemanCompress applies caveman.Compress, the cleanups that cannot change meaning, to files.
// Without a flag it prints the compressed text of its one input (a file or "-") and nothing
// else. --stats prints one before/after line per input and a total and writes nothing;
// --in-place rewrites each file that changes, through the compare-and-swap snapshot writer,
// and prints the same lines. A directory expands to the Markdown files below it, as it does
// for check. An input whose compression fails caveman.Floor is refused: it is never printed
// or rewritten, and the command exits non-zero. No change is a success.
func cavemanCompress(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	fset := flag.NewFlagSet("caveman compress", flag.ContinueOnError)
	inPlace := fset.Bool("in-place", false, "Rewrite each named file; a refused or unchanged file is left untouched")
	statsOnly := fset.Bool("stats", false, "Print the before/after line per input and a total; write nothing")
	paths, err := parseInterspersed(fset, args)
	if err != nil {
		return err
	}
	if err := validateCompressRequest(*inPlace, *statsOnly, paths); err != nil {
		return err
	}
	inputs, err := readCavemanInputs(ctx, paths, stdin)
	if err != nil {
		return err
	}
	if *inPlace || *statsOnly {
		return reportCavemanCompressions(ctx, inputs, *inPlace, out)
	}
	return printCavemanCompression(inputs, out)
}

func validateCompressRequest(inPlace, statsOnly bool, paths []string) error {
	if inPlace && statsOnly {
		return errors.New("caveman compress: --stats writes nothing and --in-place rewrites; pass one of them")
	}
	for _, path := range paths {
		if inPlace && path == "-" {
			return errors.New("caveman compress: --in-place cannot rewrite standard input")
		}
	}
	return nil
}

// printCavemanCompression writes the compressed text of exactly one input, byte for byte.
func printCavemanCompression(inputs []cavemanInput, out io.Writer) error {
	if len(inputs) != 1 {
		return fmt.Errorf("caveman compress: %d inputs, and standard output holds the text of one; pass --stats to measure or --in-place to rewrite", len(inputs))
	}
	compression := compressCavemanInput(inputs[0])
	if !compression.floor.Passed() {
		return compression.refusal()
	}
	if _, err := io.WriteString(out, compression.text); err != nil {
		return fmt.Errorf("caveman compress: write text: %w", err)
	}
	return nil
}

// compressTally counts the inputs of one report by status and sums their measures.
type compressTally struct {
	before, after cavemanMeasure
	counts        map[string]int
	inputs        int
}

func (t *compressTally) add(delta cavemanDelta, status string) {
	t.before, t.after = t.before.plus(delta.before), t.after.plus(delta.after)
	t.counts[status]++
	t.inputs++
}

// reportCavemanCompressions prints one line per input and a total, rewriting changed files
// under inPlace. A failed write stops the run; the lines of the inputs already handled are
// still printed, so the operator sees which files changed.
func reportCavemanCompressions(ctx context.Context, inputs []cavemanInput, inPlace bool, out io.Writer) error {
	rows, tally, writeErr := compressCavemanInputs(ctx, inputs, inPlace)
	changed := compressWouldChange
	if inPlace {
		changed = compressRewritten
	}
	report := fmt.Sprintf("%s%s %s=%d %s=%d %s=%d\n", rows, cavemanDeltaTotal(tally.inputs, tally.before, tally.after),
		changed, tally.counts[changed], compressUnchanged, tally.counts[compressUnchanged],
		compressRefused, tally.counts[compressRefused])
	if _, err := io.WriteString(out, report); err != nil {
		return errors.Join(writeErr, fmt.Errorf("caveman compress: write report: %w", err))
	}
	if writeErr != nil {
		return writeErr
	}
	if refused := tally.counts[compressRefused]; refused > 0 {
		return fmt.Errorf("caveman compress: %d of %d input(s) refused: compression would lose a fact the clarity floor holds; left untouched",
			refused, tally.inputs)
	}
	return nil
}

// compressCavemanInputs compresses every input in order and returns the report lines, the
// tally and the write failure that ended the run, if any. readCavemanInputs bounds the inputs
// at maxCavemanFiles.
func compressCavemanInputs(ctx context.Context, inputs []cavemanInput, inPlace bool) (string, compressTally, error) {
	var rows strings.Builder
	tally := compressTally{counts: make(map[string]int)}
	for i := 0; i < len(inputs) && i <= maxCavemanFiles; i++ {
		compression := compressCavemanInput(inputs[i])
		status, err := applyCavemanCompression(ctx, compression, inPlace)
		if err != nil {
			return rows.String(), tally, err
		}
		delta := compression.delta(status)
		tally.add(delta, status)
		fmt.Fprintf(&rows, "%s status=%s\n", delta.row(), status)
		if status == compressRefused {
			appendCavemanFindings(&rows, compression.input.name, compression.floor.Findings)
		}
	}
	return rows.String(), tally, nil
}

// applyCavemanCompression decides one input's status and, under inPlace, rewrites a file that
// changes. The write is contextopt.ReplaceSnapshot bound to the bytes that were read, so a file
// edited since the read is refused rather than overwritten, and its permissions are never
// widened.
func applyCavemanCompression(ctx context.Context, compression cavemanCompression, inPlace bool) (string, error) {
	switch {
	case !compression.floor.Passed():
		return compressRefused, nil
	case compression.text == compression.input.text:
		return compressUnchanged, nil
	case !inPlace:
		return compressWouldChange, nil
	}
	path := filepath.FromSlash(compression.input.name)
	options := contextopt.ReplaceOptions{Expected: []byte(compression.input.text), Exists: true, Mode: util.TrackedFilePerm}
	if err := contextopt.ReplaceSnapshot(ctx, path, []byte(compression.text), options); err != nil {
		return "", fmt.Errorf("caveman compress: write %s: %w", compression.input.name, err)
	}
	return compressRewritten, nil
}
