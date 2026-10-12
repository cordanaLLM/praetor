// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"gopkg.in/yaml.v3"
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

	makeTargets := readRootMakefile(repoPath)
	golangciEnabled, hasGolangci := readGolangciConfig(repoPath)
	present := make(map[string]bool, len(candidates))
	golangciRuns := false

	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		spec, err := ghworkflow.Parse(files[i].Data)
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", files[i].Name, err)
		}
		scanWorkflowSteps(ctx, spec, candidates, makeTargets, present, &golangciRuns)
	}

	if golangciRuns && hasGolangci {
		for c := 0; c < len(candidates) && c < maxLinterCandidates; c++ {
			tool := candidates[c]
			if !present[tool] && golangciEnabled[strings.ToLower(tool)] {
				present[tool] = true
			}
		}
	}

	return present, nil
}

func scanWorkflowSteps(ctx context.Context, spec ghworkflow.Spec, candidates []string, makeTargets map[string]*makeTargetDef, present map[string]bool, golangciRuns *bool) {
	for _, job := range spec.Jobs {
		if ghworkflow.NeverRuns(job.If) || advisoryJob(job.ContinueOnError) {
			continue
		}
		for s := 0; s < len(job.Steps) && s < ghworkflow.MaxStepsPerJob; s++ {
			step := job.Steps[s]
			if ghworkflow.NeverRuns(step.If) || advisoryJob(step.ContinueOnError) {
				continue
			}
			if !*golangciRuns && stepRunsTool(ctx, step, "golangci-lint", makeTargets) {
				*golangciRuns = true
			}
			for c := 0; c < len(candidates) && c < maxLinterCandidates; c++ {
				tool := candidates[c]
				if !present[tool] && stepRunsTool(ctx, step, tool, makeTargets) {
					present[tool] = true
				}
			}
		}
	}
}

// stepRunsTool reports whether step's run: or uses: names the tool or runs it via a runner form or makefile target.
func stepRunsTool(ctx context.Context, step ghworkflow.Step, tool string, makeTargets map[string]*makeTargetDef) bool {
	if step.Uses != "" && stepActionRunsTool(step.Uses, tool) {
		return true
	}
	if step.Run != "" && scriptRunsTool(ctx, step.Run, tool, makeTargets) {
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

func scriptRunsTool(ctx context.Context, run, tool string, makeTargets map[string]*makeTargetDef) bool {
	fields, err := scriptFields(run)
	if err != nil {
		return false
	}
	commands := scriptCommands(fields)
	toolLower := strings.ToLower(tool)
	for i := 0; i < len(commands) && i < maxRunScriptFields; i++ {
		cmd := parseShellCommand(commands[i])
		if commandRunsTool(ctx, cmd, toolLower, makeTargets) {
			return true
		}
	}
	return false
}

func commandRunsTool(ctx context.Context, cmd shellCommand, toolLower string, makeTargets map[string]*makeTargetDef) bool {
	if cmd.program == toolLower {
		return true
	}
	if checkRunnerTool(cmd, toolLower) {
		return true
	}
	if cmd.program == "make" && makeTargets != nil {
		return makeRunsTool(ctx, cmd, toolLower, makeTargets)
	}
	return false
}

func checkRunnerTool(cmd shellCommand, toolLower string) bool {
	if toolLower == "govet" && cmd.program == "go" && len(cmd.args) > 0 && cmd.args[0] == "vet" {
		return true
	}
	if toolLower == "clippy" && cmd.program == "cargo" && len(cmd.args) > 0 {
		if cmd.args[0] == "clippy" || (strings.HasPrefix(cmd.args[0], "+") && len(cmd.args) > 1 && cmd.args[1] == "clippy") {
			return true
		}
	}
	if cmd.program == "go" && len(cmd.args) > 0 && cmd.args[0] == "run" {
		pkg := firstNonFlagArg(cmd.args[1:])
		pkg, _, _ = strings.Cut(pkg, "@")
		if programName(pkg) == toolLower || matchesDashWord(programName(pkg), toolLower) {
			return true
		}
	}
	switch cmd.program {
	case "npx", "bunx", "uvx":
		return checkJsRunner(cmd.args, toolLower)
	case "pipx":
		return checkSubcommandRunner(cmd.args, "run", toolLower)
	case "pnpm":
		return checkSubcommandRunner(cmd.args, "dlx", toolLower)
	case "docker", "podman":
		return checkContainerRun(cmd.args, toolLower)
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

func checkJsRunner(args []string, toolLower string) bool {
	t := firstRunnerTarget(args)
	if t == "" {
		return false
	}
	t, _, _ = strings.Cut(t, "@")
	base := programName(t)
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
	case "-p", "--package", "-c", "--from", "--with", "--python", "--spec", "--index-url", "--pip-args":
		return true
	}
	return false
}

func checkSubcommandRunner(args []string, subcmd, toolLower string) bool {
	rest := argsAfterWord(args, subcmd)
	if len(rest) == 0 {
		return false
	}
	t := firstNonFlagArg(rest)
	if t == "" {
		return false
	}
	t, _, _ = strings.Cut(t, "@")
	base := programName(t)
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
	img, _, _ := strings.Cut(image, "@")
	lastSlash := strings.LastIndex(img, "/")
	lastSegment := img[lastSlash+1:]
	repoBase, _, _ := strings.Cut(lastSegment, ":")
	return strings.ToLower(repoBase) == toolLower
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

type makeTargetDef struct {
	prerequisites []string
	recipes       []string
}

func readRootMakefile(repoPath string) map[string]*makeTargetDef {
	data, err := os.ReadFile(filepath.Join(repoPath, "Makefile"))
	if err != nil {
		data, err = os.ReadFile(filepath.Join(repoPath, "makefile"))
		if err != nil {
			return nil
		}
	}
	lines := joinMakefileLines(string(data))
	targets := make(map[string]*makeTargetDef)
	var currentTargets []string

	for i := 0; i < len(lines) && i < maxLogicalLines; i++ {
		line := lines[i]
		if strings.HasPrefix(line, "\t") {
			recipe := stripMakePrefixes(line)
			for _, t := range currentTargets {
				targets[t].recipes = append(targets[t].recipes, recipe)
			}
			continue
		}
		currentTargets = nil
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		colon := strings.Index(trimmed, ":")
		if colon < 0 || isMakeAssignment(trimmed, colon) {
			continue
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
		for _, name := range targetNames {
			if strings.HasPrefix(name, ".") {
				continue
			}
			if targets[name] == nil {
				targets[name] = &makeTargetDef{}
			}
			targets[name].prerequisites = append(targets[name].prerequisites, prereqs...)
			if inlineRecipe != "" {
				targets[name].recipes = append(targets[name].recipes, stripMakePrefixes(inlineRecipe))
			}
			currentTargets = append(currentTargets, name)
		}
	}
	return targets
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

func stripMakePrefixes(s string) string {
	s = strings.TrimLeft(s, "\t ")
	for len(s) > 0 && (s[0] == '@' || s[0] == '-' || s[0] == '+') {
		s = strings.TrimLeft(s[1:], "\t ")
	}
	return s
}

func parseMakeTargets(args []string) []string {
	var targets []string
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		arg := args[i]
		if arg == "-C" {
			if i+1 < len(args) {
				dir := args[i+1]
				if dir != "." && dir != "./" {
					return nil
				}
				i++
			}
			continue
		}
		if arg == "-f" || arg == "--file" {
			if i+1 < len(args) {
				f := args[i+1]
				if f != "Makefile" && f != "./Makefile" && f != "makefile" && f != "./makefile" {
					return nil
				}
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
			continue
		}
		targets = append(targets, arg)
	}
	return targets
}

func makeRunsTool(ctx context.Context, cmd shellCommand, toolLower string, targets map[string]*makeTargetDef) bool {
	initialTargets := parseMakeTargets(cmd.args)
	if len(initialTargets) == 0 {
		return false
	}
	type queueItem struct {
		target string
		depth  int
	}
	queue := make([]queueItem, 0, len(initialTargets))
	for _, t := range initialTargets {
		queue = append(queue, queueItem{target: t, depth: 1})
	}
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

		def, ok := targets[item.target]
		if !ok || def == nil {
			continue
		}
		for _, recipe := range def.recipes {
			fields, err := scriptFields(recipe)
			if err != nil {
				continue
			}
			cmds := scriptCommands(fields)
			for _, c := range cmds {
				shCmd := parseShellCommand(c)
				if shCmd.program == toolLower || checkRunnerTool(shCmd, toolLower) {
					return true
				}
			}
		}
		if item.depth < maxMakePrereqDepth {
			for _, prereq := range def.prerequisites {
				if !visited[prereq] {
					queue = append(queue, queueItem{target: prereq, depth: item.depth + 1})
				}
			}
		}
	}
	return false
}

func readGolangciConfig(repoPath string) (map[string]bool, bool) {
	p := filepath.Join(repoPath, ".golangci.yml")
	data, err := os.ReadFile(p)
	if err != nil {
		p = filepath.Join(repoPath, ".golangci.yaml")
		data, err = os.ReadFile(p)
		if err != nil {
			return nil, false
		}
	}
	var cfg struct {
		Linters struct {
			Default    string   `yaml:"default"`
			Enable     []string `yaml:"enable"`
			Disable    []string `yaml:"disable"`
			EnableAll  bool     `yaml:"enable-all"`
			DisableAll bool     `yaml:"disable-all"`
		} `yaml:"linters"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, false
	}
	enabled := make(map[string]bool)
	switch {
	case cfg.Linters.Default == "all" || cfg.Linters.EnableAll:
		for _, l := range allGolangciLinters {
			enabled[l] = true
		}
	case cfg.Linters.Default == "standard" || (cfg.Linters.Default == "" && !cfg.Linters.DisableAll):
		for _, l := range standardGolangciLinters {
			enabled[l] = true
		}
	}
	for _, l := range cfg.Linters.Enable {
		enabled[strings.ToLower(l)] = true
	}
	for _, l := range cfg.Linters.Disable {
		delete(enabled, strings.ToLower(l))
	}
	return enabled, true
}

var standardGolangciLinters = []string{
	"errcheck", "gosimple", "govet", "ineffassign", "staticcheck", "unused",
}

var allGolangciLinters = []string{
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
