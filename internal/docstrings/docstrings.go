// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docstrings

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/cifilter"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxSourceBytes       = 16 << 20
	maxAttributeLines    = 32
	maxLineAttributes    = 16
	maxCommentBlockLines = 256
	maxPythonHeaderLines = 32
	maxPythonScanLines   = 256
	gitReadTimeout       = 15 * time.Second
)

type docStyle int

const (
	docStyleNone docStyle = iota
	docStyleCommentAbove
	docStylePythonBody
	docStyleShellAbove
)

var languageDocStyles = map[string]docStyle{
	"go":            docStyleCommentAbove,
	"c":             docStyleCommentAbove,
	"cpp":           docStyleCommentAbove,
	"cuda":          docStyleCommentAbove,
	"objective-c++": docStyleCommentAbove,
	"rust":          docStyleCommentAbove,
	"java":          docStyleCommentAbove,
	"javascript":    docStyleCommentAbove,
	"typescript":    docStyleCommentAbove,
	"svelte":        docStyleCommentAbove,
	"kotlin":        docStyleCommentAbove,
	"swift":         docStyleCommentAbove,
	"python":        docStylePythonBody,
	"shell":         docStyleShellAbove,
}

// Options configures a diff-scoped docstring coverage check.
type Options struct {
	RepoDir           string
	BaseRef           string
	HeadRef           string
	ThresholdOverride *int
}

// UncoveredFunction identifies a touched function or method lacking a docstring.
type UncoveredFunction struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Name string `json:"name"`
}

// Report summarizes diff-scoped docstring coverage over a repository range.
type Report struct {
	Counted   int                 `json:"counted"`
	Covered   int                 `json:"covered"`
	Coverage  float64             `json:"coverage"`
	Threshold int                 `json:"threshold"`
	Mode      string              `json:"mode"`
	Passed    bool                `json:"passed"`
	Uncovered []UncoveredFunction `json:"uncovered,omitempty"`
}

// Check evaluates diff-scoped docstring coverage against the merge base.
func Check(ctx context.Context, opts Options) (*Report, error) {
	if ctx == nil {
		return nil, errors.New("docstrings check requires a context")
	}
	repoDir := opts.RepoDir
	if repoDir == "" {
		repoDir = "."
	}
	cfg, err := resolveEffectiveConfig(repoDir, opts.ThresholdOverride)
	if err != nil {
		return nil, err
	}
	if cfg.Mode == "off" {
		rep := &Report{
			Threshold: cfg.Threshold,
			Mode:      cfg.Mode,
		}
		finalizeReport(rep)
		return rep, nil
	}
	baseRef := opts.BaseRef
	if baseRef == "" {
		resolved, _, err := DefaultBaseRef(ctx, repoDir, defaultRemoteName)
		if err != nil {
			return nil, err
		}
		baseRef = resolved
	}
	diffHead := opts.HeadRef
	if diffHead == "" {
		diffHead = "WORKTREE"
	}
	ranges, err := cifilter.GetChangedLineRanges(ctx, repoDir, baseRef, diffHead)
	if err != nil {
		return nil, err
	}
	return evaluateChangedFiles(ctx, repoDir, opts.HeadRef, cfg, ranges)
}

// resolveEffectiveConfig loads .coderabbit.yaml and applies any CLI threshold override.
func resolveEffectiveConfig(repoDir string, override *int) (Config, error) {
	cfg, err := LoadConfig(repoDir)
	if err != nil {
		return Config{}, err
	}
	if override != nil {
		if *override < 0 || *override > 100 {
			return Config{}, fmt.Errorf("invalid threshold %d (want 0..100)", *override)
		}
		cfg.Threshold = *override
		if cfg.Mode == "off" {
			cfg.Mode = "error"
		}
	}
	return cfg, nil
}

// evaluateChangedFiles inspects every changed file in deterministic order and computes coverage.
func evaluateChangedFiles(ctx context.Context, repoDir, headRef string, cfg Config, ranges map[string][]cifilter.LineRange) (*Report, error) {
	rep := &Report{
		Threshold: cfg.Threshold,
		Mode:      cfg.Mode,
	}
	if cfg.Mode == "off" {
		finalizeReport(rep)
		return rep, nil
	}
	paths := make([]string, 0, len(ranges))
	for rel := range ranges {
		paths = append(paths, rel)
	}
	slices.Sort(paths)

	for i := 0; i < len(paths); i++ {
		rel := paths[i]
		fileRanges := ranges[rel]
		if len(fileRanges) == 0 {
			continue
		}
		if err := evaluateSingleFile(ctx, repoDir, headRef, rel, fileRanges, rep); err != nil {
			return nil, err
		}
	}
	finalizeReport(rep)
	return rep, nil
}

// evaluateSingleFile extracts functions for rel and records coverage for those overlapping fileRanges.
func evaluateSingleFile(ctx context.Context, repoDir, headRef, rel string, fileRanges []cifilter.LineRange, rep *Report) error {
	if !isSupportedPath(rel) {
		return nil
	}
	data, ok, err := targetFileReader(ctx, repoDir, headRef, rel)
	if err != nil || !ok {
		return err
	}
	if resolveDocStyle(rel, data) == docStyleNone {
		return nil
	}
	functions, err := hiss.ExtractFileFunctions(rel, data)
	if err != nil {
		return fmt.Errorf("extract functions from %s: %w", rel, err)
	}
	if len(functions) == 0 {
		return checkEmptyLexicalFile(rel, data)
	}
	recordFunctionCoverage(rel, data, splitSourceLines(data), functions, fileRanges, rep)
	return nil
}

// checkEmptyLexicalFile refuses a zero-candidate pass when the shared lexical scan is incomplete.
func checkEmptyLexicalFile(rel string, data []byte) error {
	if resolveDocStyle(rel, data) == docStyleShellAbove {
		return nil
	}
	_, complete := hiss.ScanLines(splitSourceLines(data), util.SourceLanguage(rel))
	if !complete {
		return fmt.Errorf("%s: lexically incomplete source with no function candidates", rel)
	}
	return nil
}

// recordFunctionCoverage updates rep with coverage for functions overlapping fileRanges.
func recordFunctionCoverage(rel string, data []byte, lines []string, functions []hiss.FunctionSpan, fileRanges []cifilter.LineRange, rep *Report) {
	style := resolveDocStyle(rel, data)
	var scanned []scannedLine
	if style == docStyleCommentAbove {
		scanned = scanSourceLines(lines, util.SourceLanguage(rel))
	}
	for i := 0; i < len(functions); i++ {
		fn := functions[i]
		if !overlapsRanges(fn, fileRanges) {
			continue
		}
		rep.Counted++
		if hasDocstringForStyle(style, scanned, lines, fn) {
			rep.Covered++
		} else {
			rep.Uncovered = append(rep.Uncovered, UncoveredFunction{
				Path: rel,
				Line: fn.Start,
				Name: fn.Name,
			})
		}
	}
}

// finalizeReport computes the final coverage percentage and pass/fail verdict.
func finalizeReport(rep *Report) {
	if rep.Counted == 0 {
		rep.Coverage = 100.0
		rep.Passed = true
		return
	}
	rep.Coverage = float64(rep.Covered) * 100.0 / float64(rep.Counted)
	rep.Passed = rep.Mode == "off" || rep.Mode == "warning" || rep.Covered*100 >= rep.Counted*rep.Threshold
}

// overlapsRanges reports whether fn's line span overlaps any changed line range.
func overlapsRanges(fn hiss.FunctionSpan, ranges []cifilter.LineRange) bool {
	for i := 0; i < len(ranges); i++ {
		if ranges[i].Overlaps(fn.Start, fn.End) {
			return true
		}
	}
	return false
}

// targetFileReader reads rel from worktree or headRef; replaced by tests to count or verify file reads.
var targetFileReader = readTargetFile

// readTargetFile reads rel from the working tree when headRef is empty or from git show headRef:rel.
// It returns ok=false when rel is a non-regular worktree entry or a gitlink/symlink tree entry.
func readTargetFile(ctx context.Context, repoDir, headRef, rel string) ([]byte, bool, error) {
	if headRef != "" && headRef != "WORKTREE" {
		return readGitRevisionFile(ctx, repoDir, headRef, rel)
	}
	fullPath := filepath.Join(repoDir, filepath.FromSlash(rel))
	info, err := os.Lstat(fullPath)
	if err != nil {
		return nil, false, fmt.Errorf("stat %s: %w", rel, err)
	}
	if !info.Mode().IsRegular() {
		return nil, false, nil
	}
	if info.Size() > maxSourceBytes {
		return nil, false, fmt.Errorf("file %s exceeds %d bytes", rel, maxSourceBytes)
	}
	root, err := os.OpenRoot(repoDir)
	if err != nil {
		return nil, false, fmt.Errorf("open repository root: %w", err)
	}
	data, err := root.ReadFile(filepath.FromSlash(rel))
	if closeErr := root.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close repository root: %w", closeErr))
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", rel, err)
	}
	return data, true, nil
}

// readGitRevisionFile reads rel at headRef via git ls-tree and git show, skipping gitlinks and symlinks.
func readGitRevisionFile(ctx context.Context, repoDir, headRef, rel string) ([]byte, bool, error) {
	gitCtx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	treeOut, err := util.RunGit(gitCtx, repoDir, "ls-tree", headRef, "--", rel)
	if err != nil {
		return nil, false, fmt.Errorf("ls-tree %s:%s: %w", headRef, rel, err)
	}
	fields := strings.Fields(treeOut)
	if len(fields) == 0 || fields[0] == "160000" || fields[0] == "120000" {
		return nil, false, nil
	}
	out, err := util.RunGit(gitCtx, repoDir, "show", fmt.Sprintf("%s:%s", headRef, rel))
	if err != nil {
		return nil, false, fmt.Errorf("read %s at %s: %w", rel, headRef, err)
	}
	return []byte(out), true, nil
}

// splitSourceLines splits data into LF-normalized lines.
func splitSourceLines(data []byte) []string {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	return strings.Split(text, "\n")
}

// isSupportedPath reports whether rel is of a supported source language or needs content to decide.
func isSupportedPath(rel string) bool {
	style, needsContent := resolvePathDocStyle(rel)
	return style != docStyleNone || needsContent
}

// resolvePathDocStyle returns the docStyle determined by rel's path alone,
// and whether the file needs its content read to decide its language.
func resolvePathDocStyle(rel string) (docStyle, bool) {
	lang := util.SourceLanguage(rel)
	if lang != "" {
		return languageDocStyles[lang], false
	}
	if filepath.Ext(rel) == "" {
		return docStyleNone, true
	}
	return docStyleNone, false
}

// resolveDocStyle maps a file path and content to its language docstring family.
func resolveDocStyle(rel string, data []byte) docStyle {
	style, needsContent := resolvePathDocStyle(rel)
	if !needsContent {
		return style
	}
	if hasShellShebang(data) {
		return docStyleShellAbove
	}
	return docStyleNone
}

// hasShellShebang reports whether data starts with a sh or bash shebang line.
func hasShellShebang(data []byte) bool {
	if !bytes.HasPrefix(data, []byte("#!")) {
		return false
	}
	firstLine, _, _ := strings.Cut(string(data), "\n")
	return strings.Contains(firstLine, "sh")
}

// HasDocstring reports whether fn in rel has a docstring under its language family's convention.
func HasDocstring(rel string, data []byte, lines []string, fn hiss.FunctionSpan) bool {
	style := resolveDocStyle(rel, data)
	var scanned []scannedLine
	if style == docStyleCommentAbove {
		scanned = scanSourceLines(lines, util.SourceLanguage(rel))
	}
	return hasDocstringForStyle(style, scanned, lines, fn)
}

// hasDocstringForStyle checks docstring presence using pre-scanned lexical lines for comment-above languages.
func hasDocstringForStyle(style docStyle, scanned []scannedLine, lines []string, fn hiss.FunctionSpan) bool {
	switch style {
	case docStyleCommentAbove:
		return hasCommentAbove(scanned, fn.Start)
	case docStylePythonBody:
		return hasPythonDocstring(lines, fn.Start, fn.End)
	case docStyleShellAbove:
		return hasShellCommentAbove(lines, fn.Start)
	default:
		return false
	}
}

const maxAnnotationScanLines = maxAttributeLines * 2

// annotationSpan records a complete, balanced attribute or decorator span from openLine to closeLine.
type annotationSpan struct {
	openLine   int
	closeLine  int
	count      int
	startDepth int
	endDepth   int
	startClass hiss.LexClass
	endClass   hiss.LexClass
}

// commentSpan records a standalone line or block comment span from openLine to closeLine.
type commentSpan struct {
	openLine  int
	closeLine int
	class     hiss.LexClass
}

// scannedLine combines the lexical scanner's per-line state with recorded annotation and comment spans.
type scannedLine struct {
	lex               hiss.LexLine
	startBracketDepth int
	endBracketDepth   int
	annotation        *annotationSpan
	comment           *commentSpan
}

// scanSourceLines runs the HISS lexical scanner once forward across lines and records
// complete annotation spans, bracket depths, and standalone comment spans.
func scanSourceLines(lines []string, language string) []scannedLine {
	lexed, _ := hiss.ScanLines(lines, language)
	out := make([]scannedLine, len(lexed))
	for i := 0; i < len(lexed); i++ {
		out[i] = scannedLine{lex: lexed[i]}
	}
	recordAnnotationSpans(out)
	recordCommentSpans(out)
	return out
}

// hasCommentAbove walks upward over the forward-scanned line records above startLine,
// skipping complete annotation spans within the physical-line budget to check for a comment span.
func hasCommentAbove(scanned []scannedLine, startLine int) bool {
	if startLine <= 1 || startLine-1 > len(scanned) {
		return false
	}
	idx := startLine - 2
	if strings.TrimSpace(scanned[startLine-1].lex.Code) == "{" && idx >= 1 {
		idx--
	}
	idx, ok := skipAttributesAbove(scanned, idx)
	if !ok || idx < 0 {
		return false
	}
	return scanned[idx].comment != nil
}

// skipAttributesAbove walks upward over complete annotation spans recorded by the forward pass,
// enforcing maxAttributeLines; startLineAnnotations enforces the per-line count.
func skipAttributesAbove(scanned []scannedLine, idx int) (int, bool) {
	remainingLines := maxAttributeLines
	for steps := 0; steps < maxAttributeLines && idx >= 0; steps++ {
		span := scanned[idx].annotation
		if span == nil {
			return idx, true
		}
		usedLines := span.closeLine - span.openLine + 1
		if usedLines > remainingLines {
			return -1, false
		}
		remainingLines -= usedLines
		idx = span.openLine - 1
	}
	return idx, true
}

// recordAnnotationSpans walks forward over scanned lines and records complete annotation spans
// and per-line bracket depths.
func recordAnnotationSpans(out []scannedLine) {
	for i := 0; i < len(out); i++ {
		if !canOpenAnnotation(out[i].lex) {
			continue
		}
		if closeIdx, ok := matchAnnotationSpan(out, i); ok {
			i = closeIdx
			continue
		}
		i = markUnclosedAnnotation(out, i)
	}
}

// canOpenAnnotation reports whether lex can start a top-level attribute or decorator span.
func canOpenAnnotation(lex hiss.LexLine) bool {
	if lex.StartClass != hiss.LexCode || lex.StartInLiteral || lex.StartBlockDepth > 0 || lex.Unclosed {
		return false
	}
	_, _, ok := consumeAnnotationHeader(trimHorizontal(lex.Code))
	return ok
}

// matchAnnotationSpan scans forward from openLine to find a complete, balanced annotation span.
func matchAnnotationSpan(out []scannedLine, openLine int) (int, bool) {
	var stack []byte
	count := 0
	limit := min(len(out)-1, openLine+maxAnnotationScanLines-1)
	for j := openLine; j <= limit; j++ {
		if out[j].lex.Unclosed {
			return -1, false
		}
		var ok bool
		stack, count, ok = stepLineAnnotations(out[j].lex.Code, stack, count)
		if !ok {
			return -1, false
		}
		if len(stack) == 0 && !out[j].lex.EndInLiteral && out[j].lex.EndBlockDepth == 0 {
			if count <= 0 {
				return -1, false
			}
			applyAnnotationSpan(out, openLine, j, count)
			return j, true
		}
	}
	return -1, false
}

// applyAnnotationSpan records the bracket depths across openLine..closeLine and attaches the span to closeLine.
func applyAnnotationSpan(out []scannedLine, openLine, closeLine, count int) {
	var stack []byte
	c := 0
	for j := openLine; j <= closeLine; j++ {
		out[j].startBracketDepth = len(stack)
		stack, c, _ = stepLineAnnotations(out[j].lex.Code, stack, c)
		out[j].endBracketDepth = len(stack)
	}
	out[closeLine].annotation = &annotationSpan{
		openLine:   openLine,
		closeLine:  closeLine,
		count:      count,
		startDepth: out[openLine].startBracketDepth,
		endDepth:   out[closeLine].endBracketDepth,
		startClass: out[openLine].lex.StartClass,
		endClass:   out[closeLine].lex.EndClass,
	}
}

// markUnclosedAnnotation records positive bracket depths on lines belonging to an unclosed
// annotation up to the next statement or body delimiter, returning the last line consumed.
func markUnclosedAnnotation(out []scannedLine, openLine int) int {
	var stack []byte
	stack, _, ok := stepLineAnnotations(out[openLine].lex.Code, stack, 0)
	out[openLine].endBracketDepth = len(stack)
	if !ok || len(stack) == 0 || strings.ContainsAny(out[openLine].lex.Code, "{;") {
		return openLine
	}
	limit := min(len(out)-1, openLine+maxAttributeLines-1)
	for j := openLine + 1; j <= limit; j++ {
		if strings.ContainsAny(out[j].lex.Code, "{;") {
			return j - 1
		}
		out[j].startBracketDepth = len(stack)
		var valid bool
		stack, _, valid = stepLineAnnotations(out[j].lex.Code, stack, 1)
		out[j].endBracketDepth = max(len(stack), 1)
		if !valid || len(stack) == 0 {
			return j
		}
	}
	return limit
}

// recordCommentSpans walks forward over scanned lines and records standalone line and block comment spans.
func recordCommentSpans(out []scannedLine) {
	for i := 0; i < len(out); i++ {
		if !out[i].lex.CommentOnly || out[i].startBracketDepth > 0 || out[i].lex.StartBlockDepth > 0 {
			continue
		}
		if out[i].lex.EndBlockDepth == 0 {
			out[i].comment = &commentSpan{openLine: i, closeLine: i, class: out[i].lex.StartClass}
			continue
		}
		i = matchBlockCommentSpan(out, i)
	}
}

// matchBlockCommentSpan scans forward from openLine to the closing line of a standalone block comment.
func matchBlockCommentSpan(out []scannedLine, openLine int) int {
	limit := min(len(out)-1, openLine+maxCommentBlockLines)
	for j := openLine + 1; j <= limit; j++ {
		if out[j].lex.EndBlockDepth == 0 {
			if out[j].lex.CommentOnly {
				out[j].comment = &commentSpan{openLine: openLine, closeLine: j, class: out[openLine].lex.StartClass}
			}
			return j
		}
	}
	return limit
}

// attributeName matches a decorator's dotted identifier or a native attribute introducer.
var attributeName = regexp.MustCompile(`^(?:@[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*|__attribute__|#)`)

// stepLineAnnotations advances annotation bracket state across one line's stripped code.
func stepLineAnnotations(code string, stack []byte, count int) ([]byte, int, bool) {
	if len(stack) > 0 {
		var closed, valid bool
		code, stack, closed, valid = advanceAnnotationBody(code, stack)
		if !valid {
			return nil, 0, false
		}
		if !closed {
			return stack, count, true
		}
	}
	return startLineAnnotations(code, stack, count)
}

// startLineAnnotations consumes at most maxLineAttributes new annotations on one physical line.
func startLineAnnotations(code string, stack []byte, count int) ([]byte, int, bool) {
	code = trimHorizontal(code)
	for step := 0; step < maxLineAttributes && code != ""; step++ {
		body, hasBody, ok := consumeAnnotationHeader(code)
		if !ok {
			return nil, 0, false
		}
		count++
		if !hasBody {
			code = trimHorizontal(body)
			continue
		}
		var closed, valid bool
		code, stack, closed, valid = advanceAnnotationBody(body, stack)
		if !valid {
			return nil, 0, false
		}
		if !closed {
			return stack, count, true
		}
		code = trimHorizontal(code)
	}
	if code != "" {
		return nil, 0, false
	}
	return stack, count, true
}

// trimHorizontal trims spaces, tabs, and carriage returns while keeping newline boundaries intact.
func trimHorizontal(text string) string {
	return strings.Trim(text, " \t\r")
}

// consumeAnnotationHeader consumes an annotation introducer from the start of trimmed stripped code,
// returning the remaining text, whether a bracketed body follows, and whether the introducer is valid.
func consumeAnnotationHeader(text string) (string, bool, bool) {
	if text == "" {
		return "", false, false
	}
	name := attributeName.FindString(text)
	body := trimHorizontal(strings.TrimPrefix(text, name))
	if strings.HasPrefix(name, "@") && !strings.HasPrefix(body, "(") {
		return body, false, true
	}
	if !opensAttributeBody(name, body) {
		return "", false, false
	}
	return body, true, true
}

// opensAttributeBody reports whether name and body open a delimited attribute or decorator.
func opensAttributeBody(name, body string) bool {
	switch {
	case name == "" && strings.HasPrefix(body, "[["):
		return true
	case name == "#" && strings.HasPrefix(body, "["):
		return true
	case (name == "__attribute__" || strings.HasPrefix(name, "@")) && strings.HasPrefix(body, "("):
		return true
	default:
		return false
	}
}

// advanceAnnotationBody advances stack across code up to the closing delimiter that empties stack.
func advanceAnnotationBody(code string, stack []byte) (string, []byte, bool, bool) {
	for i := 0; i < len(code); i++ {
		ch := code[i]
		switch {
		case strings.ContainsRune("([{", rune(ch)):
			stack = append(stack, ch)
		case strings.ContainsRune(")]}", rune(ch)):
			if len(stack) == 0 || !matchingAttributeDelimiter(stack[len(stack)-1], ch) {
				return "", nil, false, false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return code[i+1:], stack, true, true
			}
		}
	}
	return "", stack, false, len(stack) > 0
}

// matchingAttributeDelimiter reports whether open and close are a matching bracket pair.
func matchingAttributeDelimiter(open, close byte) bool {
	return strings.IndexByte("([{", open) == strings.IndexByte(")]}", close)
}

// hasShellCommentAbove checks for a # comment line (excluding shebangs) directly above startLine.
func hasShellCommentAbove(lines []string, startLine int) bool {
	if startLine <= 1 || startLine-1 > len(lines) {
		return false
	}
	idx := startLine - 2
	if strings.TrimSpace(lines[startLine-1]) == "{" && idx >= 1 {
		idx--
	}
	if idx < 0 {
		return false
	}
	above := strings.TrimSpace(lines[idx])
	return strings.HasPrefix(above, "#") && !strings.HasPrefix(above, "#!")
}

// hasPythonDocstring checks whether the first statement of a Python function body is a string literal.
func hasPythonDocstring(lines []string, startLine, endLine int) bool {
	if startLine < 1 || endLine < startLine || startLine > len(lines) {
		return false
	}
	headerEnd, inlineBody := findPythonHeaderEnd(lines, startLine, endLine)
	if headerEnd < 0 {
		return false
	}
	if inlineBody != "" {
		return isPythonStringLiteral(inlineBody)
	}
	limit := min(len(lines)-1, min(endLine-1, headerEnd+maxPythonScanLines))
	for i := headerEnd + 1; i <= limit; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return isPythonStringLiteral(trimmed)
	}
	return false
}

// findPythonHeaderEnd locates the 0-based line index where a Python def header closes with ':'.
func findPythonHeaderEnd(lines []string, startLine, endLine int) (int, string) {
	limit := min(len(lines)-1, min(endLine-1, startLine-1+maxPythonHeaderLines))
	depth := 0
	for i := startLine - 1; i <= limit; i++ {
		code := stripPythonLineComment(lines[i])
		depth += countParenDelta(code)
		if depth <= 0 {
			if colonIdx := strings.LastIndexByte(code, ':'); colonIdx >= 0 {
				rest := strings.TrimSpace(code[colonIdx+1:])
				return i, rest
			}
		}
	}
	return -1, ""
}

// stripPythonLineComment removes a trailing # comment outside quotes from one line.
func stripPythonLineComment(line string) string {
	var inQuote byte
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if inQuote != 0 {
			switch ch {
			case '\\':
				i++
			case inQuote:
				inQuote = 0
			}
			continue
		}
		switch ch {
		case '"', '\'':
			inQuote = ch
		case '#':
			return strings.TrimSpace(line[:i])
		}
	}
	return strings.TrimSpace(line)
}

// countParenDelta returns the net open minus close delimiter count on a code line.
func countParenDelta(code string) int {
	delta := 0
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '(', '[', '{':
			delta++
		case ')', ']', '}':
			delta--
		}
	}
	return delta
}

// isPythonStringLiteral accepts constant text docstrings; bytes and interpolated strings provide none.
func isPythonStringLiteral(stmt string) bool {
	s := stmt
	if s != "" && strings.ContainsRune("rRuU", rune(s[0])) {
		s = s[1:]
	}
	return s != "" && (s[0] == '"' || s[0] == '\'')
}
