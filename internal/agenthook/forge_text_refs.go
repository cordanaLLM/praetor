// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
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
	ghAPIScopedPath    = regexp.MustCompile(`(?i)(?:^|/)(?:issues|pulls|comments)(?:/|$|\?)`)
	ghWriteWordPattern = regexp.MustCompile(ghWriteWordSource(scopedGHActions))
	catSubstPattern    = regexp.MustCompile(`\$\(\s*cat(?:\s+--)?\s+([^\s<>|;&)]+)\s*\)`)
	scopedGHActions    = map[string]map[string]bool{
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
}

// ghWriteCall holds the raw command text, resolved file arguments and any file-collection
// refusal for a single scoped gh write invocation.
type ghWriteCall struct {
	raw     string
	files   []ghTextFile
	fileErr string
}

// evaluateForgeTextRefs inspects every scoped gh write call in canonical.Command and
// refuses the command when shell parsing fails, trusted organisations cannot be resolved,
// or any inline text or named file contains a foreign forge reference.
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
	own, noOrgList, err := resolveHookOwnOrganizations(ctx, root, in.Getenv)
	if err != nil {
		return forgeTextDeny(err.Error(), false)
	}
	for i := 0; i < len(calls) && i < maxForgeTextSegments; i++ {
		if verdict := judgeGHWriteCall(ctx, calls[i], own, noOrgList); verdict.Outcome != Allow {
			return verdict
		}
	}
	return Verdict{Outcome: Allow}
}

// judgeGHWriteCall checks a single ghWriteCall and its named files against own, refusing
// file collection errors, unreadable or oversized files, and foreign forge references.
func judgeGHWriteCall(ctx context.Context, call ghWriteCall, own []string, noOrgList bool) Verdict {
	if call.fileErr != "" {
		return forgeTextDeny(call.fileErr, noOrgList)
	}
	if findings := forge.ForeignReferenceFindings(call.raw, "gh command", own); len(findings) > 0 {
		return forgeTextDeny(findings[0], noOrgList)
	}
	for i := 0; i < len(call.files) && i < maxForgeTextFiles; i++ {
		text, err := readForgeTextFile(ctx, call.files[i].resolved)
		if err != nil {
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
// settings, the repository manifest and repository identity, refusing invalid manifests or settings.
func resolveHookOwnOrganizations(ctx context.Context, root string, getenv func(string) string) ([]string, bool, error) {
	opForge, err := loadOperatorForgeSettings(ctx, getenv)
	if err != nil {
		return nil, false, err
	}
	manifestPath := ""
	if root != "" {
		manifestPath = filepath.Join(root, manifestName)
	}
	own, err := forge.ResolveOwnOrganizations(ctx, root, manifestPath, opForge.OwnOrganizations, getenv)
	if err != nil {
		return nil, false, err
	}
	noOrgList := len(opForge.OwnOrganizations) == 0 && len(own) <= 1
	return own, noOrgList, nil
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
// and returns every scoped gh write call, refusing commands that exceed maxForgeTextSegments or fail shell parsing.
func collectGHWriteCalls(command, baseDir string) ([]ghWriteCall, error) {
	fields, err := shellFieldsForGH(command)
	if err != nil || len(fields) == 0 {
		return nil, err
	}
	var calls []ghWriteCall
	workDir := baseDir
	start, segments := 0, 0
	for i := 0; i <= len(fields) && i <= maxForgeTextWords; i++ {
		if i < len(fields) && !targetCommandSeparator(fields[i]) {
			continue
		}
		segments++
		if segments > maxForgeTextSegments {
			return nil, fmt.Errorf("command exceeds %d command segments", maxForgeTextSegments)
		}
		call, nextDir, ok := parseSegmentGHCall(command, fields[start:i], workDir)
		workDir = nextDir
		if ok {
			calls = append(calls, call)
		}
		start = i + 1
	}
	return calls, nil
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
	raw := strings.Join(words, " ")
	files, readsStdin, fileErr := extractGHCallFiles(raw, subcmd, rest, cmdDir)
	if readsStdin {
		raw = fullCommand
	}
	return ghWriteCall{raw: raw, files: files, fileErr: fileErr}, workDir, true
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
	return ghTextFile{label: label, resolved: resolved}, true
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
