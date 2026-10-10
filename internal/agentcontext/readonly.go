package agentcontext

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// CanonicalReadOnlyFile is the single canonical read-only context projection.
	CanonicalReadOnlyFile = "AGENTS.readonly.md"
	// ReadOnlyBanner is the caveman-compliant header stating the run is read-only.
	ReadOnlyBanner = "Run read-only: file edits, state mutations, commits prohibited. Mutating turn-end steps dropped."
)

// forbiddenMutatingCommands are mutating commands from the gate list that must never appear in
// a read-only context projection.
var forbiddenMutatingCommands = []string{
	"make verify-all",
	"state sync",
	"state task add",
	"state task complete",
	"state task archive",
	"agent-checkpoint-tool",
	"agent-checkpoint-stop",
	"commit with sign-off",
	"git commit",
	"git push",
	"git add",
}

// ReadOnlyProjection transforms canonical AGENTS.md content into a read-only projection.
// It inserts the read-only banner at the top, drops mutating turn-end steps and ledger commands,
// and verifies that no mutating gate commands remain.
func ReadOnlyProjection(content string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", errors.New("canonical AGENTS.md content is empty")
	}
	if lf, _, err := util.NormalizeLineEndingsStrict(content); err == nil {
		content = lf
	}
	lines := strings.Split(content, "\n")
	lines = insertReadOnlyBanner(lines)
	lines = dropTurnEndBlock(lines)
	lines = filterPrimaryCommands(lines)
	lines = filterMutatingLines(lines)

	result := strings.Join(lines, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	if err := assertNoMutatingCommands(result); err != nil {
		return "", err
	}
	return result, nil
}

// insertReadOnlyBanner places ReadOnlyBanner directly after the top-level H1 heading.
func insertReadOnlyBanner(lines []string) []string {
	result := make([]string, 0, len(lines)+2)
	inserted := false
	for i := 0; i < len(lines); i++ {
		result = append(result, lines[i])
		if !inserted && strings.HasPrefix(strings.TrimSpace(lines[i]), "# ") {
			result = append(result, "", ReadOnlyBanner)
			inserted = true
		}
	}
	if !inserted {
		return append([]string{ReadOnlyBanner, ""}, lines...)
	}
	return result
}

// dropTurnEndBlock removes the 'Before concluding any turn:' block up to the next H2 section.
func dropTurnEndBlock(lines []string) []string {
	result := make([]string, 0, len(lines))
	inDrop := false
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(strings.ToLower(trimmed), "before concluding any turn:") {
			inDrop = true
			continue
		}
		if inDrop {
			if strings.HasPrefix(trimmed, "## ") {
				inDrop = false
				result = append(result, lines[i])
			}
			continue
		}
		result = append(result, lines[i])
	}
	return result
}

// filterMutatingLines rewrites HISS-17 table row, Rule 2, and Rule 10 to eliminate mutating instructions.
func filterMutatingLines(lines []string) []string {
	result := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "**State ledger discipline") && strings.Contains(trimmed, "HISS-17") {
			rule10Lines, next := filterRule10(lines, i)
			result = append(result, rule10Lines...)
			i = next
			continue
		}
		if filtered, ok := filterMutatingLine(line); ok {
			result = append(result, filtered)
		}
	}
	return result
}

func filterRule10(lines []string, start int) ([]string, int) {
	result := make([]string, 0, 8)
	lead := lines[start]
	lead = strings.ReplaceAll(lead, "Agents MUST maintain local `.workingdir` ledger every turn.", "Read-only session maintains no state ledger mutations.")
	lead = strings.ReplaceAll(lead, "Agents MUST maintain local .workingdir ledger every turn.", "Read-only session maintains no state ledger mutations.")
	result = append(result, lead)
	i := start + 1
	for ; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if isRule10Terminator(trimmed) {
			break
		}
		if trimmed == "" || containsForbiddenCommand(lines[i]) || strings.Contains(lines[i], "state-audit") {
			continue
		}
		result = append(result, lines[i])
	}
	result = append(result, "    - Read-only execution: no task additions, ledger mutations, checkpoint hooks, commits.", "")
	return result, i - 1
}

func isRule10Terminator(trimmed string) bool {
	return strings.HasPrefix(trimmed, "11. ") || strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "<!-- ")
}

func filterHISS17Row(line string) string {
	parts := strings.Split(line, "|")
	if len(parts) < 5 {
		return line
	}
	rule := strings.TrimSpace(parts[2])
	if idx := strings.Index(rule, "; tasks via"); idx != -1 {
		rule = rule[:idx] + "; read-only: no ledger mutation"
	} else if idx := strings.Index(rule, "; turn end"); idx != -1 {
		rule = rule[:idx] + "; read-only: no ledger mutation"
	} else if idx := strings.Index(rule, "turn end"); idx != -1 {
		rule = rule[:idx] + "read-only: no ledger mutation"
	} else {
		rule = "read-only: no ledger mutation"
	}
	parts[2] = " " + rule + " "
	return strings.Join(parts, "|")
}

func filterMutatingLine(line string) (string, bool) {
	if strings.Contains(line, "**HISS-17**") && strings.Contains(line, "state ledger") {
		return filterHISS17Row(line), true
	}
	if strings.Contains(line, "`make verify-all`") {
		line = strings.ReplaceAll(line, "`make verify-all`", "verification gate")
	}
	if strings.Contains(line, "make verify-all") {
		line = strings.ReplaceAll(line, "make verify-all", "verification gate")
	}
	if containsForbiddenCommand(line) {
		return "", false
	}
	return line, true
}

// filterPrimaryCommands removes make verify-all and its introducing comment from Primary Verification Commands.
func filterPrimaryCommands(lines []string) []string {
	result := make([]string, 0, len(lines))
	inSection := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## Primary Verification Commands") {
			inSection = true
		} else if strings.HasPrefix(trimmed, "## ") {
			inSection = false
		}
		if inSection && trimmed == "make verify-all" {
			if len(result) > 0 && strings.HasPrefix(strings.TrimSpace(result[len(result)-1]), "#") {
				result = result[:len(result)-1]
			}
			if len(result) > 0 && strings.TrimSpace(result[len(result)-1]) == "" {
				result = result[:len(result)-1]
			}
			continue
		}
		result = append(result, line)
	}
	return result
}

func containsForbiddenCommand(s string) bool {
	for _, cmd := range forbiddenMutatingCommands {
		if strings.Contains(s, cmd) {
			return true
		}
	}
	return false
}

// assertNoMutatingCommands verifies that zero mutating gate commands remain in the text.
func assertNoMutatingCommands(text string) error {
	for _, cmd := range forbiddenMutatingCommands {
		if strings.Contains(text, cmd) {
			return fmt.Errorf("read-only projection contains mutating command %q", cmd)
		}
	}
	return nil
}

// ContextForBrief returns the read-only context projection if brief is marked read-only,
// or the full content unchanged if brief is not read-only.
func ContextForBrief(content, brief string) (string, error) {
	isReadOnly, err := caveman.ExtractBriefReadOnly(brief)
	if err != nil {
		return "", err
	}
	if isReadOnly {
		return ReadOnlyProjection(content)
	}
	return content, nil
}

// ContextForRole returns the read-only context projection if role represents a read-only agent,
// or the full content unchanged otherwise.
func ContextForRole(content, role string) (string, error) {
	if IsReadOnlyRole(role) {
		return ReadOnlyProjection(content)
	}
	return content, nil
}

// IsReadOnlyRole reports whether role describes a read-only agent (e.g. audit, review, research).
func IsReadOnlyRole(role string) bool {
	r := strings.ToLower(strings.TrimSpace(role))
	if r == "" {
		return false
	}
	switch r {
	case "research", "researcher", "review", "reviewer", "audit", "auditor", "praetor-auditor", "read-only", "readonly":
		return true
	}
	return strings.Contains(r, "audit") || strings.Contains(r, "review") || strings.Contains(r, "research") || strings.Contains(r, "readonly")
}
