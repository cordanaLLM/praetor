package agentcontext

import (
	"errors"
	"fmt"
	"strings"

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
	lines = filterMutatingLines(lines)
	lines = filterPrimaryCommands(lines)

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
	inRule10 := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "10. **State ledger discipline (HISS-17).**") {
			inRule10 = true
			result = append(result, readOnlyRule10Lines...)
			continue
		}
		if inRule10 {
			if isRule10Terminator(trimmed) {
				inRule10 = false
				result = append(result, filterMutatingLine(line))
			}
			continue
		}
		result = append(result, filterMutatingLine(line))
	}
	return result
}

var readOnlyRule10Lines = []string{
	"10. **State ledger discipline (HISS-17).** Read-only session maintains no state ledger mutations. Whole `.workingdir` private + Git-ignored. Never stage its contents. Read existing context without writes.",
	"    - Turn start: read `.workingdir/OPEN.md` or `praetorctl state status` read-only when needed; never read whole `.workingdir/STATE.md` at turn start (~45k tokens).",
	"    - Read-only execution: no task additions, ledger mutations, checkpoint hooks, commits.",
}

func isRule10Terminator(trimmed string) bool {
	if strings.HasPrefix(trimmed, "11. ") || strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "<!-- ") {
		return true
	}
	return false
}

func filterMutatingLine(line string) string {
	if strings.Contains(line, "**HISS-17**") && strings.Contains(line, "state ledger") {
		return "| **HISS-17** state ledger | turn start: `praetorctl state status` + `.workingdir/OPEN.md`, never whole `.workingdir/STATE.md`; read-only: no ledger mutation | pre-commit / CI | gate |"
	}
	if strings.Contains(line, "`make verify-all`") {
		line = strings.ReplaceAll(line, "`make verify-all`", "verification gate")
	}
	if strings.Contains(line, "make verify-all") {
		line = strings.ReplaceAll(line, "make verify-all", "verification gate")
	}
	return line
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
			continue
		}
		result = append(result, line)
	}
	return result
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
