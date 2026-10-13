// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxPackageJSONBytes bounds the package.json read by workflow linter measurement (HISS-02).
const maxPackageJSONBytes = 1 << 20

// maxScriptChainDepth bounds the depth when following chained package.json scripts (HISS-02).
const maxScriptChainDepth = 8

// maxScriptsVisited bounds the total scripts visited during one expansion (HISS-02).
const maxScriptsVisited = 64

type packageJSONFile struct {
	Scripts map[string]string `json:"scripts"`
}

func readPackageScripts(ctx context.Context, repoPath, workingDir string) map[string]string {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return nil
	}
	rel := "package.json"
	cleanDir := filepath.Clean(filepath.FromSlash(workingDir))
	if cleanDir != "." && cleanDir != "" {
		rel = filepath.Join(cleanDir, "package.json")
	}
	data, err := util.ReadConfinedLimited(repoPath, rel, maxPackageJSONBytes)
	if err != nil {
		return nil
	}
	var pkg packageJSONFile
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	return pkg.Scripts
}

type packageScriptsCache struct {
	scripts map[string]map[string]string
}

func newPackageScriptsCache() *packageScriptsCache {
	return &packageScriptsCache{
		scripts: make(map[string]map[string]string),
	}
}

func (c *packageScriptsCache) get(ctx context.Context, repoPath, workingDir string) map[string]string {
	if c == nil {
		return readPackageScripts(ctx, repoPath, workingDir)
	}
	cleanDir := filepath.Clean(filepath.FromSlash(workingDir))
	if cleanDir == "." {
		cleanDir = ""
	}
	if s, ok := c.scripts[cleanDir]; ok {
		return s
	}
	s := readPackageScripts(ctx, repoPath, cleanDir)
	c.scripts[cleanDir] = s
	return s
}

func isYarnBuiltin(name string) bool {
	switch strings.ToLower(name) {
	case "access", "add", "audit", "autoclean", "bin", "cache", "check",
		"config", "create", "dedupe", "dlx", "exec", "explain",
		"generate-lock-entry", "global", "help", "import", "info", "init",
		"install", "licenses", "link", "list", "lockfile", "login",
		"logout", "node", "outdated", "owner", "pack", "patch",
		"patch-commit", "policies", "publish", "rebuild", "remove", "run",
		"self-update", "set", "stage", "tag", "team", "test", "unlink",
		"unplug", "up", "upgrade", "upgrade-interactive", "version",
		"versions", "why", "workspace", "workspaces":
		return true
	default:
		return false
	}
}

func checkNpmRunner(args []string, toolLower string) bool {
	rest := argsAfterWord(args, "exec")
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

func checkYarnRunner(args []string, toolLower string, pkgScripts map[string]string) bool {
	if rest := argsAfterWord(args, "exec"); len(rest) > 0 {
		t := firstNonFlagArg(rest)
		if t == "" {
			return false
		}
		clean := cleanRunnerSpec(t)
		base := programName(clean)
		return base == toolLower || matchesDashWord(base, toolLower)
	}
	t := firstNonFlagArg(args)
	if t == "" || isYarnBuiltin(t) {
		return false
	}
	if pkgScripts != nil && pkgScripts[t] != "" {
		return false
	}
	clean := cleanRunnerSpec(t)
	base := programName(clean)
	return base == toolLower || matchesDashWord(base, toolLower)
}

func checkBunRunner(args []string, toolLower string) bool {
	rest := argsAfterWord(args, "x")
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

func parsePackageScriptInvocation(prog string, args []string) (string, bool) {
	switch prog {
	case "npm":
		return parseNpmScriptInvocation(args)
	case "pnpm":
		return parsePnpmScriptInvocation(args)
	case "yarn":
		return parseYarnScriptInvocation(args)
	case "bun":
		return parseBunScriptInvocation(args)
	default:
		return "", false
	}
}

func parseNpmScriptInvocation(args []string) (string, bool) {
	if rest := argsAfterWord(args, "run"); len(rest) > 0 {
		return scriptFromRest(rest)
	}
	if rest := argsAfterWord(args, "run-script"); len(rest) > 0 {
		return scriptFromRest(rest)
	}
	if firstNonFlagArg(args) == "test" {
		return "test", true
	}
	return "", false
}

func parsePnpmScriptInvocation(args []string) (string, bool) {
	if rest := argsAfterWord(args, "run"); len(rest) > 0 {
		return scriptFromRest(rest)
	}
	first := firstNonFlagArg(args)
	if first != "" && first != "dlx" && first != "exec" {
		return first, true
	}
	return "", false
}

func parseYarnScriptInvocation(args []string) (string, bool) {
	if rest := argsAfterWord(args, "run"); len(rest) > 0 {
		return scriptFromRest(rest)
	}
	first := firstNonFlagArg(args)
	if first != "" && first != "exec" {
		return first, true
	}
	return "", false
}

func parseBunScriptInvocation(args []string) (string, bool) {
	rest := argsAfterWord(args, "run")
	if len(rest) == 0 {
		return "", false
	}
	return scriptFromRest(rest)
}

func scriptFromRest(rest []string) (string, bool) {
	s := firstNonFlagArg(rest)
	if s == "" {
		return "", false
	}
	return s, true
}

type scriptQueueItem struct {
	name  string
	depth int
}

func packageScriptRunsTool(ctx context.Context, cmd shellCommand, toolLower string, pkgScripts map[string]string, makeTargets map[string]*makeTargetDef) bool {
	initialScript, ok := parsePackageScriptInvocation(cmd.program, cmd.args)
	if !ok || pkgScripts[initialScript] == "" {
		return false
	}
	queue := []scriptQueueItem{{name: initialScript, depth: 1}}
	visited := make(map[string]bool, maxScriptsVisited)
	totalVisited := 0

	for len(queue) > 0 && totalVisited < maxScriptsVisited {
		if err := ctx.Err(); err != nil {
			return false
		}
		item := queue[0]
		queue = queue[1:]
		if visited[item.name] {
			continue
		}
		visited[item.name] = true
		totalVisited++

		content, exists := pkgScripts[item.name]
		if !exists || strings.TrimSpace(content) == "" {
			continue
		}
		if scriptContentRunsTool(ctx, content, toolLower, makeTargets, pkgScripts) {
			return true
		}
		queue = enqueueChainedScripts(queue, content, item.depth, visited)
	}
	return false
}

func scriptContentRunsTool(ctx context.Context, scriptContent, toolLower string, makeTargets map[string]*makeTargetDef, pkgScripts map[string]string) bool {
	fields, err := scriptFields(scriptContent)
	if err != nil {
		return false
	}
	cmds := scriptCommands(fields)
	for i := 0; i < len(cmds) && i < maxRunScriptFields; i++ {
		shCmd := parseShellCommand(cmds[i])
		if directCommandRunsTool(shCmd, toolLower, pkgScripts) {
			return true
		}
		if shCmd.program == "make" && makeTargets != nil && makeRunsTool(ctx, shCmd, toolLower, makeTargets) {
			return true
		}
	}
	return false
}

func enqueueChainedScripts(queue []scriptQueueItem, scriptContent string, depth int, visited map[string]bool) []scriptQueueItem {
	if depth >= maxScriptChainDepth {
		return queue
	}
	fields, err := scriptFields(scriptContent)
	if err != nil {
		return queue
	}
	cmds := scriptCommands(fields)
	for i := 0; i < len(cmds) && i < maxRunScriptFields; i++ {
		shCmd := parseShellCommand(cmds[i])
		next, ok := parsePackageScriptInvocation(shCmd.program, shCmd.args)
		if ok && !visited[next] {
			queue = append(queue, scriptQueueItem{name: next, depth: depth + 1})
		}
	}
	return queue
}
