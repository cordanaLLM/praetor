// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/router"
	"github.com/cordanaLLM/praetor/internal/util"
)

var armPlaceholderPattern = regexp.MustCompile(`\{[a-z_]+\}`)

// ParseArm parses a "name=<command>" specification into a validated Arm.
func ParseArm(spec string) (Arm, error) {
	if len(spec) == 0 || len(spec) > maxCommandBytes {
		return Arm{}, errors.New("arm specification must be 1..4096 bytes")
	}
	name, rawCmd, ok := strings.Cut(spec, "=")
	if !ok || name == "" || strings.TrimSpace(rawCmd) == "" {
		return Arm{}, fmt.Errorf("arm %q must have the form name=<command>", spec)
	}
	if !ValidReplayName(name) {
		return Arm{}, fmt.Errorf("arm name %q is invalid", name)
	}
	tokens, err := tokenizeCommand(rawCmd)
	if err != nil {
		return Arm{}, fmt.Errorf("arm %s: %w", name, err)
	}
	if err := validateArmTokens(name, tokens); err != nil {
		return Arm{}, err
	}
	return Arm{Name: name, Command: tokens, Timeout: DefaultArmTimeout}, nil
}

// ValidReplayName reports whether s is a non-empty, whitespace-free routing identifier.
func ValidReplayName(s string) bool {
	return router.ValidTaskLabel(s) && strings.IndexFunc(s, unicode.IsSpace) < 0
}

// commandLexer holds state while splitting an arm command template into argv tokens.
type commandLexer struct {
	tokens  []string
	cur     strings.Builder
	inToken bool
	quote   byte
}

// tokenizeCommand splits a command template into argv tokens without invoking a shell.
func tokenizeCommand(raw string) ([]string, error) {
	var lex commandLexer
	for i := 0; i < len(raw) && i < maxCommandBytes; i++ {
		i = lex.step(raw, i)
	}
	if lex.quote != 0 {
		return nil, errors.New("unclosed quote in command template")
	}
	lex.flush()
	return lex.tokens, nil
}

// step advances lex over raw[i] and returns the last byte index consumed.
func (l *commandLexer) step(raw string, i int) int {
	ch := raw[i]
	if l.quote != 0 {
		return l.stepQuoted(raw, i, ch)
	}
	if ch == '\'' || ch == '"' {
		l.quote = ch
		l.inToken = true
		return i
	}
	if unicode.IsSpace(rune(ch)) {
		l.flush()
		return i
	}
	l.inToken = true
	l.cur.WriteByte(ch)
	return i
}

// stepQuoted consumes one byte inside an active single or double quote.
func (l *commandLexer) stepQuoted(raw string, i int, ch byte) int {
	if l.quote == '"' && ch == '\\' && i+1 < len(raw) && raw[i+1] == '"' {
		l.cur.WriteByte('"')
		return i + 1
	}
	if ch == l.quote {
		l.quote = 0
		return i
	}
	l.cur.WriteByte(ch)
	return i
}

// flush appends the buffered token when one is open.
func (l *commandLexer) flush() {
	if l.inToken {
		l.tokens = append(l.tokens, l.cur.String())
		l.cur.Reset()
		l.inToken = false
	}
}

// validateArmTokens enforces argument count, control-byte absence, and known placeholders.
func validateArmTokens(name string, tokens []string) error {
	if len(tokens) == 0 || len(tokens) > maxCommandArgs {
		return fmt.Errorf("arm %s command must contain 1..%d arguments", name, maxCommandArgs)
	}
	for i := 0; i < len(tokens) && i < maxCommandArgs; i++ {
		if tokens[i] == "" || strings.IndexFunc(tokens[i], unicode.IsControl) >= 0 {
			return fmt.Errorf("arm %s argument %d is empty or contains control characters", name, i)
		}
		for _, tok := range armPlaceholderPattern.FindAllString(tokens[i], maxCommandArgs) {
			if tok != TreePlaceholder && tok != DraftsPlaceholder {
				return fmt.Errorf("arm %s uses unknown placeholder %s", name, tok)
			}
		}
	}
	return nil
}

// NormalizeVerdict maps an arm's verdict string onto VerdictChangesRequested or VerdictApprove.
func NormalizeVerdict(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case VerdictChangesRequested, "request_changes", "reject", "rejected", "fail", "block":
		return VerdictChangesRequested, nil
	case VerdictApprove, "approved", "pass", "ok", "lgtm", "clean":
		return VerdictApprove, nil
	default:
		return "", fmt.Errorf("unsupported arm verdict %q", raw)
	}
}

// expandArmCommand expands {tree} and {drafts}, appending treeDir and drafts when neither appears.
func expandArmCommand(template []string, treeDir string, drafts []string) []string {
	out := make([]string, 0, len(template)+len(drafts)+1)
	usedPlaceholder := false
	joinedDrafts := strings.Join(drafts, ",")
	for i := 0; i < len(template) && i < maxCommandArgs; i++ {
		tok := template[i]
		if tok == DraftsPlaceholder {
			usedPlaceholder = true
			out = append(out, drafts...)
			continue
		}
		if strings.Contains(tok, TreePlaceholder) || strings.Contains(tok, DraftsPlaceholder) {
			usedPlaceholder = true
			tok = strings.ReplaceAll(tok, TreePlaceholder, treeDir)
			tok = strings.ReplaceAll(tok, DraftsPlaceholder, joinedDrafts)
		}
		out = append(out, tok)
	}
	if !usedPlaceholder {
		out = append(out, treeDir)
		out = append(out, drafts...)
	}
	return out
}

// runArmOnce executes arm against treeDir and drafts and parses its JSON verdict output.
func runArmOnce(ctx context.Context, arm Arm, treeDir string, drafts []string) (verdict ArmVerdict, norm string, retErr error) {
	timeout := arm.Timeout
	if timeout <= 0 {
		timeout = DefaultArmTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	sha, err := util.ResolveGitCommit(runCtx, treeDir, "HEAD")
	if err != nil {
		return ArmVerdict{}, "", err
	}
	treeDir, cleanup, err := snapshotCutoff(runCtx, treeDir, sha)
	if err != nil {
		return ArmVerdict{}, "", err
	}
	defer func() { retErr = errors.Join(retErr, cleanup()) }()
	drafts, err = copyArmDrafts(runCtx, treeDir, drafts)
	if err != nil {
		return ArmVerdict{}, "", err
	}
	argv := expandArmCommand(arm.Command, treeDir, drafts)
	res, err := util.RunCommandBytes(runCtx, treeDir, argv[0], maxArmOutputBytes, argv[1:]...)
	if err != nil {
		return ArmVerdict{}, "", fmt.Errorf("execute arm %s: %w (stderr: %s)", arm.Name, err, strings.TrimSpace(string(res.Stderr)))
	}
	verdict, norm, err = decodeArmVerdict(res.Stdout)
	if err != nil {
		return ArmVerdict{}, "", fmt.Errorf("arm %s output: %w", arm.Name, err)
	}
	return verdict, norm, nil
}

// copyArmDrafts isolates each input under the run's disposable tree, retaining duplicate basenames.
func copyArmDrafts(ctx context.Context, treeDir string, drafts []string) ([]string, error) {
	if len(drafts) > MaxDrafts {
		return nil, refusedf(ErrLimitExceeded, "draft count exceeds %d", MaxDrafts)
	}
	dir, err := os.MkdirTemp(treeDir, ".praetor-drafts-*")
	if err != nil {
		return nil, fmt.Errorf("create private draft directory: %w", err)
	}
	copies := make([]string, 0, len(drafts))
	for i := 0; i < len(drafts) && i < MaxDrafts; i++ {
		raw, err := contextopt.ReadSnapshot(ctx, drafts[i])
		if err != nil {
			return nil, fmt.Errorf("read arm draft: %w", err)
		}
		path := filepath.Join(dir, strconv.Itoa(i), filepath.Base(drafts[i]))
		if err := contextopt.WriteSnapshot(ctx, path, raw, 0o600); err != nil {
			return nil, fmt.Errorf("copy arm draft: %w", err)
		}
		copies = append(copies, path)
	}
	return copies, nil
}

// decodeArmVerdict parses and validates one JSON verdict object from stdout bytes.
func decodeArmVerdict(raw []byte) (ArmVerdict, string, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	dec.DisallowUnknownFields()
	var parsed ArmVerdict
	if err := dec.Decode(&parsed); err != nil {
		return ArmVerdict{}, "", fmt.Errorf("decode verdict JSON: %w", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ArmVerdict{}, "", errors.New("verdict must contain exactly one JSON object")
	}
	if len(parsed.Findings) > MaxFindings {
		return ArmVerdict{}, "", fmt.Errorf("findings count %d exceeds %d", len(parsed.Findings), MaxFindings)
	}
	norm, err := NormalizeVerdict(parsed.Verdict)
	if err != nil {
		return ArmVerdict{}, "", err
	}
	return parsed, norm, nil
}

// countRetainedDefects counts ground-truth fix hunks hit in both forward and reverse runs.
func countRetainedDefects(hunks []FixHunk, forward, reverse []Finding) int {
	retained := 0
	for i := 0; i < len(hunks) && i < MaxDiffLines; i++ {
		if findingsHitHunk(hunks[i], forward) && findingsHitHunk(hunks[i], reverse) {
			retained++
		}
	}
	return retained
}

// findingsHitHunk reports whether any finding's file:line pointer falls inside hunk.
func findingsHitHunk(hunk FixHunk, findings []Finding) bool {
	wantFile := normalizeFindingPath(hunk.File)
	for i := 0; i < len(findings) && i < MaxFindings; i++ {
		file, startLine, endLine, ok := resolveFindingSpan(findings[i])
		if !ok || file != wantFile {
			continue
		}
		if startLine <= hunk.EndLine && endLine >= hunk.StartLine {
			return true
		}
	}
	return false
}

// resolveFindingSpan extracts normalized file and [start, end] line range from Finding.
func resolveFindingSpan(f Finding) (string, int, int, bool) {
	file, line := f.File, f.Line
	if f.Location != "" && (file == "" || line <= 0) {
		locFile, locLine, ok := splitFileLine(f.Location)
		if ok {
			file, line = locFile, locLine
		}
	} else if line <= 0 && strings.Contains(file, ":") {
		locFile, locLine, ok := splitFileLine(file)
		if ok {
			file, line = locFile, locLine
		}
	}
	norm := normalizeFindingPath(file)
	if norm == "" || line <= 0 {
		return "", 0, 0, false
	}
	end := f.EndLine
	if end < line {
		end = line
	}
	return norm, line, end, true
}

// splitFileLine splits "path/to/file.go:42" on its final colon.
func splitFileLine(loc string) (string, int, bool) {
	idx := strings.LastIndexByte(loc, ':')
	if idx <= 0 || idx+1 >= len(loc) {
		return "", 0, false
	}
	n, err := strconv.Atoi(loc[idx+1:])
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return loc[:idx], n, true
}

// normalizeFindingPath preserves repository-relative directories, removing only leading "./".
func normalizeFindingPath(p string) string {
	slash := filepath.ToSlash(strings.TrimSpace(p))
	slash = strings.TrimPrefix(slash, "./")

	return slash
}
