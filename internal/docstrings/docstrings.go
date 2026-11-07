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
		return nil
	}
	recordFunctionCoverage(rel, data, splitSourceLines(data), functions, fileRanges, rep)
	return nil
}

// recordFunctionCoverage updates rep with coverage for functions overlapping fileRanges.
func recordFunctionCoverage(rel string, data []byte, lines []string, functions []hiss.FunctionSpan, fileRanges []cifilter.LineRange, rep *Report) {
	for i := 0; i < len(functions); i++ {
		fn := functions[i]
		if !overlapsRanges(fn, fileRanges) {
			continue
		}
		rep.Counted++
		if HasDocstring(rel, data, lines, fn) {
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
	data, err := os.ReadFile(fullPath) // #nosec G304 -- fullPath is confined to repoDir and verified regular above
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
	switch resolveDocStyle(rel, data) {
	case docStyleCommentAbove:
		return hasCommentAbove(lines, fn.Start)
	case docStylePythonBody:
		return hasPythonDocstring(lines, fn.Start, fn.End)
	case docStyleShellAbove:
		return hasShellCommentAbove(lines, fn.Start)
	default:
		return false
	}
}

// hasCommentAbove checks for a //, ///, /* */, or /** */ comment ending directly above startLine,
// skipping intervening attributes and decorators.
func hasCommentAbove(lines []string, startLine int) bool {
	if startLine <= 1 || startLine-1 > len(lines) {
		return false
	}
	idx := startLine - 2
	if strings.TrimSpace(lines[startLine-1]) == "{" && idx >= 1 {
		idx--
	}
	for steps := 0; steps < maxAttributeLines && idx >= 0; steps++ {
		if !isAttributeOrDecorator(strings.TrimSpace(lines[idx])) {
			break
		}
		idx--
	}
	if idx < 0 {
		return false
	}
	above := strings.TrimSpace(lines[idx])
	if strings.HasPrefix(above, "//") {
		return true
	}
	if strings.HasSuffix(above, "*/") {
		return hasBlockCommentStart(lines, idx)
	}
	return false
}

// isAttributeOrDecorator reports whether a trimmed line is an attribute or decorator annotation.
func isAttributeOrDecorator(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	return strings.HasPrefix(trimmed, "#[") ||
		strings.HasPrefix(trimmed, "@") ||
		strings.HasPrefix(trimmed, "[[") ||
		strings.HasPrefix(trimmed, "__attribute__")
}

// hasBlockCommentStart verifies that a line ending with */ belongs to a standalone /* or /** comment block.
func hasBlockCommentStart(lines []string, endIdx int) bool {
	minIdx := max(0, endIdx-maxCommentBlockLines)
	for i := endIdx; i >= minIdx; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if i < endIdx && strings.Contains(trimmed, "*/") {
			return false
		}
		if strings.Contains(trimmed, "/*") {
			return strings.HasPrefix(trimmed, "/*")
		}
	}
	return false
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

// isPythonStringLiteral reports whether a trimmed Python statement starts with a string literal.
func isPythonStringLiteral(stmt string) bool {
	s := stmt
	for i := 0; i < 2 && len(s) > 0; i++ {
		if strings.IndexByte("rRuUbBfF", s[0]) >= 0 {
			s = s[1:]
		} else {
			break
		}
	}
	return len(s) > 0 && (s[0] == '"' || s[0] == '\'')
}
