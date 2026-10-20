// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Only literal shell words are evidence; variables and expansions are never evaluated.
var toolWords = regexp.MustCompile(`'[^']*'|"[^"\n]*"|[^\s;&|<>()'" ]+|[;&|\n()]`)

type transcriptTool struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type transcriptToolInput struct {
	Command      string `json:"command"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Path         string `json:"path"`
}

func literalPath(path, cwd string) string {
	if path == "" || strings.HasPrefix(path, "~") || strings.ContainsAny(path, "$`*?{}") {
		return ""
	}
	if strings.Contains(path, "\\") && filepath.VolumeName(path) == "" {
		return ""
	}
	if !absoluteTranscriptPath(path) {
		if !absoluteTranscriptPath(cwd) {
			return ""
		}
		path = filepath.Join(cwd, path)
	}
	return canonicalTranscriptPath(path)
}

func canonicalTranscriptPath(path string) string {
	// Recorded creations and Edit/Write targets may no longer exist. Resolve their
	// existing ancestors too, so aliases still join the live worktree root.
	if resolved, err := util.ResolveExistingAncestor(path); err == nil {
		path = resolved
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func (j *transcriptJoin) collectTools(rec *harvester.LedgerRecord, response *transcriptResponse) error {
	content := rec.Message.Content
	if len(content) == 0 || content[0] != '[' {
		return nil
	}
	var blocks []transcriptTool
	if err := json.Unmarshal(content, &blocks); err != nil {
		return fmt.Errorf("decode tool blocks: %w", err)
	}
	for _, block := range blocks {
		if block.Type != "tool_use" || !supportedTranscriptTool(block.Name) {
			continue
		}
		// Replayed blocks consume the byte budget once, like their response usage.
		key := block.ID + ":" + string(block.Input)
		if response.tools[key] {
			continue
		}
		if len(block.Input) > MaxResponseToolBytes-response.bytes {
			return fmt.Errorf("response tool inputs exceed %d byte bound", MaxResponseToolBytes)
		}
		response.bytes += len(block.Input)
		response.tools[key] = true
		var input transcriptToolInput
		if err := json.Unmarshal(block.Input, &input); err != nil {
			return fmt.Errorf("decode %s input: %w", block.Name, err)
		}
		if err := j.toolPaths(block.Name, input, rec.CWD, response.paths); err != nil {
			return err
		}
	}
	return nil
}

func (j *transcriptJoin) toolPaths(name string, input transcriptToolInput, cwd string, paths map[string]bool) error {
	var path string
	switch name {
	case "Bash":
		return j.bashPaths(input.Command, cwd, paths)
	case "Read", "Edit", "Write", "NotebookEdit":
		path = input.FilePath
		if path == "" {
			path = input.NotebookPath
		}
	case "EnterWorktree":
		path = input.Path
	}
	if path = literalPath(path, cwd); path != "" {
		paths[path] = true
	}
	return nil
}

func addToolPath(paths map[string]bool, path, cwd string) string {
	path = literalPath(strings.Trim(path, "\"'"), cwd)
	if path != "" {
		paths[path] = true
	}
	return path
}

func (j *transcriptJoin) bashPaths(command, cwd string, paths map[string]bool) error {
	words := toolWords.FindAllString(command, MaxResponseToolBytes)
	for i, raw := range words {
		collectAbsoluteToolPaths(raw, cwd, paths)
		cwd = shellDirectory(words, i, cwd, paths)
		if raw == "git" && shellCommandStart(words, i) {
			if err := j.creation(words[i+1:], cwd); err != nil {
				return err
			}
		}
	}
	return nil
}

func shellCommandStart(words []string, i int) bool {
	if i == 0 {
		return true
	}
	switch words[i-1] {
	case ";", "&", "|", "\n", "(", ")":
		return true
	default:
		return false
	}
}

func shellDirectory(words []string, i int, cwd string, paths map[string]bool) string {
	if i+1 >= len(words) {
		return cwd
	}
	switch words[i] {
	case "cd":
		if !shellCommandStart(words, i) {
			return cwd
		}
		if path := addToolPath(paths, words[i+1], cwd); path != "" {
			return path
		}
	case "-C":
		if i > 0 && words[i-1] == "git" {
			addToolPath(paths, words[i+1], cwd)
		}
	}
	return cwd
}

func creationArgs(words []string) (root, branch string) {
	if len(words) < 3 || words[0] != "worktree" || words[1] != "add" {
		return "", ""
	}
	words = words[2:]
	words, branch, detached := creationOptions(words)
	if len(words) == 0 || strings.ContainsAny(words[0], ";&|\n()") {
		return "", ""
	}
	root = strings.Trim(words[0], "\"'")
	if detached {
		return root, ""
	}
	if branch == "" && len(words) > 1 && !strings.ContainsAny(words[1], ";&|\n()") {
		branch = strings.Trim(words[1], "\"'")
	}
	return root, branch
}

// Only allow-listed options establish a creation; unknown flags never become paths.
func creationOptions(words []string) (remaining []string, branch string, detached bool) {
	for i := 0; i < len(words); i++ {
		word := strings.Trim(words[i], "\"'")
		if word == "--" {
			return words[i+1:], branch, detached
		}
		if !strings.HasPrefix(word, "-") {
			return words[i:], branch, detached
		}
		switch word {
		case "-b", "-B", "--orphan":
			if i+1 >= len(words) {
				return nil, "", false
			}
			i++
			branch = strings.Trim(words[i], "\"'")
			if strings.HasPrefix(branch, "-") || !literalBranch(branch) {
				return nil, "", false
			}
		case "--detach", "-d":
			detached = true
		case "-q", "--quiet", "-f", "--force", "--lock", "--no-checkout", "--checkout", "--track", "--no-track", "--guess-remote":
			// Value-less options; the path follows the last option.
		default:
			return nil, "", false
		}
	}
	return nil, "", false
}

func (j *transcriptJoin) creation(words []string, cwd string) error {
	if len(words) >= 2 && words[0] == "-C" {
		cwd = literalPath(strings.Trim(words[1], "\"'"), cwd)
		words = words[2:]
	}
	path, branch := creationArgs(words)
	root := literalPath(path, cwd)
	if root == "" {
		return nil
	}
	// A commit hash or expression does not prove a branch name.
	if !literalBranch(branch) {
		branch = ""
	}
	return j.recordRoot(root, branch)
}

var commitHash = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

func literalBranch(branch string) bool {
	return branch != "" && branch != "HEAD" && !strings.ContainsAny(branch, "$`~^:{}*? \\;") && !commitHash.MatchString(branch)
}

func (j *transcriptJoin) recordRoot(root, branch string) error {
	root = canonicalTranscriptPath(root)
	if j.live[root] || j.conflicts[root] {
		return nil
	}
	previous, exists := j.roots[root]
	if !exists {
		if len(j.roots) >= maxTranscriptRoots {
			return fmt.Errorf("worktree roots exceed %d bound", maxTranscriptRoots)
		}
		j.recorded++
		j.roots[root] = branch
		return nil
	}
	if previous == "" {
		j.roots[root] = branch
		return nil
	}
	if branch != "" && previous != branch {
		j.roots[root] = ""
		j.conflicts[root] = true
	}
	return nil
}

func absoluteTranscriptPath(path string) bool {
	return filepath.IsAbs(path) || strings.HasPrefix(path, "/")
}

func supportedTranscriptTool(name string) bool {
	switch name {
	case "Bash", "Read", "Edit", "Write", "NotebookEdit", "EnterWorktree":
		return true
	default:
		return false
	}
}

// Absolute literals inside quoted command text also count (for example Python -c).
var embeddedToolPaths = regexp.MustCompile(`(?:^|[\s='"(])(/[^\s'"\\;&|<>()]+)`)

func collectAbsoluteToolPaths(raw, cwd string, paths map[string]bool) {
	word := strings.Trim(raw, "\"'")
	if _, value, ok := strings.Cut(word, "="); ok {
		word = strings.Trim(value, "\"'")
	}
	if absoluteTranscriptPath(word) {
		addToolPath(paths, word, cwd)
		// Preserve a single quoted path containing blanks. Multiple absolute literals
		// in one quoted script still need separate evidence.
		if !strings.Contains(word, " /") {
			return
		}
	}
	for _, match := range embeddedToolPaths.FindAllStringSubmatch(word, MaxResponseToolBytes) {
		addToolPath(paths, match[1], cwd)
	}
}
