// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package caveman

import (
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ReturnCitation is one cited path with an optional 1-based line number (0 = file only).
type ReturnCitation struct {
	Path string
	Line int
}

const (
	maxReturnCitations   = 256
	maxReturnClaimedRuns = 64
	maxTokenCleanDepth   = 8
	maxLineTokens        = 128
	maxLineLinks         = 32
	maxLineBackticks     = 32
)

var (
	citationRangeRe      = regexp.MustCompile(`^(.*?):(\d+)-(\d+)$`)
	citationDiagnosticRe = regexp.MustCompile(`^(.*?):(\d+)(?::\d+)?$`)
	citationFragmentRe   = regexp.MustCompile(`^(.*?)#L(\d+)(?:-L?(\d+))?$`)
	bareLineSpecRe       = regexp.MustCompile(`^(\d+)(?:-(\d+))?$`)
	markdownLinkRe       = regexp.MustCompile(`\[([^\]]*)\]\(([^)]*)\)`)
	backtickSpanRe       = regexp.MustCompile("`([^`]+)`")
	commandFieldStartRe  = regexp.MustCompile(`(?i)^(?:[-*+]\s+)?(ran|commands):(?:\s*(.*))?$`)
)

// fileExtensionRe is the shape of a file extension in any repository: a dot, then 1 to 16
// letters, digits, underscores, plus or dash with at least one letter. Version tails such as
// go1.27 and long dotted symbols such as forge.ForeignReferenceFindings do not match, so code
// symbols are never taken for files; no per-repository extension list is kept.
var fileExtensionRe = regexp.MustCompile(`^\.[A-Za-z0-9_+-]*[A-Za-z][A-Za-z0-9_+-]*$`)

const maxFileExtensionBytes = 17

// ExtractReturnCitations extracts referenced file paths and path:line citations from return text.
//
// Citation grammar:
//  1. Strip markdown/quote wrappers and trailing sentence punctuation (. , ; : )) before parsing.
//  2. Parse path:N, path:N-M (1 <= N <= M, checking M), path:N:col, and path#LN(-M).
//  3. Trailing comma-separated line numbers or ranges after path:N (e.g. path:46, 57-63, 383) belong to that path.
//  4. Require a repository file extension (or known root filename); dotted symbols (Policy.Command) and plain words are ignored.
//  5. Skip bare file names without a directory (e.g. policy.go, dialect.go): they are ambiguous across subdirectories; root dotfiles (.standards.yaml) are kept.
func ExtractReturnCitations(text string) ([]ReturnCitation, error) {
	if len(text) > maxReturnExtractBytes {
		return nil, errors.New("return exceeds citation extraction bound")
	}
	lines, _ := scan(text)
	var citations []ReturnCitation
	seen := make(map[string]bool)
	currentField := ""
	for i := 0; i < len(lines) && i < maxReturnExtractBytes; i++ {
		ln := lines[i]
		if !shapeContent(ln) {
			continue
		}
		raw := ln.text
		if field := currentReturnField(raw); field != "" {
			currentField = field
		}
		if currentField == "ran" || currentField == "commands" {
			continue
		}
		extractLineCitations(&citations, seen, raw, currentField)
	}
	return citations, nil
}

func currentReturnField(raw string) string {
	if m := commandFieldStartRe.FindStringSubmatch(strings.TrimSpace(raw)); len(m) > 1 {
		return strings.ToLower(m[1])
	}
	return schemaFieldAtStart(raw)
}

func extractLineCitations(citations *[]ReturnCitation, seen map[string]bool, raw, currentField string) {
	extractMarkdownLinkCitations(citations, seen, raw)
	cleaned := markdownLinkRe.ReplaceAllString(raw, " ")
	if currentField == "evidence" {
		extractEvidencePointer(citations, seen, raw)
	}
	tokens := strings.Fields(cleaned)
	listPath := ""
	inList := false
	for i := 0; i < len(tokens) && i < maxLineTokens; i++ {
		rawTok := tokens[i]
		hasComma := strings.HasSuffix(strings.TrimSpace(rawTok), ",")
		if inList && consumeListLineToken(citations, seen, listPath, rawTok) {
			inList = hasComma
			continue
		}
		keptPath, hadLine := addCandidateCitation(citations, seen, rawTok)
		listPath = keptPath
		inList = hadLine && hasComma
	}
}

func extractMarkdownLinkCitations(citations *[]ReturnCitation, seen map[string]bool, raw string) {
	matches := markdownLinkRe.FindAllStringSubmatch(raw, -1)
	for i := 0; i < len(matches) && i < maxLineLinks; i++ {
		if len(matches[i]) == 3 {
			addCandidateCitation(citations, seen, matches[i][2])
		}
	}
}

func consumeListLineToken(citations *[]ReturnCitation, seen map[string]bool, listPath, rawTok string) bool {
	if !isLeadingDigit(rawTok) {
		return false
	}
	line, ok := parseBareLineSpec(cleanCandidateToken(rawTok))
	if !ok {
		return false
	}
	if listPath != "" {
		appendCitation(citations, seen, listPath, line)
	}
	return true
}

func isLeadingDigit(s string) bool {
	return len(s) > 0 && s[0] >= '0' && s[0] <= '9'
}

func extractEvidencePointer(citations *[]ReturnCitation, seen map[string]bool, raw string) {
	match := runtimeEvidenceFieldRe.FindStringSubmatch(strings.TrimSpace(raw))
	if len(match) == 2 {
		fields := strings.Fields(match[1])
		if len(fields) > 0 {
			addCandidateCitation(citations, seen, fields[0])
		}
	}
}

func addCandidateCitation(citations *[]ReturnCitation, seen map[string]bool, token string) (string, bool) {
	if len(*citations) >= maxReturnCitations {
		return "", false
	}
	token = cleanCandidateToken(token)
	if token == "" || isIgnoredToken(token) {
		return "", false
	}
	if _, ok := parseBareLineSpec(token); ok {
		return "", false
	}
	path, line := parsePathAndLine(token)
	if path == "" || !isPathCandidate(path) {
		return "", line > 0
	}
	appendCitation(citations, seen, path, line)
	return path, line > 0
}

func appendCitation(citations *[]ReturnCitation, seen map[string]bool, path string, line int) {
	if len(*citations) >= maxReturnCitations {
		return
	}
	key := path + ":" + strconv.Itoa(line)
	if seen[key] {
		return
	}
	seen[key] = true
	*citations = append(*citations, ReturnCitation{Path: path, Line: line})
}

func cleanCandidateToken(token string) string {
	token = strings.TrimSpace(token)
	for depth := 0; depth < maxTokenCleanDepth; depth++ {
		next := strings.TrimLeft(token, "`\"'([{<")
		next = strings.TrimRight(next, ".,;:)]}>`\"'")
		if next == token {
			break
		}
		token = next
	}
	return strings.TrimPrefix(token, "file://")
}

func parseBareLineSpec(token string) (int, bool) {
	m := bareLineSpecRe.FindStringSubmatch(token)
	if len(m) != 3 {
		return 0, false
	}
	start, err := strconv.Atoi(m[1])
	if err != nil || start < 1 {
		return 0, false
	}
	if m[2] == "" {
		return start, true
	}
	end, err := strconv.Atoi(m[2])
	if err != nil || end < start {
		return 0, false
	}
	return end, true
}

func parsePathAndLine(token string) (string, int) {
	if m := citationFragmentRe.FindStringSubmatch(token); len(m) == 4 {
		if line, ok := parseRangeOrLine(m[2], m[3]); ok {
			return m[1], line
		}
	}
	if m := citationRangeRe.FindStringSubmatch(token); len(m) == 4 && !isNonPathPrefix(m[1]) {
		if line, ok := parseRangeOrLine(m[2], m[3]); ok {
			return m[1], line
		}
	}
	if m := citationDiagnosticRe.FindStringSubmatch(token); len(m) == 3 && !isNonPathPrefix(m[1]) {
		if line, err := strconv.Atoi(m[2]); err == nil && line >= 1 {
			return m[1], line
		}
	}
	return token, 0
}

func parseRangeOrLine(startStr, endStr string) (int, bool) {
	start, err := strconv.Atoi(startStr)
	if err != nil || start < 1 {
		return 0, false
	}
	if endStr == "" {
		return start, true
	}
	end, err := strconv.Atoi(endStr)
	if err != nil || end < start {
		return 0, false
	}
	return end, true
}

func isNonPathPrefix(prefix string) bool {
	switch strings.ToLower(prefix) {
	case "sha256", "lines", "http", "https":
		return true
	default:
		return false
	}
}

func isIgnoredToken(token string) bool {
	lower := strings.ToLower(token)
	switch lower {
	case "none", "nil", "n/a", "-", "exit", "pass", "fail", "partial", "unknown",
		"blocked", "green", "red", "read", "only", "read-only", "readonly",
		"verdict", "changed", "ran", "commands", "evidence", "open", "task", "inputs", "goal", "return":
		return true
	}
	if strings.HasPrefix(lower, "sha256:") || strings.HasPrefix(lower, "lines:") {
		return true
	}
	return strings.HasPrefix(token, "-")
}

// isPathCandidate reports whether path is an unambiguous repository file citation.
// Bare file names without a directory (such as policy.go, dialect.go, command_targets.go,
// pr.go, operator_merge.go) are ambiguous across package directories and are skipped;
// root dotfiles (such as .standards.yaml) and directory-qualified paths are checked.
func isPathCandidate(path string) bool {
	if isNonPathToken(path) || isHostLike(path) {
		return false
	}
	base := filepath.Base(path)
	if !hasRepoFileExtension(base) && !isKnownExtensionlessFile(base) {
		return false
	}
	if strings.Contains(path, "/") || strings.Contains(path, "\\") {
		return true
	}
	return strings.HasPrefix(base, ".") && len(base) > 1
}

// isNonPathToken reports a token that can never name a repository file: empty, a flag, an
// address with a scheme, or one holding a glob, pipe, redirect or quote character.
func isNonPathToken(path string) bool {
	return path == "" || strings.HasPrefix(path, "-") || strings.HasPrefix(path, "http://") ||
		strings.HasPrefix(path, "https://") || strings.ContainsAny(path, "*?|<>\"'")
}

func hasRepoFileExtension(base string) bool {
	ext := filepath.Ext(base)
	return len(ext) > 1 && len(ext) <= maxFileExtensionBytes && fileExtensionRe.MatchString(ext)
}

// isHostLike reports a token whose first segment is a host name (github.com/o/r/x.md,
// docs.example.org/a.html): an address without its scheme, not a repository path.
func isHostLike(path string) bool {
	first, _, found := strings.Cut(filepath.ToSlash(path), "/")
	return found && !strings.HasPrefix(first, ".") && strings.Contains(first, ".")
}

func isKnownExtensionlessFile(base string) bool {
	switch base {
	case "Makefile", "Dockerfile", "LICENSE":
		return true
	default:
		return false
	}
}

// ExtractReturnRan extracts shell commands claimed to have run from the return text.
// Prose in the ran or commands field (e.g. "grep, sed, cat reads of repo; no build")
// is not a claimed command: only backticked spans in the field or list items under it
// starting with "$ " count as claimed runs.
func ExtractReturnRan(text string) ([]string, error) {
	if len(text) > maxReturnExtractBytes {
		return nil, errors.New("return exceeds ran extraction bound")
	}
	lines, _ := scan(text)
	var commands []string
	inRan := false
	for i := 0; i < len(lines) && i < maxReturnExtractBytes; i++ {
		ln := lines[i]
		if !shapeContent(ln) {
			continue
		}
		raw := strings.TrimSpace(ln.text)
		if m := commandFieldStartRe.FindStringSubmatch(raw); len(m) > 1 {
			inRan = true
			extractBacktickedCommands(&commands, m[2])
			continue
		}
		if schemaFieldAtStart(raw) != "" {
			inRan = false
			continue
		}
		if inRan {
			extractRanContinuationLine(&commands, raw)
		}
	}
	return commands, nil
}

func extractRanContinuationLine(commands *[]string, raw string) {
	if item, ok := trimListPrefix(raw); ok {
		if strings.HasPrefix(item, "$ ") {
			addClaimedCommand(commands, strings.TrimSpace(item[2:]))
			return
		}
	}
	extractBacktickedCommands(commands, raw)
}

func trimListPrefix(raw string) (string, bool) {
	for _, prefix := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(raw, prefix) {
			return strings.TrimSpace(raw[len(prefix):]), true
		}
	}
	return "", false
}

func extractBacktickedCommands(commands *[]string, text string) {
	matches := backtickSpanRe.FindAllStringSubmatch(text, -1)
	for i := 0; i < len(matches) && i < maxLineBackticks; i++ {
		if len(matches[i]) == 2 {
			cmd := strings.TrimSpace(matches[i][1])
			cmd = strings.TrimSpace(strings.TrimPrefix(cmd, "$ "))
			addClaimedCommand(commands, cmd)
		}
	}
}

func addClaimedCommand(commands *[]string, cmd string) {
	cmd = strings.Trim(cmd, "`\"'")
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || isHonestNoRun(cmd) {
		return
	}
	if len(*commands) < maxReturnClaimedRuns {
		*commands = append(*commands, cmd)
	}
}

func isHonestNoRun(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	switch lower {
	case "none", "n/a", "nil", "-", "no tests", "no runs", "not applicable":
		return true
	}
	for _, marker := range []string{"could not run", "not run", "unable to run", "cannot run", "failed to run", "skipped"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
