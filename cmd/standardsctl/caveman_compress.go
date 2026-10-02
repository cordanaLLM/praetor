package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
// no number of the text: Floor's F9 reads them as numbers (#713). Once that is fixed the
// workaround can go; TestStripANSI in internal/caveman fails then and says so.
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
// for check, and a file reached twice (named twice, or named beside its directory) is one
// input. --in-place rewrites Markdown files only: Compress reads its input as Markdown, so a
// file of another extension is refused by name before anything is written. An input whose
// compression fails caveman.Floor is refused: it is never printed or rewritten, and the
// command exits non-zero. No change is a success.
func cavemanCompress(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	fset := flag.NewFlagSet("caveman compress", flag.ContinueOnError)
	inPlace := fset.Bool("in-place", false, "Rewrite each named Markdown file; a refused or unchanged file is left untouched")
	statsOnly := fset.Bool("stats", false, "Print the before/after line per input and a total; write nothing")
	args, err := parseInterspersed(fset, args)
	if err != nil {
		return err
	}
	if err := validateCompressRequest(*inPlace, *statsOnly, args); err != nil {
		return err
	}
	paths, err := compressPaths(ctx, args, *inPlace)
	if err != nil {
		return err
	}
	inputs, err := readCavemanPaths(ctx, paths, stdin)
	if err != nil {
		return err
	}
	if *inPlace || *statsOnly {
		return reportCavemanCompressions(ctx, inputs, *inPlace, out)
	}
	return printCavemanCompression(inputs, out)
}

// compressPaths expands args into the files to compress, each once, and under inPlace refuses
// every file that is not Markdown.
func compressPaths(ctx context.Context, args []string, inPlace bool) ([]string, error) {
	expanded, err := expandCavemanPaths(ctx, args, cavemanProseExtensions())
	if err != nil {
		return nil, err
	}
	paths, err := uniqueCavemanPaths(expanded)
	if err != nil || !inPlace {
		return paths, err
	}
	return paths, requireMarkdownPaths(paths)
}

// maxNamedRefusals bounds the files one refusal names; the count says how many more there are.
const maxNamedRefusals = 8

// requireMarkdownPaths refuses, by name, every path that is not a Markdown file. Compress
// protects what Markdown makes code; in Python or YAML it would collapse the blanks of an
// aligned column or a single-quoted literal, where they carry meaning. Standard output and
// --stats write no file and take any text.
func requireMarkdownPaths(paths []string) error {
	var refused []string
	for _, path := range paths {
		if !strings.EqualFold(filepath.Ext(path), cavemanProseExtension) {
			refused = append(refused, filepath.ToSlash(path))
		}
	}
	if len(refused) == 0 {
		return nil
	}
	more := ""
	if extra := len(refused) - maxNamedRefusals; extra > 0 {
		refused, more = refused[:maxNamedRefusals], fmt.Sprintf(" (+%d more)", extra)
	}
	return fmt.Errorf("caveman compress: --in-place rewrites %s files only, not %s%s; nothing written (print one with no flag, or measure with --stats)",
		cavemanProseExtension, strings.Join(refused, ", "), more)
}

// seenCavemanPath is one path a run already holds: its spelling with the directory resolved,
// and the file it names.
type seenCavemanPath struct {
	resolved string
	info     fs.FileInfo
}

// uniqueCavemanPaths drops every path that names a file an earlier path names, keeping order:
// the same spelling twice, a directory beside a file below it, a relative beside an absolute
// spelling, and on a file system that ignores case two spellings that differ in case. A file
// compressed twice in one run would fail its second compare-and-swap write, since the first
// changed the bytes the second was bound to. Hard links under different names are distinct
// paths and stay distinct: the atomic write replaces one name, so each name needs its own.
// "-" is no file and passes.
func uniqueCavemanPaths(paths []string) ([]string, error) {
	unique := make([]string, 0, len(paths))
	seen := make(map[string][]seenCavemanPath, len(paths))
	for _, path := range paths {
		if path == "-" {
			unique = append(unique, path)
			continue
		}
		entry, err := resolveCavemanPath(path)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(entry.resolved)
		if !slices.ContainsFunc(seen[key], entry.sameFile) {
			seen[key] = append(seen[key], entry)
			unique = append(unique, path)
		}
	}
	return unique, nil
}

// resolveCavemanPath spells path absolutely with the symlinks of its directory resolved. The
// last element is not followed: a symlinked file stays its own path, for the reader to refuse.
func resolveCavemanPath(path string) (seenCavemanPath, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return seenCavemanPath{}, fmt.Errorf("read %s: %w", path, err)
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return seenCavemanPath{}, fmt.Errorf("read %s: %w", path, err)
	}
	resolved := filepath.Join(dir, filepath.Base(absolute))
	// #nosec G703 -- path is a file the operator names on the command line; it is only
	// compared here, and read through the bounded, symlink-resistant contextopt.ReadSnapshot.
	info, err := os.Lstat(resolved)
	if err != nil {
		return seenCavemanPath{}, fmt.Errorf("read %s: %w", path, err)
	}
	return seenCavemanPath{resolved: resolved, info: info}, nil
}

// sameFile reports whether other names the file p names: the same resolved spelling, or
// spellings that differ only in case and reach one file.
func (p seenCavemanPath) sameFile(other seenCavemanPath) bool {
	return p.resolved == other.resolved || os.SameFile(p.info, other.info)
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
// edited since the read is refused rather than overwritten. The file keeps the mode it had
// (KeepMode): a cleanup of its text is no reason to drop an execute bit, which git would
// record as a mode change beside the text change.
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
	options := contextopt.ReplaceOptions{Expected: []byte(compression.input.text), Exists: true, Mode: util.TrackedFilePerm, KeepMode: true}
	if err := contextopt.ReplaceSnapshot(ctx, path, []byte(compression.text), options); err != nil {
		return "", fmt.Errorf("caveman compress: write %s: %w", compression.input.name, err)
	}
	return compressRewritten, nil
}
