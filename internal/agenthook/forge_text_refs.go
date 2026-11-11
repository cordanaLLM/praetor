// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	forgeTextRefRuleName  = "forge-text-foreign-reference"
	forgeTextAllowedForms = "Allowed forms: fleet organisation reference, plain text without owner/repo#N, or a code span."
	forgeTextNoOrgNotice  = "no forge.own_organizations list resolved for repository root (only repository owner counts as own)"
	maxForgeTextFileBytes = 1 << 20
	maxForgeTextSegments  = 256
	maxForgeTextWords     = 4096
	maxForgeTextFiles     = 32
	forgeTextReadTimeout  = 5 * time.Second
)

var (
	errHookOperatorForgeSettings = errors.New("operator forge settings unavailable")
	ghAPIScopedPath              = regexp.MustCompile(`(?i)(?:^|/)(?:issues|pulls|comments)(?:/|$|\?)`)
	ghWriteWordPattern           = regexp.MustCompile(ghWriteWordSource(scopedGHActions))
	catSubstPattern              = regexp.MustCompile(`\$\(\s*cat(?:\s+--)?\s+([^\s<>|;&)]+)\s*\)`)
	scopedGHActions              = map[string]map[string]bool{
		"issue":   {"create": true, "edit": true, "comment": true, "close": true, "reopen": true},
		"pr":      {"create": true, "edit": true, "comment": true, "review": true, "close": true, "reopen": true, "merge": true},
		"release": {"create": true, "edit": true},
	}
	ghAPIValueFlags = map[string]bool{
		"-H": true, "--header": true, "--hostname": true, "--cache": true,
		"-q": true, "--jq": true, "-p": true, "--preview": true, "-t": true, "--template": true,
	}
)

// ghWriteWordSource builds the pattern that keeps an unparseable command refused when it may be a gh
// write: every subcommand and action of actions, plus gh api on issues, pulls or comments. Deriving it
// from scopedGHActions keeps the fallback and the allowlist naming the same actions.
func ghWriteWordSource(actions map[string]map[string]bool) string {
	subs := slices.Sorted(maps.Keys(actions))
	alts := make([]string, 0, len(subs)+1)
	for i := 0; i < len(subs) && i < 16; i++ {
		names := slices.Sorted(maps.Keys(actions[subs[i]]))
		alts = append(alts, regexp.QuoteMeta(subs[i])+`\b[\s\S]*\b(`+strings.Join(names, "|")+`)`)
	}
	alts = append(alts, `api\b[\s\S]*(issues|pulls|comments)`)
	return `(?i)\bgh\b[\s\S]*\b(` + strings.Join(alts, "|") + `)\b`
}

// ghTextFile pairs the human-readable label reported in refusals with the resolved
// filesystem path read by the forge-text guard.
type ghTextFile struct {
	label    string
	resolved string
	word     string
}

// ghWriteCall holds the raw command text, resolved file arguments and any file-collection
// refusal for a single scoped gh write invocation.
type ghWriteCall struct {
	raw     string
	files   []ghTextFile
	fileErr string
	dir     string
	bodies  []hiss.HereDoc
}

// ghDirPolicy holds the resolved trusted organisations, missing-list flag, diagnostic
// notice and any fatal resolution error cached for a command working directory.
type ghDirPolicy struct {
	own       []string
	noOrgList bool
	notice    string
	err       error
}

// evaluateForgeTextRefs inspects every scoped gh write call in canonical.Command and
// refuses unreadable text and foreign references. Manifest defects are stated findings,
// never a refusal of a write containing no active reference.
func evaluateForgeTextRefs(ctx context.Context, canonical Canonical, root string, in Invocation) Verdict {
	ctx, cancel := context.WithTimeout(ctx, forgeTextReadTimeout)
	defer cancel()
	baseDir := commandBase(canonical, in.WorkDir)
	if baseDir == "" {
		baseDir = root
	}
	calls, err := collectGHWriteCalls(canonical.Command, baseDir)
	if err != nil {
		return forgeTextDeny("shell reader: "+err.Error(), false)
	}
	if len(calls) == 0 {
		return Verdict{Outcome: Allow}
	}
	_, bodies, err := hiss.SplitHereDocs(canonical.Command)
	if err != nil {
		return forgeTextDeny("shell reader: "+err.Error(), false)
	}
	for i := 0; i < len(calls) && i < maxForgeTextSegments; i++ {
		calls[i].bodies = bodies
	}
	dirPolicies := make(map[string]ghDirPolicy, len(calls))
	var notices []string
	for i := 0; i < len(calls) && i < maxForgeTextSegments; i++ {
		dirPolicy, ok := dirPolicies[calls[i].dir]
		if !ok {
			dirPolicy = resolveGHDirPolicy(ctx, calls[i].dir, in.Getenv)
			dirPolicies[calls[i].dir] = dirPolicy
		}
		verdict := judgeResolvedGHWriteCall(ctx, calls[i], dirPolicy)
		if verdict.Outcome != Allow {
			return verdict
		}
		notices = appendUniqueNotice(notices, verdict.Notice)
	}
	return Verdict{Outcome: Allow, Notice: strings.Join(notices, "; ")}
}

// appendUniqueNotice appends notice to notices when notice is non-empty and not already present.
func appendUniqueNotice(notices []string, notice string) []string {
	if notice == "" || slices.Contains(notices, notice) {
		return notices
	}
	return append(notices, notice)
}

// resolveGHDirPolicy resolves the repository root and trusted organisations for dir once.
// Commands outside any git repository keep an empty root so no arbitrary directory manifest is loaded.
func resolveGHDirPolicy(ctx context.Context, dir string, getenv func(string) string) ghDirPolicy {
	root, err := repositoryRoot(ctx, dir)
	if err != nil {
		return ghDirPolicy{err: err}
	}
	own, noOrgList, notice, err := resolveHookOwnOrganizations(ctx, root, getenv)
	if errors.Is(err, errHookOperatorForgeSettings) {
		return ghDirPolicy{err: err}
	}
	if err != nil {
		notice = strings.TrimPrefix(notice+"; organisation finding: "+err.Error(), "; ")
	}
	return ghDirPolicy{own: own, noOrgList: noOrgList, notice: notice}
}

// judgeResolvedGHWriteCall judges call against the cached directory policy for call.dir.
// Operator-policy and root-lookup failures deny writes; identity and manifest findings attach as notices.
func judgeResolvedGHWriteCall(ctx context.Context, call ghWriteCall, dirPolicy ghDirPolicy) Verdict {
	if dirPolicy.err != nil {
		return forgeTextDeny(dirPolicy.err.Error(), false)
	}
	verdict := judgeGHWriteCall(ctx, call, dirPolicy.own, dirPolicy.noOrgList)
	if verdict.Outcome == Allow {
		verdict.Notice = dirPolicy.notice
	} else if dirPolicy.notice != "" {
		verdict.Reason += "\n" + dirPolicy.notice
	}
	return verdict
}

// judgeGHWriteCall checks a single ghWriteCall and its named files against own, refusing
// file collection errors, unreadable or oversized files, and foreign forge references.
func judgeGHWriteCall(ctx context.Context, call ghWriteCall, own []string, noOrgList bool) Verdict {
	if finding := heredocFinding(call.bodies, own); finding != "" {
		return forgeTextDeny(finding, noOrgList)
	}
	if call.fileErr != "" {
		return forgeTextDeny(call.fileErr, noOrgList)
	}
	if findings := forge.ForeignReferenceFindings(call.raw, "gh command", own); len(findings) > 0 {
		return forgeTextDeny(findings[0], noOrgList)
	}
	for i := 0; i < len(call.files) && i < maxForgeTextFiles; i++ {
		text, err := readForgeTextFile(ctx, call.files[i].resolved)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && heredocWritesFile(call.bodies, call.files[i].word) {
				continue
			}
			return forgeTextDeny(fmt.Sprintf("cannot read file %s: %v", call.files[i].label, err), noOrgList)
		}
		if findings := forge.ForeignReferenceFindings(text, call.files[i].label, own); len(findings) > 0 {
			return forgeTextDeny(findings[0], noOrgList)
		}
	}
	return Verdict{Outcome: Allow}
}

// forgeTextDeny builds a HISS Deny verdict for rule forge-text-foreign-reference with
// detail and the allowed reference forms, appending the missing-organisation notice when noOrgList is set.
func forgeTextDeny(detail string, noOrgList bool) Verdict {
	if noOrgList {
		detail += "; " + forgeTextNoOrgNotice
	}
	return Verdict{
		Outcome: Deny,
		Reason:  refusal("HISS", forgeTextRefRuleName+": "+detail+"\n"+forgeTextAllowedForms),
	}
}

// resolveHookOwnOrganizations resolves the trusted forge organisations from operator
// settings, the command manifest and repository identity. Invalid manifests contribute only a
// finding; their organisations are never trusted.
func resolveHookOwnOrganizations(ctx context.Context, root string, getenv func(string) string) ([]string, bool, string, error) {
	opForge, err := loadOperatorForgeSettings(ctx, getenv)
	if err != nil {
		return nil, false, "", fmt.Errorf("%w: %w", errHookOperatorForgeSettings, err)
	}
	manifestPath := ""
	if root != "" {
		manifestPath = filepath.Join(root, manifestName)
	}
	resolved, err := forge.ResolveOwnOrganizationSources(ctx, root, manifestPath, opForge.OwnOrganizations, getenv)
	notice := ""
	if resolved.ManifestProblem != nil {
		sources := strings.Join(resolved.Sources, ", ")
		if sources == "" {
			sources = "none; references fail closed"
		}
		notice = forgeTextRefRuleName + ": manifest finding; trusted sources: " + sources + "; " + resolved.ManifestProblem.Error()
	}
	noOrgList := len(opForge.OwnOrganizations) == 0 && len(resolved.Organizations) <= 1
	return resolved.Organizations, noOrgList, notice, err
}

// loadOperatorForgeSettings loads operator-level forge settings when fleet or workstation
// config environment variables are set, refusing invalid operator policy files.
func loadOperatorForgeSettings(ctx context.Context, getenv func(string) string) (config.ForgeSettings, error) {
	if getenv == nil {
		return config.ForgeSettings{}, nil
	}
	if getenv(config.FleetConfigEnv) == "" && getenv(config.WorkstationConfigEnv) == "" {
		return config.ForgeSettings{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, forgeTextReadTimeout)
	defer cancel()
	policy, err := config.SelectOperatorPolicy(ctx, config.SettingsRequest{Getenv: getenv})
	if err != nil || policy == nil {
		return config.ForgeSettings{}, err
	}
	return policy.OperatorSettings().Forge, nil
}

// collectGHWriteCalls splits command into shell segments, tracks working-directory changes
// per subshell level and returns every scoped gh write call, refusing commands exceeding limits.
func collectGHWriteCalls(command, baseDir string) ([]ghWriteCall, error) {
	fields, err := shellFieldsForGH(command)
	if err != nil || len(fields) == 0 {
		return nil, err
	}
	var calls []ghWriteCall
	dirStack := []string{baseDir}
	start, segments := 0, 0
	for i := 0; i <= len(fields) && i <= maxForgeTextWords; i++ {
		if i < len(fields) && !targetCommandSeparator(fields[i]) {
			continue
		}
		segments++
		if segments > maxForgeTextSegments {
			return nil, fmt.Errorf("command exceeds %d command segments", maxForgeTextSegments)
		}
		segWords := fields[start:i]
		dirStack = pushSubshellLevel(dirStack, countSubshellOpeners(segWords))
		closers := min(subshellClosers(segWords, countSubshellClosers(segWords)), max(0, len(dirStack)-1))
		if closers > 0 {
			segWords = trimSegmentTrailingParens(segWords, closers)
		}
		call, nextDir, ok := parseSegmentGHCall(command, segWords, dirStack[len(dirStack)-1])
		dirStack[len(dirStack)-1] = nextDir
		if ok {
			calls = append(calls, call)
		}
		dirStack = popSubshellLevel(dirStack, closers)
		start = i + 1
	}
	return calls, nil
}

// pushSubshellLevel duplicates the top working directory on stack for each opened subshell level.
func pushSubshellLevel(stack []string, count int) []string {
	for i := 0; i < count && len(stack) < maxForgeTextSegments; i++ {
		stack = append(stack, stack[len(stack)-1])
	}
	return stack
}

// popSubshellLevel pops count subshell levels from stack, keeping the root directory.
func popSubshellLevel(stack []string, count int) []string {
	for i := 0; i < count && len(stack) > 1; i++ {
		stack = stack[:len(stack)-1]
	}
	return stack
}

// countLeadingParens returns the number of leading '(' bytes in word.
func countLeadingParens(word string) int {
	count := 0
	for count < len(word) && word[count] == '(' {
		count++
	}
	return count
}

// countSubshellOpeners counts unquoted leading '(' in words ahead of the command word.
func countSubshellOpeners(words []string) int {
	openers := 0
	for i := 0; i < len(words) && i < maxForgeTextWords; i++ {
		w := words[i]
		if len(w) == 0 {
			continue
		}
		if w[0] != '(' {
			break
		}
		leading := countLeadingParens(w)
		openers += leading
		if leading < len(w) {
			break
		}
	}
	return openers
}

// countTrailingParens returns the number of unescaped trailing ')' bytes in word.
func countTrailingParens(word string) int {
	count := 0
	for i := len(word) - 1; i >= 0; i-- {
		if word[i] != ')' {
			break
		}
		slashes := 0
		for j := i - 1; j >= 0 && word[j] == '\\'; j-- {
			slashes++
		}
		if slashes%2 != 0 {
			break
		}
		count++
	}
	return count
}

// countSubshellClosers counts unquoted, unescaped trailing ')' from the end of words.
func countSubshellClosers(words []string) int {
	closers := 0
	for i := len(words) - 1; i >= 0; i-- {
		w := words[i]
		if len(w) == 0 {
			continue
		}
		trailing := countTrailingParens(w)
		if trailing == 0 {
			break
		}
		closers += trailing
		if trailing == len(w) {
			continue
		}
		break
	}
	return closers
}

// subshellClosers discounts unclosed $( command substitutions from trailing parentheses,
// ensuring command substitutions own their closing paren rather than subshell scopes.
func subshellClosers(words []string, trailing int) int {
	if trailing <= 0 || len(words) == 0 {
		return 0
	}
	stopWord, stopIdx := trailingParenOrigin(words, trailing)
	unclosed := countUnclosedSubstOpenings(words, stopWord, stopIdx)
	return max(0, trailing-unclosed)
}

// trailingParenOrigin finds the word index and byte offset where trailing unescaped
// closing parentheses begin across the tail of words.
func trailingParenOrigin(words []string, trailing int) (int, int) {
	remaining := trailing
	for i := len(words) - 1; i >= 0 && remaining > 0 && i < maxForgeTextWords; i-- {
		w := words[i]
		if len(w) == 0 {
			continue
		}
		t := countTrailingParens(w)
		if t == 0 {
			break
		}
		consume := min(remaining, t)
		remaining -= consume
		if remaining == 0 {
			return i, len(w) - consume
		}
	}
	return 0, 0
}

// countUnclosedSubstOpenings scans words left to right up to stopWord and stopIdx,
// returning the number of $( command substitutions still unclosed at that point.
func countUnclosedSubstOpenings(words []string, stopWord, stopIdx int) int {
	unclosed := 0
	var quote byte // 0 outside quotes, '\'' or '"' inside that quote
	for i := 0; i <= stopWord && i < len(words) && i < maxForgeTextWords; i++ {
		limit := len(words[i])
		if i == stopWord {
			limit = min(limit, stopIdx)
		}
		scanWordSubstOpenings(words[i], limit, &unclosed, &quote)
	}
	return unclosed
}

// scanWordSubstOpenings scans word up to limit bytes, updating the unclosed $( count and
// the quote state: single-quoted text is literal, double-quoted text still opens $(.
func scanWordSubstOpenings(word string, limit int, unclosed *int, quote *byte) {
	for j := 0; j < limit && j < len(word); j++ {
		if *quote == '\'' {
			if word[j] == '\'' {
				*quote = 0
			}
			continue
		}
		j = stepSubstByte(word, j, limit, unclosed, quote)
	}
}

// stepSubstByte processes one byte outside single quotes, updating the unclosed $( count,
// escapes and the quote state, and returns the index of the last byte it consumed.
func stepSubstByte(word string, j, limit int, unclosed *int, quote *byte) int {
	c := word[j]
	if c == '\\' {
		return j + 1
	}
	if c == '"' || c == '\'' {
		*quote = nextQuoteState(*quote, c)
		return j
	}
	if c == '$' && j+1 < limit && word[j+1] == '(' {
		*unclosed++
		return j + 1
	}
	if c == ')' && *unclosed > 0 {
		*unclosed--
	}
	return j
}

// nextQuoteState returns the quote state after quote byte c outside single quotes: a double
// quote opens or closes double quoting, a single quote opens single quoting only outside it.
func nextQuoteState(quote, c byte) byte {
	switch {
	case c == '"' && quote == 0:
		return '"'
	case c == '"':
		return 0
	case quote == 0:
		return '\''
	default:
		return quote
	}
}

// trimSegmentTrailingParens strips trailing subshell closing parentheses from words.
func trimSegmentTrailingParens(words []string, closers int) []string {
	if closers <= 0 || len(words) == 0 {
		return words
	}
	cleaned := slices.Clone(words)
	remaining := closers
	for i := len(cleaned) - 1; i >= 0 && remaining > 0; i-- {
		w := cleaned[i]
		trailing := countTrailingParens(w)
		if trailing == 0 {
			break
		}
		strip := min(trailing, remaining)
		cleaned[i] = w[:len(w)-strip]
		remaining -= strip
	}
	return slices.DeleteFunc(cleaned, func(s string) bool { return s == "" })
}

// unquoteShellWord removes shell quotes and resolves escape sequences in word,
// joining adjacent quoted and unquoted fragments into the token the shell passes.
func unquoteShellWord(word string) string {
	var sb strings.Builder
	inSingle, inDouble := false, false
	for i := 0; i < len(word); i++ {
		c := word[i]
		if inSingle {
			if c == '\'' {
				inSingle = false
			} else {
				sb.WriteByte(c)
			}
			continue
		}
		if inDouble {
			i = stepDoubleQuotedByte(&sb, word, i, c, &inDouble)
			continue
		}
		i = stepUnquotedByte(&sb, word, i, c, &inSingle, &inDouble)
	}
	return sb.String()
}

// stepDoubleQuotedByte processes one byte inside double quotes, resolving \" \\ \$ \` and \newline.
func stepDoubleQuotedByte(sb *strings.Builder, word string, i int, c byte, inDouble *bool) int {
	if c == '"' {
		*inDouble = false
		return i
	}
	if c == '\\' && i+1 < len(word) {
		next := word[i+1]
		if next == '"' || next == '\\' || next == '$' || next == '`' || next == '\n' {
			sb.WriteByte(next)
			return i + 1
		}
	}
	sb.WriteByte(c)
	return i
}

// stepUnquotedByte processes one byte outside quotes, entering single or double quotes or escaping.
func stepUnquotedByte(sb *strings.Builder, word string, i int, c byte, inSingle, inDouble *bool) int {
	switch c {
	case '\'':
		*inSingle = true
	case '"':
		*inDouble = true
	case '\\':
		if i+1 < len(word) {
			i++
			sb.WriteByte(word[i])
		}
	default:
		sb.WriteByte(c)
	}
	return i
}

// shellWordsText unquotes each shell word in words and joins them with single spaces,
// reproducing the argument text the shell would pass to the command.
func shellWordsText(words []string) string {
	if len(words) == 0 {
		return ""
	}
	unquoted := make([]string, len(words))
	for i := 0; i < len(words) && i < maxForgeTextWords; i++ {
		unquoted[i] = unquoteShellWord(words[i])
	}
	return strings.Join(unquoted, " ")
}

// shellFieldsForGH splits command into shell words with forge.ShellFields, refusing any
// forge.ErrShellReadLimit error, commands over maxForgeTextWords, or parse errors on commands matching ghWriteWordPattern.
func shellFieldsForGH(command string) ([]string, error) {
	fields, err := forge.ShellFields(command)
	if err != nil {
		if errors.Is(err, forge.ErrShellReadLimit) || ghWriteWordPattern.MatchString(command) {
			return nil, err
		}
		return nil, nil
	}
	if len(fields) > maxForgeTextWords {
		return nil, fmt.Errorf("command exceeds %d shell words", maxForgeTextWords)
	}
	return fields, nil
}

// parseSegmentGHCall inspects one command segment, updates workDir on cd or pushd, and
// extracts a ghWriteCall when the segment invokes a scoped gh write subcommand, refusing non-gh or unscoped segments.
func parseSegmentGHCall(fullCommand string, words []string, workDir string) (ghWriteCall, string, bool) {
	word, args, cmdDir, _, _ := walkCommandWords(words, workDir)
	if word == "cd" || word == "pushd" {
		if dirs := changeDirectoryTarget(args, cmdDir); len(dirs) > 0 && dirs[0].dir != "" {
			return ghWriteCall{}, dirs[0].dir, false
		}
		return ghWriteCall{}, workDir, false
	}
	if commandName(word) != "gh" {
		return ghWriteCall{}, workDir, false
	}
	subcmd, rest, ok := classifyGHCall(args)
	if !ok {
		return ghWriteCall{}, workDir, false
	}
	raw := shellWordsText(words)
	files, readsStdin, fileErr := extractGHCallFiles(strings.Join(words, " "), subcmd, rest, cmdDir)
	if readsStdin {
		raw = fullCommand
	}
	return ghWriteCall{raw: raw, files: files, fileErr: fileErr, dir: cmdDir}, workDir, true
}

// classifyGHCall identifies the gh subcommand and remaining arguments for a scoped write
// action or write-mode gh api call, refusing missing, read-only or unscoped subcommands.
func classifyGHCall(args []string) (string, []string, bool) {
	subcmd, next, ok := nextGHPositional(args, 0)
	if !ok {
		return "", nil, false
	}
	if subcmd == "api" {
		return "api", args[next:], isScopedGHAPI(args[next:])
	}
	action, after, ok := nextGHPositional(args, next)
	if !ok || !isScopedGHSubcommand(subcmd, action) {
		return "", nil, false
	}
	return subcmd, args[after:], true
}

// nextGHPositional returns the next non-flag positional token in args starting at start,
// skipping -R/--repo values and flag tokens and refusing when no positional token remains.
func nextGHPositional(args []string, start int) (string, int, bool) {
	for i := start; i < len(args) && i < maxForgeTextWords; i++ {
		tok := unquoteTarget(args[i])
		if tok == "-R" || tok == "--repo" {
			i++
			continue
		}
		if strings.HasPrefix(tok, "-") {
			continue
		}
		return tok, i + 1, true
	}
	return "", len(args), false
}

// isScopedGHSubcommand reports whether subcmd and action form a gh write command that can
// post user-supplied text to the forge, refusing read-only or non-text actions.
func isScopedGHSubcommand(subcmd, action string) bool {
	return scopedGHActions[subcmd][action]
}

// isScopedGHAPI reports whether a gh api argument list targets an issues, pulls or comments
// endpoint with a non-read method or field flags, refusing GET/HEAD reads and unscoped paths.
func isScopedGHAPI(args []string) bool {
	hasPath, hasFields, method := scanGHAPIArgs(args)
	if !hasPath || method == "GET" || method == "HEAD" {
		return false
	}
	return method != "" || hasFields
}

// scanGHAPIArgs scans gh api arguments for a scoped endpoint path, field/input flags and
// an explicit HTTP method, skipping non-field option values.
func scanGHAPIArgs(args []string) (bool, bool, string) {
	hasPath, hasFields := false, false
	method := ""
	for i := 0; i < len(args) && i < maxForgeTextWords; i++ {
		tok := unquoteTarget(args[i])
		if val, next, ok, _ := flagValue(args, i, "-X", "-X", "--method"); ok {
			method, i = strings.ToUpper(unquoteTarget(val)), next
			continue
		}
		if next, ok := parseGHAPIFieldFlag(args, i, tok); ok {
			hasFields, i = true, next
			continue
		}
		i, hasPath = stepGHAPIOtherArg(tok, i, hasPath)
	}
	return hasPath, hasFields, method
}

// stepGHAPIOtherArg advances past value-taking gh api flags and records whether tok matches
// a scoped issues, pulls or comments endpoint path.
func stepGHAPIOtherArg(tok string, i int, hasPath bool) (int, bool) {
	if ghAPIValueFlags[tok] {
		return i + 1, hasPath
	}
	if !strings.HasPrefix(tok, "-") && ghAPIScopedPath.MatchString(tok) {
		return i, true
	}
	return i, hasPath
}

// parseGHAPIFieldFlag checks whether args[i] is a gh api field or input flag (-f, -F,
// --field, --raw-field, --input) and returns the next index when matched, refusing other flags.
func parseGHAPIFieldFlag(args []string, i int, tok string) (int, bool) {
	if _, next, ok, _ := flagValue(args, i, "-f", "-f", "--raw-field", "--input"); ok {
		return next, true
	}
	if _, next, ok, _ := flagValue(args, i, "-F", "-F", "--field"); ok {
		return next, true
	}
	return i, false
}

// extractGHCallFiles collects file paths referenced by gh flags and $(cat <path>)
// substitutions in segment, refusing malformed paths or calls exceeding maxForgeTextFiles.
func extractGHCallFiles(segment, subcmd string, args []string, workDir string) ([]ghTextFile, bool, string) {
	files, readsStdin, fileErr := collectFlagFiles(subcmd, args, workDir)
	if fileErr != "" {
		return nil, readsStdin, fileErr
	}
	catPaths, exceeded := extractCatSubstFiles(segment)
	if exceeded {
		return nil, readsStdin, fmt.Sprintf("gh command exceeds %d named files", maxForgeTextFiles)
	}
	for i := 0; i < len(catPaths) && i < maxForgeTextFiles; i++ {
		var ok bool
		files, ok, fileErr = appendGHTextFile(files, catPaths[i], workDir)
		if !ok {
			return nil, readsStdin, fileErr
		}
	}
	return files, readsStdin, ""
}

// collectFlagFiles scans args for file-bearing flags on subcmd, marks standard-input
// targets (-, /dev/stdin, /dev/fd/0), and refuses missing flag values or file counts over maxForgeTextFiles.
func collectFlagFiles(subcmd string, args []string, workDir string) ([]ghTextFile, bool, string) {
	var files []ghTextFile
	readsStdin := false
	for i := 0; i < len(args) && i < maxForgeTextWords; i++ {
		rawPath, next, matched, valid := extractFlagFile(subcmd, args, i)
		i = next
		if !matched {
			continue
		}
		if !valid {
			return nil, readsStdin, "cannot read file " + rawPath
		}
		if isStdinTarget(rawPath) {
			readsStdin = true
			continue
		}
		var ok bool
		var fileErr string
		files, ok, fileErr = appendGHTextFile(files, rawPath, workDir)
		if !ok {
			return nil, readsStdin, fileErr
		}
	}
	return files, readsStdin, ""
}

// isStdinTarget reports whether rawPath names standard input (-, /dev/stdin or /dev/fd/0)
// rather than a regular file path, refusing other paths so they are resolved on disk.
func isStdinTarget(rawPath string) bool {
	target := unquoteTarget(strings.TrimSpace(rawPath))
	if target == "-" {
		return true
	}
	cleaned := filepath.ToSlash(filepath.Clean(target))
	return cleaned == "/dev/stdin" || cleaned == "/dev/fd/0"
}

// appendGHTextFile resolves rawPath against workDir and appends it to files, refusing
// empty paths or lists that already hold maxForgeTextFiles entries.
func appendGHTextFile(files []ghTextFile, rawPath, workDir string) ([]ghTextFile, bool, string) {
	file, ok := resolveGHTextFile(rawPath, workDir)
	if !ok {
		return nil, false, "cannot read file " + rawPath
	}
	if len(files) >= maxForgeTextFiles {
		return nil, false, fmt.Sprintf("gh command exceeds %d named files", maxForgeTextFiles)
	}
	return append(files, file), true, ""
}

// extractFlagFile extracts a file path argument from a gh body/notes flag or a gh api
// --input or @file field at args[i], refusing non-file flags or empty file operands.
func extractFlagFile(subcmd string, args []string, i int) (string, int, bool, bool) {
	if subcmd != "api" {
		return flagValue(args, i, "-F", "--body-file", "--notes-file", "-F")
	}
	if val, next, ok, valid := flagValue(args, i, "", "--input"); ok {
		return val, next, true, valid
	}
	val, next, ok, valid := flagValue(args, i, "-F", "-F", "--field")
	if !ok || !valid {
		return val, next, ok, valid
	}
	path, matched, pathValid := extractAPIFieldFile(val)
	return path, next, matched, pathValid
}

// flagValue parses separate, inline (=) or attached short flag values at args[i] for
// names, refusing a matched flag whose value is missing or empty.
func flagValue(args []string, i int, short string, names ...string) (string, int, bool, bool) {
	tok := unquoteTarget(args[i])
	for j := 0; j < len(names) && j < 4; j++ {
		if tok == names[j] {
			if i+1 >= len(args) {
				return tok, i, true, false
			}
			return args[i+1], i + 1, true, true
		}
		if val, ok := strings.CutPrefix(tok, names[j]+"="); ok {
			return val, i, true, val != ""
		}
	}
	if short != "" && strings.HasPrefix(tok, short) && len(tok) > len(short) {
		return tok[len(short):], i, true, true
	}
	return "", i, false, false
}

// extractAPIFieldFile extracts the file path from a gh api key=@path field assignment,
// refusing assignments without @ or with an empty path after @.
func extractAPIFieldFile(raw string) (string, bool, bool) {
	_, val, ok := strings.Cut(unquoteTarget(raw), "=")
	if !ok {
		return "", false, false
	}
	val = unquoteTarget(strings.TrimSpace(val))
	if !strings.HasPrefix(val, "@") {
		return "", false, false
	}
	path := unquoteTarget(strings.TrimSpace(val[1:]))
	if path == "" {
		return val, true, false
	}
	return path, true, true
}

// extractCatSubstFiles extracts file operands from $(cat <path>) command substitutions in
// segment, ignoring flag arguments and refusing segments with more than maxForgeTextFiles matches.
func extractCatSubstFiles(segment string) ([]string, bool) {
	matches := catSubstPattern.FindAllStringSubmatch(segment, maxForgeTextFiles+1)
	if len(matches) > maxForgeTextFiles {
		return nil, true
	}
	var paths []string
	for i := 0; i < len(matches) && i < maxForgeTextFiles; i++ {
		arg := unquoteTarget(matches[i][1])
		if arg != "" && !strings.HasPrefix(arg, "-") {
			paths = append(paths, matches[i][1])
		}
	}
	return paths, false
}

// resolveGHTextFile cleans and resolves rawPath against workDir into a ghTextFile with its
// refusal label, refusing empty paths after unquoting.
func resolveGHTextFile(rawPath, workDir string) (ghTextFile, bool) {
	cleaned := homeTarget(unquoteTarget(strings.TrimSpace(rawPath)))
	if cleaned == "" {
		return ghTextFile{label: rawPath}, false
	}
	resolved := cleaned
	if !filepath.IsAbs(resolved) && workDir != "" {
		resolved = filepath.Join(workDir, resolved)
	}
	resolved = filepath.Clean(resolved)
	label := cleaned
	if label != resolved {
		label = fmt.Sprintf("%s (%s)", cleaned, resolved)
	}
	return ghTextFile{label: label, resolved: resolved, word: unquoteShellWord(strings.TrimSpace(rawPath))}, true
}

// readForgeTextFile reads up to maxForgeTextFileBytes from path within forgeTextReadTimeout,
// refusing expired contexts, non-regular or unreadable files, and files over 1 MiB.
func readForgeTextFile(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, forgeTextReadTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := util.ReadFileLimited(path, maxForgeTextFileBytes)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return string(data), nil
}

// heredocWritesFile reports a literal output target matching the file argument.
func heredocWritesFile(bodies []hiss.HereDoc, word string) bool {
	for i := 0; i < len(bodies) && i < maxForgeTextWords; i++ {
		if bodies[i].Data() && bodies[i].Target != "" && unquoteShellWord(bodies[i].Target) == word {
			return true
		}
	}
	return false
}

// heredocFinding returns the first foreign reference in the supplied bodies.
func heredocFinding(bodies []hiss.HereDoc, own []string) string {
	for i := 0; i < len(bodies) && i < maxForgeTextWords; i++ {
		if findings := forge.ForeignReferenceFindings(bodies[i].Text, "here-document", own); bodies[i].Data() && len(findings) > 0 {
			return findings[0]
		}
	}
	return ""
}
