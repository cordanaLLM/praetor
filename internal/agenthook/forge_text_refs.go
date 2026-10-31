// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
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
	ghWriteWordPattern = regexp.MustCompile(`(?i)\bgh\b[\s\S]*\b(issue\b[\s\S]*\b(create|edit|comment)|pr\b[\s\S]*\b(create|edit|comment|review)|release\b[\s\S]*\b(create|edit)|api\b[\s\S]*(issues|pulls|comments))\b`)
	catSubstPattern    = regexp.MustCompile(`\$\(\s*cat(?:\s+--)?\s+([^\s<>|;&)]+)\s*\)`)
	ghAPIValueFlags    = map[string]bool{
		"-H": true, "--header": true, "--hostname": true, "--cache": true,
		"-q": true, "--jq": true, "-p": true, "--preview": true, "-t": true, "--template": true,
	}
)

type ghTextFile struct {
	label    string
	resolved string
}

type ghWriteCall struct {
	raw     string
	files   []ghTextFile
	fileErr string
}

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

func judgeGHWriteCall(ctx context.Context, call ghWriteCall, own []string, noOrgList bool) Verdict {
	if call.fileErr != "" {
		return forgeTextDeny("cannot read file "+call.fileErr, noOrgList)
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

func forgeTextDeny(detail string, noOrgList bool) Verdict {
	if noOrgList {
		detail += "; " + forgeTextNoOrgNotice
	}
	return Verdict{
		Outcome: Deny,
		Reason:  refusal("HISS", forgeTextRefRuleName+": "+detail+"\n"+forgeTextAllowedForms),
	}
}

func resolveHookOwnOrganizations(ctx context.Context, root string, getenv func(string) string) ([]string, bool, error) {
	opForge, err := loadOperatorForgeSettings(ctx, getenv)
	if err != nil {
		return nil, false, err
	}
	manifestPath := ""
	if root != "" {
		manifestPath = filepath.Join(root, manifestName)
	}
	own, err := forge.ResolveOwnOrganizations(ctx, root, manifestPath, opForge.OwnOrganizations)
	if err != nil {
		return nil, false, err
	}
	noOrgList := len(opForge.OwnOrganizations) == 0 && len(own) <= 1
	return own, noOrgList, nil
}

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

func collectGHWriteCalls(command, baseDir string) ([]ghWriteCall, error) {
	fields, err := forge.ShellFields(command)
	if err != nil {
		if ghWriteWordPattern.MatchString(command) {
			return nil, err
		}
		return nil, nil
	}
	var calls []ghWriteCall
	workDir := baseDir
	start := 0
	for i := 0; i <= len(fields) && i <= maxForgeTextWords && len(calls) < maxForgeTextSegments; i++ {
		if i < len(fields) && !targetCommandSeparator(fields[i]) {
			continue
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

func isScopedGHSubcommand(subcmd, action string) bool {
	switch subcmd {
	case "issue":
		return action == "create" || action == "edit" || action == "comment"
	case "pr":
		return action == "create" || action == "edit" || action == "comment" || action == "review"
	case "release":
		return action == "create" || action == "edit"
	default:
		return false
	}
}

func isScopedGHAPI(args []string) bool {
	hasPath, hasFields, method := scanGHAPIArgs(args)
	if !hasPath || method == "GET" || method == "HEAD" {
		return false
	}
	return method != "" || hasFields
}

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

func stepGHAPIOtherArg(tok string, i int, hasPath bool) (int, bool) {
	if ghAPIValueFlags[tok] {
		return i + 1, hasPath
	}
	if !strings.HasPrefix(tok, "-") && ghAPIScopedPath.MatchString(tok) {
		return i, true
	}
	return i, hasPath
}

func parseGHAPIFieldFlag(args []string, i int, tok string) (int, bool) {
	if _, next, ok, _ := flagValue(args, i, "-f", "-f", "--raw-field", "--input"); ok {
		return next, true
	}
	if _, next, ok, _ := flagValue(args, i, "-F", "-F", "--field"); ok {
		return next, true
	}
	return i, false
}

func extractGHCallFiles(segment, subcmd string, args []string, workDir string) ([]ghTextFile, bool, string) {
	files, readsStdin, fileErr := collectFlagFiles(subcmd, args, workDir)
	if fileErr != "" {
		return nil, readsStdin, fileErr
	}
	catPaths := extractCatSubstFiles(segment)
	for i := 0; i < len(catPaths) && len(files) < maxForgeTextFiles; i++ {
		file, ok := resolveGHTextFile(catPaths[i], workDir)
		if !ok {
			return nil, readsStdin, catPaths[i]
		}
		files = append(files, file)
	}
	return files, readsStdin, ""
}

func collectFlagFiles(subcmd string, args []string, workDir string) ([]ghTextFile, bool, string) {
	var files []ghTextFile
	readsStdin := false
	for i := 0; i < len(args) && i < maxForgeTextWords && len(files) < maxForgeTextFiles; i++ {
		rawPath, next, matched, valid := extractFlagFile(subcmd, args, i)
		i = next
		if !matched {
			continue
		}
		if !valid {
			return nil, readsStdin, rawPath
		}
		if unquoteTarget(rawPath) == "-" {
			readsStdin = true
			continue
		}
		file, ok := resolveGHTextFile(rawPath, workDir)
		if !ok {
			return nil, readsStdin, rawPath
		}
		files = append(files, file)
	}
	return files, readsStdin, ""
}

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

func extractCatSubstFiles(segment string) []string {
	matches := catSubstPattern.FindAllStringSubmatch(segment, maxForgeTextFiles)
	var paths []string
	for i := 0; i < len(matches) && i < maxForgeTextFiles; i++ {
		arg := unquoteTarget(matches[i][1])
		if arg != "" && !strings.HasPrefix(arg, "-") {
			paths = append(paths, matches[i][1])
		}
	}
	return paths
}

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
