// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// PersonaToolClasses is the closed vocabulary accepted by canonical persona front matter.
const PersonaToolClasses = "read, search, edit, shell, web, task"

type personaToolClass struct {
	name    string
	clients map[string][]string
}

// personaToolMappings is the one engine-class to native-tool mapping. Checked against Claude
// Code 2.1.302, GitHub Copilot custom-agent docs (2026-10-17), Gemini CLI subagents docs
// (main checked 2026-10-17), Codex CLI 0.162.1 docs, and OpenCode 1.18.7 docs. Claude's
// subagent `tools` allow-list: https://code.claude.com/docs/en/sub-agents. Copilot's `tools`
// aliases: https://docs.github.com/en/copilot/reference/custom-agents-configuration. Gemini's
// list: https://github.com/google-gemini/gemini-cli/blob/main/docs/core/subagents.md. Codex
// custom agents inherit parent tools and cannot disable edit; https://learn.chatgpt.com/docs/agent-configuration/subagents.
// OpenCode permission keys: https://opencode.ai/docs/agents/.
var personaToolMappings = []personaToolClass{
	{name: "read", clients: map[string][]string{
		".claude/agents": {"Read"}, ".github/agents": {"read"},
		".gemini/agents":   {"read_file", "read_many_files"},
		".opencode/agents": {"read"},
		".codex/agents":    nil,
	}},
	{name: "search", clients: map[string][]string{
		".claude/agents": {"Grep", "Glob"}, ".github/agents": {"search"},
		".gemini/agents":   {"grep_search", "glob", "list_directory"},
		".opencode/agents": {"grep", "glob", "list"},
		".codex/agents":    nil,
	}},
	{name: "edit", clients: map[string][]string{
		".claude/agents": {"Edit", "Write", "NotebookEdit"},
		".github/agents": {"edit"}, ".gemini/agents": {"write_file", "replace"},
		".opencode/agents": {"edit"},
		".codex/agents":    nil,
	}},
	{name: "shell", clients: map[string][]string{
		".claude/agents": {"Bash"}, ".github/agents": {"execute"},
		".gemini/agents": {"run_shell_command"}, ".opencode/agents": {"bash"},
		".codex/agents": nil,
	}},
	{name: "web", clients: map[string][]string{
		".claude/agents": {"WebFetch", "WebSearch"}, ".github/agents": {"web"},
		".gemini/agents":   {"web_fetch", "google_web_search"},
		".opencode/agents": {"webfetch", "websearch"},
		".codex/agents":    nil,
	}},
	{name: "task", clients: map[string][]string{
		".claude/agents": {"Agent", "TaskCreate", "TaskGet", "TaskList", "TaskUpdate", "TaskStop"},
		".github/agents": {"agent", "todo"}, ".gemini/agents": {"invoke_agent"},
		".opencode/agents": {"task"},
		".codex/agents":    nil,
	}},
}

// projectPersonaTools replaces canonical engine classes with the selected client's native list.
func projectPersonaTools(targetDir, rel string, data []byte) ([]byte, error) {
	classes, present, err := canonicalPersonaToolClasses(rel, data)
	if err != nil || !present {
		return data, err
	}
	if targetDir == ".codex/agents" {
		return removePersonaToolsWithGap(data)
	}
	if targetDir == ".opencode/agents" {
		return rewritePersonaPermissions(data, classes)
	}
	tools, ok := clientPersonaTools(targetDir, classes)
	if !ok {
		return nil, fmt.Errorf("%s: client %s has no verified per-agent tool control", rel, targetDir)
	}
	return rewritePersonaTools(data, tools)
}

func canonicalPersonaToolClasses(rel string, data []byte) ([]string, bool, error) {
	block, opened, err := frontMatterBlock(data)
	if err != nil || !opened {
		return nil, false, err
	}
	var front map[string]any
	if err := util.DecodeYAMLDocument(block, &front, util.YAMLDocumentOptions{AllowEmpty: true}); err != nil {
		return nil, false, fmt.Errorf("%s: decode tools front matter: %w", rel, err)
	}
	value, present := front["tools"]
	if !present {
		return nil, false, nil
	}
	classes, err := decodePersonaToolClasses(rel, value)
	return classes, true, err
}

func decodePersonaToolClasses(rel string, value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 || len(items) > len(personaToolMappings) {
		return nil, fmt.Errorf("%s: tools must be a non-empty list from: %s", rel, PersonaToolClasses)
	}
	classes := make([]string, 0, len(items))
	for _, item := range items {
		name, ok := item.(string)
		if !ok || !knownPersonaToolClass(name) || slices.Contains(classes, name) {
			return nil, fmt.Errorf("%s: invalid or duplicate tool class %q; allowed classes: %s", rel, item, PersonaToolClasses)
		}
		classes = append(classes, name)
	}
	return classes, nil
}

func knownPersonaToolClass(name string) bool {
	for _, class := range personaToolMappings {
		if name == class.name {
			return true
		}
	}
	return false
}

func clientPersonaTools(targetDir string, classes []string) ([]string, bool) {
	var tools []string
	for _, class := range personaToolMappings {
		if !slices.Contains(classes, class.name) {
			continue
		}
		mapped, ok := class.clients[targetDir]
		if !ok {
			return nil, false
		}
		tools = append(tools, mapped...)
	}
	return tools, true
}

func rewritePersonaTools(data []byte, tools []string) ([]byte, error) {
	return rewritePersonaFrontMatter(data, func(front map[string]any) { front["tools"] = tools })
}

func rewritePersonaFrontMatter(data []byte, change func(map[string]any)) ([]byte, error) {
	crlf := bytes.Contains(data, []byte("\r\n"))
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	block, opened, err := frontMatterBlock(normalized)
	if err != nil || !opened {
		return nil, err
	}
	var front map[string]any
	if err := util.DecodeYAMLDocument(block, &front, util.YAMLDocumentOptions{AllowEmpty: true}); err != nil {
		return nil, fmt.Errorf("decode persona front matter: %w", err)
	}
	change(front)
	encoded, err := util.EncodeYAMLDocument(front)
	if err != nil {
		return nil, fmt.Errorf("encode projected persona tools: %w", err)
	}
	_, body, found := strings.Cut(string(normalized), "\n---\n")
	if !found {
		return nil, fmt.Errorf("projected persona front matter has no closing delimiter")
	}
	projected := append(encoded, []byte("---\n"+body)...)
	if crlf {
		projected = bytes.ReplaceAll(projected, []byte("\n"), []byte("\r\n"))
	}
	return projected, nil
}

func removePersonaToolsWithGap(data []byte) ([]byte, error) {
	clean, err := rewritePersonaFrontMatter(data, func(front map[string]any) { delete(front, "tools") })
	if err != nil {
		return nil, err
	}
	gap := "\n<!-- TOOL PERMISSION GAP: Codex custom agents inherit parent tools; no per-agent allow-list is available. -->\n"
	if bytes.Contains(data, []byte("\r\n")) {
		gap = strings.ReplaceAll(gap, "\n", "\r\n")
	}
	return append(clean, []byte(gap)...), nil
}

func rewritePersonaPermissions(data []byte, classes []string) ([]byte, error) {
	permissions := map[string]string{"*": "deny"}
	for _, class := range classes {
		for _, key := range findPersonaToolClass(class).clients[".opencode/agents"] {
			permissions[key] = "allow"
		}
	}
	return rewritePersonaFrontMatter(data, func(front map[string]any) {
		delete(front, "tools")
		front["permission"] = permissions
	})
}

func findPersonaToolClass(name string) personaToolClass {
	for _, class := range personaToolMappings {
		if class.name == name {
			return class
		}
	}
	return personaToolClass{}
}

// PersonaToolPermissionGaps reports selected persona targets that cannot enforce declared tools.
func PersonaToolPermissionGaps(ctx context.Context, rootDir string) ([]string, error) {
	dirs, err := agentProjectionDirs(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(dirs, ".codex/agents") {
		return nil, nil
	}
	names, err := listCanonicalAgents(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	var gaps []string
	for i := 0; i < len(names) && i < maxAgentProjections; i++ {
		data, err := readCanonicalAgent(ctx, rootDir, names[i])
		if err != nil {
			return nil, err
		}
		if _, present, err := canonicalPersonaToolClasses(CanonicalAgentsRel+"/"+names[i], data); err != nil {
			return nil, err
		} else if present {
			gaps = append(gaps, fmt.Sprintf("Codex cannot enforce per-agent tool classes for %s; it inherits parent permissions", names[i]))
		}
	}
	return gaps, nil
}
