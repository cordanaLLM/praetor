// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

// maxLinterCandidates bounds the linters checked by one measurement (HISS-02).
const maxLinterCandidates = 64

// maxMakePrereqDepth bounds the recursion depth when following Makefile target prerequisites (HISS-02).
const maxMakePrereqDepth = 4

// maxMakeTargets bounds the total Makefile targets visited during one expansion (HISS-02).
const maxMakeTargets = 64

// maxLogicalLines bounds the logical lines parsed from one Makefile (HISS-02).
const maxLogicalLines = 4096

// MeasureWorkflowLinters inspects every workflow document directly under .github/workflows in
// repoPath and reports which candidate linters are run by at least one runnable step. A step
// counts as running a linter when its run: or uses: names the tool, a runner executes it, or
// a root Makefile target (or its prerequisites) runs it.
func MeasureWorkflowLinters(ctx context.Context, repoPath string, candidates []string) (map[string]bool, error) {
	if ctx == nil {
		return nil, errors.New("measure workflow linters requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return make(map[string]bool), nil
	}
	if len(candidates) > maxLinterCandidates {
		return nil, fmt.Errorf("linter candidates count exceeds %d", maxLinterCandidates)
	}

	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}

	makeTargets := readRootMakefile(ctx, repoPath)
	golangciEnabled, hasGolangci := readGolangciConfig(ctx, repoPath)
	present := make(map[string]bool, len(candidates))
	golangciRuns := false

	if err := scanWorkflowFiles(ctx, repoPath, files, candidates, makeTargets, present, &golangciRuns); err != nil {
		return nil, err
	}

	if golangciRuns && hasGolangci {
		applyGolangciPresent(candidates, golangciEnabled, present)
	}

	return present, nil
}

func scanWorkflowFiles(ctx context.Context, repoPath string, files []workflowFile, candidates []string, makeTargets map[string]*makeTargetDef, present map[string]bool, golangciRuns *bool) error {
	pkgCache := newPackageScriptsCache()
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		spec, err := ghworkflow.Parse(files[i].Data)
		if err != nil {
			return fmt.Errorf("workflow %s: %w", files[i].Name, err)
		}
		scanWorkflowSteps(ctx, repoPath, spec, candidates, makeTargets, pkgCache, present, golangciRuns)
	}
	return nil
}

func applyGolangciPresent(candidates []string, golangciEnabled map[string]bool, present map[string]bool) {
	for c := 0; c < len(candidates) && c < maxLinterCandidates; c++ {
		tool := candidates[c]
		if !present[tool] && golangciEnabled[strings.ToLower(tool)] {
			present[tool] = true
		}
	}
}

func scanSingleStep(ctx context.Context, repoPath string, step ghworkflow.Step, candidates []string, makeTargets map[string]*makeTargetDef, pkgCache *packageScriptsCache, present map[string]bool, golangciRuns *bool) {
	if ghworkflow.NeverRuns(step.If) || advisoryJob(step.ContinueOnError) {
		return
	}
	pkgScripts := pkgCache.get(ctx, repoPath, step.WorkingDirectory)
	if !*golangciRuns && stepRunsTool(ctx, step, "golangci-lint", makeTargets, pkgScripts) {
		*golangciRuns = true
	}
	for c := 0; c < len(candidates) && c < maxLinterCandidates; c++ {
		tool := candidates[c]
		if !present[tool] && stepRunsTool(ctx, step, tool, makeTargets, pkgScripts) {
			present[tool] = true
		}
	}
}

func scanJobSteps(ctx context.Context, repoPath string, job ghworkflow.Job, candidates []string, makeTargets map[string]*makeTargetDef, pkgCache *packageScriptsCache, present map[string]bool, golangciRuns *bool) {
	if ghworkflow.NeverRuns(job.If) || advisoryJob(job.ContinueOnError) {
		return
	}
	for s := 0; s < len(job.Steps) && s < ghworkflow.MaxStepsPerJob; s++ {
		scanSingleStep(ctx, repoPath, job.Steps[s], candidates, makeTargets, pkgCache, present, golangciRuns)
	}
}

func scanWorkflowSteps(ctx context.Context, repoPath string, spec ghworkflow.Spec, candidates []string, makeTargets map[string]*makeTargetDef, pkgCache *packageScriptsCache, present map[string]bool, golangciRuns *bool) {
	for _, job := range spec.Jobs {
		scanJobSteps(ctx, repoPath, job, candidates, makeTargets, pkgCache, present, golangciRuns)
	}
}

// stepRunsTool reports whether step's run: or uses: names the tool or runs it via a runner form or makefile target.
func stepRunsTool(ctx context.Context, step ghworkflow.Step, tool string, makeTargets map[string]*makeTargetDef, pkgScripts map[string]string) bool {
	if step.Uses != "" && stepActionRunsTool(step.Uses, tool) {
		return true
	}
	if step.Run != "" && scriptRunsTool(ctx, step.Run, tool, makeTargets, pkgScripts) {
		return true
	}
	return false
}

// stepActionRunsTool reports whether step's uses: names tool as a dash-separated word in its repository name.
func stepActionRunsTool(uses, tool string) bool {
	action := strings.TrimPrefix(uses, "docker://")
	action, _, _ = strings.Cut(action, "@")
	parts := strings.Split(action, "/")
	var repo string
	switch {
	case len(parts) >= 2 && parts[0] != "." && parts[0] != "":
		repo = parts[1]
	case len(parts) >= 2:
		repo = parts[len(parts)-1]
	case len(parts) == 1:
		repo = parts[0]
	default:
		return false
	}
	repo, _, _ = strings.Cut(repo, ":")
	return matchesDashWord(strings.ToLower(repo), strings.ToLower(tool))
}

func matchesDashWord(s, t string) bool {
	if s == t {
		return true
	}
	return strings.HasPrefix(s, t+"-") || strings.HasSuffix(s, "-"+t) || strings.Contains(s, "-"+t+"-")
}

func scriptRunsTool(ctx context.Context, run, tool string, makeTargets map[string]*makeTargetDef, pkgScripts map[string]string) bool {
	fields, err := scriptFields(run)
	if err != nil {
		return false
	}
	commands := scriptCommands(fields)
	toolLower := strings.ToLower(tool)
	for i := 0; i < len(commands) && i < maxRunScriptFields; i++ {
		cmd := parseShellCommand(commands[i])
		if commandRunsTool(ctx, cmd, toolLower, makeTargets, pkgScripts) {
			return true
		}
	}
	return false
}

func directProgramRunsTool(cmd shellCommand, toolLower string, pkgScripts map[string]string) bool {
	if cmd.program == toolLower {
		return true
	}
	if checkRunnerTool(cmd, toolLower, pkgScripts) {
		return true
	}
	return checkScriptTool(cmd, toolLower)
}

func directCommandRunsTool(cmd shellCommand, toolLower string, pkgScripts map[string]string) bool {
	curr := cmd
	for step := 0; step < 4; step++ {
		if directProgramRunsTool(curr, toolLower, pkgScripts) {
			return true
		}
		if curr.program == "find" {
			return findExecRunsTool(curr.args, toolLower, pkgScripts)
		}
		words, ok := unwrapWrapper(curr)
		if !ok || len(words) == 0 {
			break
		}
		curr = parseShellCommand(words)
	}
	return false
}

func commandRunsTool(ctx context.Context, cmd shellCommand, toolLower string, makeTargets map[string]*makeTargetDef, pkgScripts map[string]string) bool {
	if directCommandRunsTool(cmd, toolLower, pkgScripts) {
		return true
	}
	if cmd.program == "make" && makeTargets != nil {
		return makeRunsTool(ctx, cmd, toolLower, makeTargets)
	}
	if len(pkgScripts) > 0 && packageScriptRunsTool(ctx, cmd, toolLower, pkgScripts, makeTargets) {
		return true
	}
	return false
}

func checkGoRunner(args []string, toolLower string) bool {
	if len(args) == 0 {
		return false
	}
	if toolLower == "govet" && args[0] == "vet" {
		return true
	}
	if args[0] == "tool" || args[0] == "run" {
		target := firstGoTarget(args[1:])
		clean := cleanRunnerSpec(target)
		cleanLower := strings.ToLower(clean)
		cleanTrim := strings.TrimPrefix(cleanLower, "./")
		cleanTrim = strings.TrimSuffix(cleanTrim, "/")
		if toolLower == "gitleaks" && (cleanTrim == "tools/secrets/gate" || strings.HasPrefix(cleanTrim, "tools/secrets/gate/")) {
			return true
		}
		return cleanLower == toolLower || strings.HasSuffix(cleanLower, "/"+toolLower)
	}
	return false
}

func checkCargoRunner(args []string, toolLower string) bool {
	if toolLower != "clippy" || len(args) == 0 {
		return false
	}
	if args[0] == "clippy" {
		return true
	}
	return strings.HasPrefix(args[0], "+") && len(args) > 1 && args[1] == "clippy"
}

func checkRunnerTool(cmd shellCommand, toolLower string, pkgScripts map[string]string) bool {
	switch cmd.program {
	case "go":
		return checkGoRunner(cmd.args, toolLower)
	case "cargo":
		return checkCargoRunner(cmd.args, toolLower)
	case "npx", "bunx", "uvx":
		return checkJsOrUvxRunner(cmd.program, cmd.args, toolLower)
	case "pipx":
		return checkPipxRunner(cmd.args, toolLower)
	case "pnpm":
		return checkPnpmRunner(cmd.args, toolLower)
	case "npm":
		return checkNpmRunner(cmd.args, toolLower)
	case "yarn":
		return checkYarnRunner(cmd.args, toolLower, pkgScripts)
	case "bun":
		return checkBunRunner(cmd.args, toolLower)
	case "docker", "podman":
		return checkContainerRun(cmd.args, toolLower)
	default:
		return false
	}
}

func firstGoTarget(args []string) string {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			if !strings.Contains(arg, "=") && isGoFlagWithValue(arg) && i+1 < len(args) {
				i++
			}
			continue
		}
		return arg
	}
	return ""
}

func isGoFlagWithValue(flag string) bool {
	switch flag {
	case "-modfile", "-mod", "-C", "-tags", "-overlay", "-pkgdir", "-p":
		return true
	}
	return false
}

func checkScriptTool(cmd shellCommand, toolLower string) bool {
	isInterp := isInterpreter(cmd.program)
	isRepoPath := strings.ContainsAny(cmd.word, "/\\")
	if !isInterp && !isRepoPath {
		return false
	}
	if hasToolOption(cmd.args, toolLower) {
		return true
	}
	var scriptPath string
	if isInterp {
		scriptPath = firstNonFlagArg(cmd.args)
	} else {
		scriptPath = cmd.word
	}
	if scriptPath == "" {
		return false
	}
	return scriptPathMatchesTool(scriptPath, toolLower)
}

func hasToolOption(args []string, toolLower string) bool {
	target := "--" + toolLower
	prefix := target + "="
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		arg := strings.ToLower(args[i])
		if arg == target || strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
}

func isInterpreter(prog string) bool {
	switch prog {
	case "python", "python3", "node", "deno", "bun", "bash", "sh", "ruby", "perl", "pwsh":
		return true
	}
	return false
}

func scriptPathMatchesTool(scriptPath, toolLower string) bool {
	normalized := filepath.ToSlash(scriptPath)
	parts := strings.Split(normalized, "/")
	for i := 0; i < len(parts)-1; i++ {
		comp := strings.ToLower(parts[i])
		if comp == "." || comp == "" {
			continue
		}
		if comp == toolLower {
			return true
		}
	}
	base := parts[len(parts)-1]
	words := strings.FieldsFunc(base, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
	for _, w := range words {
		if strings.ToLower(w) == toolLower {
			return true
		}
	}
	return false
}

func firstNonFlagArg(args []string) string {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		if !strings.HasPrefix(args[i], "-") {
			return args[i]
		}
	}
	return ""
}

func argsAfterWord(args []string, word string) []string {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		if args[i] == word && i+1 < len(args) {
			return args[i+1:]
		}
	}
	return nil
}

func cleanRunnerSpec(raw string) string {
	s := unquoter.Replace(raw)
	s = stripVarRefs(s)
	s = stripVersionSuffix(s)
	return strings.TrimSpace(s)
}

func stripVarRefs(s string) string {
	for i := 0; i < maxRunScriptFields; i++ {
		start := strings.Index(s, "${")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			break
		}
		s = s[:start] + s[start+end+1:]
	}
	return s
}

func stripVersionSuffix(spec string) string {
	cutIdx := -1
	for _, sep := range []string{"==", ">=", "~="} {
		if idx := strings.Index(spec, sep); idx >= 0 {
			if cutIdx < 0 || idx < cutIdx {
				cutIdx = idx
			}
		}
	}
	if cutIdx >= 0 {
		spec = spec[:cutIdx]
	}
	if atIdx := strings.LastIndex(spec, "@"); atIdx > 0 {
		spec = spec[:atIdx]
	}
	if colonIdx := strings.LastIndex(spec, ":"); colonIdx > 0 {
		lastSlash := strings.LastIndex(spec, "/")
		if colonIdx > lastSlash {
			spec = spec[:colonIdx]
		}
	}
	return spec
}

func checkJsOrUvxRunner(prog string, args []string, toolLower string) bool {
	t := firstRunnerTarget(args)
	if t == "" {
		return false
	}
	clean := cleanRunnerSpec(t)
	base := programName(clean)
	if prog == "uvx" {
		return base == toolLower
	}
	return base == toolLower || matchesDashWord(base, toolLower)
}

func firstRunnerTarget(args []string) string {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			if isJsRunnerValOption(arg) && i+1 < len(args) {
				i++
			}
			continue
		}
		if arg == "run" && i+1 < len(args) {
			return firstNonFlagArg(args[i+1:])
		}
		return arg
	}
	return ""
}

func isJsRunnerValOption(opt string) bool {
	switch opt {
	case "-p", "--package", "-c", "--from", "--with":
		return true
	}
	return false
}

func checkPipxRunner(args []string, toolLower string) bool {
	rest := argsAfterWord(args, "run")
	if len(rest) == 0 {
		return false
	}
	t := firstPipxTarget(rest)
	if t == "" {
		return false
	}
	clean := cleanRunnerSpec(t)
	base := programName(clean)
	return base == toolLower
}

func firstPipxTarget(args []string) string {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			if isPipxValOption(arg) && i+1 < len(args) {
				i++
			}
			continue
		}
		return arg
	}
	return ""
}

func isPipxValOption(opt string) bool {
	switch opt {
	case "--spec", "--index-url", "--pip-args", "--python":
		return true
	}
	return false
}

func checkPnpmRunner(args []string, toolLower string) bool {
	rest := argsAfterWord(args, "dlx")
	if len(rest) == 0 {
		rest = argsAfterWord(args, "exec")
	}
	if len(rest) == 0 {
		return false
	}
	t := firstNonFlagArg(rest)
	if t == "" {
		return false
	}
	clean := cleanRunnerSpec(t)
	base := programName(clean)
	return base == toolLower || matchesDashWord(base, toolLower)
}

func checkContainerRun(args []string, toolLower string) bool {
	rest := argsAfterWord(args, "run")
	if len(rest) == 0 {
		return false
	}
	for i := 0; i < len(rest) && i < maxRunScriptFields; i++ {
		arg := rest[i]
		if arg == "--" {
			if i+1 < len(rest) {
				return containerImageMatches(rest[i+1], toolLower)
			}
			return false
		}
		if strings.HasPrefix(arg, "-") {
			if !strings.Contains(arg, "=") && isDockerValOption(arg) && i+1 < len(rest) {
				i++
			}
			continue
		}
		return containerImageMatches(arg, toolLower)
	}
	return false
}

func containerImageMatches(image, toolLower string) bool {
	clean := cleanRunnerSpec(image)
	lastSlash := strings.LastIndex(clean, "/")
	lastSegment := clean[lastSlash+1:]
	return strings.ToLower(lastSegment) == toolLower
}

func isDockerValOption(opt string) bool {
	switch opt {
	case "-v", "--volume", "-e", "--env", "--env-file", "-w", "--workdir", "-u", "--user",
		"-p", "--publish", "--name", "--entrypoint", "--network", "--net", "--platform",
		"--mount", "--cpus", "-m", "--memory", "--shm-size", "--pull", "--label",
		"--restart", "--device", "--user-group", "--security-opt", "--log-opt",
		"--log-driver", "--cap-add", "--cap-drop", "-l", "-h", "--hostname",
		"--add-host", "--dns", "--cidfile", "--ipc", "--pid", "--uts", "--cgroup-parent":
		return true
	}
	return false
}

func execSliceRunsTool(execArgs []string, toolLower string, pkgScripts map[string]string) bool {
	subCmd := parseShellCommand(execArgs)
	if directProgramRunsTool(subCmd, toolLower, pkgScripts) {
		return true
	}
	unwrap, ok := unwrapWrapper(subCmd)
	if !ok || len(unwrap) == 0 {
		return false
	}
	innerCmd := parseShellCommand(unwrap)
	return directProgramRunsTool(innerCmd, toolLower, pkgScripts)
}

func isFindExecArg(arg string) bool {
	return arg == "-exec" || arg == "-execdir"
}

func findExecRunsTool(args []string, toolLower string, pkgScripts map[string]string) bool {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		if isFindExecArg(args[i]) && i+1 < len(args) {
			if execSliceRunsTool(args[i+1:], toolLower, pkgScripts) {
				return true
			}
		}
	}
	return false
}

func unwrapWrapper(cmd shellCommand) ([]string, bool) {
	switch cmd.program {
	case "xargs":
		res := unwrapXargs(cmd.args)
		return res, len(res) > 0
	case "env":
		return unwrapEnv(cmd.args)
	case "sudo":
		return unwrapSudo(cmd.args)
	case "time":
		return unwrapTime(cmd.args)
	case "nice":
		return unwrapNice(cmd.args)
	}
	return nil, false
}

func unwrapXargs(args []string) []string {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 < len(args) {
				return args[i+1:]
			}
			return nil
		}
		if strings.HasPrefix(arg, "-") {
			if isXargsValOption(arg) && i+1 < len(args) {
				i++
			}
			continue
		}
		return args[i:]
	}
	return nil
}

func isXargsValOption(opt string) bool {
	switch opt {
	case "-I", "-n", "-P", "-L", "-s", "-d", "-a", "-E":
		return true
	}
	return false
}

func unwrapEnv(args []string) ([]string, bool) {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 < len(args) {
				return args[i+1:], true
			}
			return nil, false
		}
		if strings.HasPrefix(arg, "-") {
			if isEnvValOption(arg) && i+1 < len(args) {
				i++
			}
			continue
		}
		if shellAssignmentWord.MatchString(arg) {
			continue
		}
		return args[i:], true
	}
	return nil, false
}

func isEnvValOption(opt string) bool {
	switch opt {
	case "-u", "--unset", "-C", "--chdir", "-S", "--split-string", "-a", "--argv0":
		return true
	}
	return false
}

func unwrapSudo(args []string) ([]string, bool) {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 < len(args) {
				return args[i+1:], true
			}
			return nil, false
		}
		if strings.HasPrefix(arg, "-") {
			if isSudoValOption(arg) && i+1 < len(args) {
				i++
			}
			continue
		}
		return args[i:], true
	}
	return nil, false
}

func isSudoValOption(opt string) bool {
	switch opt {
	case "-C", "--close-from", "-D", "--chdir", "-g", "--group", "-h", "--host",
		"-p", "--prompt", "-R", "--chroot", "-T", "--command-timeout", "-U", "--other-user", "-u", "--user":
		return true
	}
	return false
}

// isTimeValOption reports whether a time option takes a following value argument.
func isTimeValOption(opt string) bool {
	switch opt {
	case "-f", "--format", "-o", "--output":
		return true
	}
	return false
}

func unwrapTime(args []string) ([]string, bool) {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 < len(args) {
				return args[i+1:], true
			}
			return nil, false
		}
		if strings.HasPrefix(arg, "-") {
			if isTimeValOption(arg) && i+1 < len(args) {
				i++
			}
			continue
		}
		return args[i:], true
	}
	return nil, false
}

func unwrapNice(args []string) ([]string, bool) {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 < len(args) {
				return args[i+1:], true
			}
			return nil, false
		}
		if strings.HasPrefix(arg, "-") {
			if (arg == "-n" || arg == "--adjustment") && i+1 < len(args) {
				i++
			}
			continue
		}
		return args[i:], true
	}
	return nil, false
}

type makeTargetDef struct {
	prerequisites []string
	recipes       []string
}

const maxMakeVariables = 64

func readRootMakefileData(ctx context.Context, repoPath string) ([]byte, bool) {
	if ctx == nil {
		return nil, false
	}
	for _, name := range []string{"Makefile", "makefile"} {
		if err := ctx.Err(); err != nil {
			return nil, false
		}
		if data, err := contextopt.ReadSnapshot(ctx, filepath.Join(repoPath, name)); err == nil {
			return data, true
		}
	}
	return nil, false
}

func readRootMakefile(ctx context.Context, repoPath string) map[string]*makeTargetDef {
	data, ok := readRootMakefileData(ctx, repoPath)
	if !ok {
		return nil
	}
	lines := joinMakefileLines(string(data))
	makeVars := collectMakeVariables(lines)
	targets := make(map[string]*makeTargetDef)
	var currentTargets []string

	for i := 0; i < len(lines) && i < maxLogicalLines; i++ {
		line := lines[i]
		if strings.HasPrefix(line, "\t") {
			recipe := stripMakeRecipePrefixes(line)
			recipe = expandMakeVariables(recipe, makeVars)
			for _, t := range currentTargets {
				targets[t].recipes = append(targets[t].recipes, recipe)
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			currentTargets = nil
			continue
		}
		currentTargets = parseTargetLine(trimmed, makeVars, targets)
	}
	return targets
}

func parseTargetLine(trimmed string, makeVars map[string]string, targets map[string]*makeTargetDef) []string {
	colon := strings.Index(trimmed, ":")
	if colon < 0 || isMakeAssignment(trimmed, colon) {
		return nil
	}
	head := strings.TrimSpace(trimmed[:colon])
	targetNames := strings.Fields(head)
	rest := trimmed[colon+1:]
	inlineRecipe := ""
	if semi := strings.Index(rest, ";"); semi >= 0 {
		inlineRecipe = strings.TrimSpace(rest[semi+1:])
		rest = rest[:semi]
	}
	if commentIdx := strings.Index(rest, "#"); commentIdx >= 0 {
		rest = rest[:commentIdx]
	}
	prereqs := strings.Fields(rest)
	var active []string
	for _, name := range targetNames {
		if strings.HasPrefix(name, ".") {
			continue
		}
		if targets[name] == nil {
			targets[name] = &makeTargetDef{}
		}
		targets[name].prerequisites = append(targets[name].prerequisites, prereqs...)
		if inlineRecipe != "" {
			rec := stripMakeRecipePrefixes(inlineRecipe)
			rec = expandMakeVariables(rec, makeVars)
			targets[name].recipes = append(targets[name].recipes, rec)
		}
		active = append(active, name)
	}
	return active
}

func recordMakeVariable(vars map[string]string, varName, op, val string) {
	_, exists := vars[varName]
	if op == "?=" {
		if !exists && len(vars) < maxMakeVariables {
			vars[varName] = val
		}
		return
	}
	if exists || len(vars) < maxMakeVariables {
		vars[varName] = val
	}
}

func parseMakeVariableLine(line string) (name, op, val string, ok bool) {
	if strings.HasPrefix(line, "\t") {
		return "", "", "", false
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", "", false
	}
	return parseMakeSimpleAssignment(trimmed)
}

func collectMakeVariables(lines []string) map[string]string {
	vars := make(map[string]string)
	for i := 0; i < len(lines) && i < maxLogicalLines && len(vars) < maxMakeVariables; i++ {
		varName, op, val, ok := parseMakeVariableLine(lines[i])
		if ok {
			recordMakeVariable(vars, varName, op, val)
		}
	}
	return vars
}

func parseMakeSimpleAssignment(line string) (name, op, val string, ok bool) {
	opIdx, opLen, chosenOp := findMakeAssignmentOp(line)
	if opIdx < 0 {
		return "", "", "", false
	}
	head := line[:opIdx]
	if strings.Contains(head, ":") {
		return "", "", "", false
	}
	head = util.StripMakeDirectivePrefixes(head)
	varName := strings.TrimSpace(head)
	if varName == "" || strings.ContainsAny(varName, " \t$(){}") {
		return "", "", "", false
	}
	rawVal := line[opIdx+opLen:]
	if commentIdx := strings.Index(rawVal, "#"); commentIdx >= 0 {
		rawVal = rawVal[:commentIdx]
	}
	return varName, chosenOp, strings.TrimSpace(rawVal), true
}

func findMakeAssignmentOp(line string) (int, int, string) {
	if idx := strings.Index(line, "::="); idx >= 0 {
		return idx, 3, "::="
	}
	if idx := strings.Index(line, ":="); idx >= 0 {
		return idx, 2, ":="
	}
	if idx := strings.Index(line, "?="); idx >= 0 {
		return idx, 2, "?="
	}
	if idx := strings.Index(line, "="); idx >= 0 {
		if idx > 0 && (line[idx-1] == '!' || line[idx-1] == '+') {
			return -1, 0, ""
		}
		return idx, 1, "="
	}
	return -1, 0, ""
}

func matchMakeVarRef(s string) (varName string, consumed int, ok bool) {
	if !strings.HasPrefix(s, "$(") && !strings.HasPrefix(s, "${") {
		return "", 0, false
	}
	closer := byte(')')
	if s[1] == '{' {
		closer = '}'
	}
	end := strings.IndexByte(s[2:], closer)
	if end < 0 {
		return "", 0, false
	}
	return s[2 : 2+end], 2 + end + 1, true
}

func expandMakeVariables(line string, vars map[string]string) string {
	if len(vars) == 0 || (!strings.Contains(line, "$(") && !strings.Contains(line, "${")) {
		return line
	}
	var b strings.Builder
	for i := 0; i < len(line); {
		if strings.HasPrefix(line[i:], "$$") {
			b.WriteString("$$")
			i += 2
			continue
		}
		if varName, consumed, ok := matchMakeVarRef(line[i:]); ok {
			if val, exists := vars[varName]; exists {
				b.WriteString(val)
			} else {
				b.WriteString(line[i : i+consumed])
			}
			i += consumed
			continue
		}
		b.WriteByte(line[i])
		i++
	}
	return b.String()
}

func stripMakeRecipePrefixes(s string) string {
	s = strings.TrimLeft(s, "\t ")
	for len(s) > 0 && (s[0] == '@' || s[0] == '-' || s[0] == '+') {
		s = strings.TrimLeft(s[1:], "\t ")
	}
	return s
}

func isMakeAssignment(line string, colon int) bool {
	rest := line[colon:]
	if strings.HasPrefix(rest, ":=") || strings.HasPrefix(rest, "::=") {
		return true
	}
	head := line[:colon]
	return strings.ContainsAny(head, "=+?!")
}

func joinMakefileLines(content string) []string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	rawLines := strings.Split(normalized, "\n")
	var lines []string
	var current strings.Builder
	for i := 0; i < len(rawLines) && i < maxLogicalLines; i++ {
		line := rawLines[i]
		if strings.HasSuffix(line, "\\") {
			current.WriteString(strings.TrimSuffix(line, "\\"))
			current.WriteByte(' ')
			continue
		}
		current.WriteString(line)
		lines = append(lines, current.String())
		current.Reset()
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return lines
}

func isSupportedMakeFile(f string) bool {
	switch f {
	case "Makefile", "./Makefile", "makefile", "./makefile":
		return true
	default:
		return false
	}
}

func isSupportedMakeDir(d string) bool {
	return d == "." || d == "./"
}

func consumeMakeOption(args []string, i int) (int, bool, bool) {
	arg := args[i]
	if arg == "-C" {
		if i+1 < len(args) {
			if !isSupportedMakeDir(args[i+1]) {
				return i, false, true
			}
			return i + 1, true, true
		}
		return i, true, true
	}
	if arg == "-f" || arg == "--file" {
		if i+1 < len(args) {
			if !isSupportedMakeFile(args[i+1]) {
				return i, false, true
			}
			return i + 1, true, true
		}
		return i, true, true
	}
	return i, true, false
}

func parseMakeTargets(args []string) []string {
	var targets []string
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		newI, valid, consumed := consumeMakeOption(args, i)
		if !valid {
			return nil
		}
		if consumed {
			i = newI
			continue
		}
		arg := args[i]
		if strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
			continue
		}
		targets = append(targets, arg)
	}
	return targets
}

type makeQueueItem struct {
	target string
	depth  int
}

func recipeRunsTool(recipe, toolLower string) bool {
	fields, err := scriptFields(recipe)
	if err != nil {
		return false
	}
	cmds := scriptCommands(fields)
	for _, c := range cmds {
		shCmd := parseShellCommand(c)
		if directCommandRunsTool(shCmd, toolLower, nil) {
			return true
		}
	}
	return false
}

func targetRecipesRunTool(recipes []string, toolLower string) bool {
	for _, recipe := range recipes {
		if recipeRunsTool(recipe, toolLower) {
			return true
		}
	}
	return false
}

func enqueuePrerequisites(queue []makeQueueItem, prereqs []string, depth int, visited map[string]bool) []makeQueueItem {
	if depth >= maxMakePrereqDepth {
		return queue
	}
	for _, prereq := range prereqs {
		if !visited[prereq] {
			queue = append(queue, makeQueueItem{target: prereq, depth: depth + 1})
		}
	}
	return queue
}

func initMakeQueue(targets []string) []makeQueueItem {
	queue := make([]makeQueueItem, 0, len(targets))
	for _, t := range targets {
		queue = append(queue, makeQueueItem{target: t, depth: 1})
	}
	return queue
}

func makeRunsTool(ctx context.Context, cmd shellCommand, toolLower string, targets map[string]*makeTargetDef) bool {
	initialTargets := parseMakeTargets(cmd.args)
	if len(initialTargets) == 0 {
		return false
	}
	queue := initMakeQueue(initialTargets)
	visited := make(map[string]bool, maxMakeTargets)
	totalVisited := 0

	for len(queue) > 0 && totalVisited < maxMakeTargets {
		if err := ctx.Err(); err != nil {
			return false
		}
		item := queue[0]
		queue = queue[1:]
		if visited[item.target] {
			continue
		}
		visited[item.target] = true
		totalVisited++

		def := targets[item.target]
		if def == nil {
			continue
		}
		if targetRecipesRunTool(def.recipes, toolLower) {
			return true
		}
		queue = enqueuePrerequisites(queue, def.prerequisites, item.depth, visited)
	}
	return false
}

type golangciLintersConfig struct {
	Linters struct {
		Default    string   `yaml:"default"`
		Enable     []string `yaml:"enable"`
		Disable    []string `yaml:"disable"`
		EnableAll  bool     `yaml:"enable-all"`
		DisableAll bool     `yaml:"disable-all"`
	} `yaml:"linters"`
}

func initialGolangciLinters(cfg golangciLintersConfig) map[string]bool {
	enabled := make(map[string]bool)
	switch {
	case cfg.Linters.Default == "all" || cfg.Linters.EnableAll:
		for _, l := range AllGolangciLinters {
			enabled[l] = true
		}
	case cfg.Linters.Default == "standard" || (cfg.Linters.Default == "" && !cfg.Linters.DisableAll):
		for _, l := range standardGolangciLinters {
			enabled[l] = true
		}
	}
	return enabled
}

func applyGolangciOverrides(enabled map[string]bool, enable, disable []string) {
	for _, l := range enable {
		enabled[strings.ToLower(l)] = true
	}
	for _, l := range disable {
		delete(enabled, strings.ToLower(l))
	}
}

func readGolangciData(ctx context.Context, repoPath string) ([]byte, bool) {
	if ctx == nil {
		return nil, false
	}
	for _, name := range []string{".golangci.yml", ".golangci.yaml"} {
		if err := ctx.Err(); err != nil {
			return nil, false
		}
		if data, err := contextopt.ReadSnapshot(ctx, filepath.Join(repoPath, name)); err == nil {
			return data, true
		}
	}
	return nil, false
}

func readGolangciConfig(ctx context.Context, repoPath string) (map[string]bool, bool) {
	data, ok := readGolangciData(ctx, repoPath)
	if !ok {
		return nil, false
	}
	var cfg golangciLintersConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, false
	}
	enabled := initialGolangciLinters(cfg)
	applyGolangciOverrides(enabled, cfg.Linters.Enable, cfg.Linters.Disable)
	return enabled, true
}

var standardGolangciLinters = []string{
	"errcheck", "gosimple", "govet", "ineffassign", "staticcheck", "unused",
}

// AllGolangciLinters lists known linters that golangci-lint can run.
var AllGolangciLinters = []string{
	"asasalint", "asciicheck", "bidichk", "bodyclose", "canonicalheader",
	"containedctx", "contextcheck", "copyloopvar", "cyclop", "decorder",
	"depguard", "dogsled", "dupl", "dupword", "durationcheck",
	"err113", "errcheck", "errchkjson", "errname", "errorlint",
	"execinquery", "exhaustive", "exhaustivestruct", "exhaustruct", "exportloopref",
	"fatcontext", "forbidigo", "forcetypeassert", "funlen", "gci",
	"ginkgolinter", "gocheckcompilerdirectives", "gochecknoglobals", "gochecknoinits", "gochecksumtype",
	"gocognit", "goconst", "gocritic", "gocyclo", "godot",
	"godox", "goerr113", "gofmt", "gofumpt", "goheader",
	"goimports", "golint", "gomnd", "gomoddirectives", "gomodguard",
	"goprintffuncname", "gosec", "gosimple", "gosmopolitan", "govet",
	"grouper", "ifshort", "importas", "inamedparam", "ineffassign",
	"interfacebloat", "interfacer", "intrange", "ireturn", "lll",
	"loggercheck", "maintidx", "makezero", "maligned", "mirror",
	"misspell", "modernize", "musttag", "nakedret", "nestif",
	"nilerr", "nilnil", "nlreturn", "noctx", "nolintlint",
	"nonamedreturns", "nosnakecase", "nosprintfhostport", "paralleltest", "perfsprint",
	"prealloc", "predeclared", "promlinter", "protogetter", "reassign",
	"revive", "rowserrcheck", "scopelint", "sloglint", "spancheck",
	"sqlclosecheck", "staticcheck", "structcheck", "stylecheck", "tagalign",
	"tagliatelle", "tenv", "testableexamples", "testifylint", "testpackage",
	"thelper", "tparallel", "unconvert", "unparam", "unused",
	"usestdlibvars", "varcheck", "varnamelen", "wastedassign", "whitespace",
	"wrapcheck", "wsl", "zerologlint",
}
