package agentcontext

import (
	"errors"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// CanonicalReadOnlyFile is the single canonical read-only context projection.
	CanonicalReadOnlyFile = "AGENTS.readonly.md"
	// ReadOnlyBanner is the caveman-compliant header stating the run is read-only.
	ReadOnlyBanner = "Run read-only: file edits, state mutations, commits prohibited. Mutating steps dropped; prohibitions kept."
	// gateName replaces the verification gate's command wherever prose or a table names it, so
	// a description of the gate survives without the command.
	gateName = "verification gate"
	// droppedCell stands in for a table cell whose every clause named a mutating step.
	droppedCell = "read-only run: step dropped"
	// readOnlyItemNote stands in for the first list item dropped from a list.
	readOnlyItemNote = "Read-only run: mutating steps dropped here; rule duties bind write runs only."
	// turnEndLead opens the turn-end steps in the canonical harness and the adopted one.
	turnEndLead = "before concluding any turn:"
)

// mutatingCommand matches the gate list: the commands a read-only run is never told to run.
// `state task` matches with the word after it, so the read-only `state task list` is told
// apart (readOnlySubcommand). Word boundaries keep prose such as "state synchronization" out.
var mutatingCommand = regexp.MustCompile(`\bmake (?:verify-all|state-audit)\b|\bstate sync\b|\bstate task\b(?:[ \t]+[a-z-]+)?|` +
	`\bagent-checkpoint-[a-z-]+\b|\bcommit with sign-off\b|\bgit[ \t]+(?:commit|push|add)\b`)

// negation marks a clause that forbids the command it names instead of asking for it.
var negation = regexp.MustCompile(`(?i)\b(?:never|not|no|don't|cannot|without)\b`)

var (
	listMarker = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)
	boldTitle  = regexp.MustCompile(`^\*\*[^*]+\*\*\s*`)
)

// ReadOnlyProjection transforms canonical AGENTS.md content into a read-only projection. It
// drops the turn-end steps, every fenced command line from the gate list, every clause, sentence
// or list item that asks for one, and renames the verification gate's command in prose, then
// states the run is read-only below the title. A clause that forbids a mutating command is a
// prohibition and stays. The result is checked to name no mutating command outside one.
func ReadOnlyProjection(content string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", errors.New("canonical AGENTS.md content is empty")
	}
	if lf, _, err := util.NormalizeLineEndingsStrict(content); err == nil {
		content = lf
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	lines = dropTurnEndSteps(lines)
	lines = filterLines(lines)
	lines = collapseBlankRuns(lines)
	lines = insertReadOnlyBanner(lines)
	result := strings.Join(lines, "\n") + "\n"
	if err := assertReadOnly(result); err != nil {
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

// dropTurnEndSteps removes each "Before concluding any turn:" lead with the steps it opens.
func dropTurnEndSteps(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		trimmed := strings.ToLower(strings.TrimSpace(lines[i]))
		if !strings.HasPrefix(trimmed, turnEndLead) {
			out = append(out, lines[i])
			continue
		}
		i = turnEndStepsEnd(lines, i)
	}
	return out
}

// turnEndStepsEnd returns the index of the last line of the turn-end steps the lead at index
// lead opens: the step block below it (a fenced block, else the lines up to the next blank
// line), and the paragraph right after that block when it names one of the block's commands,
// since it describes those steps. Steps written on the lead line itself leave the lines below.
func turnEndStepsEnd(lines []string, lead int) int {
	if rest := strings.TrimSpace(lines[lead])[len(turnEndLead):]; strings.TrimSpace(rest) != "" {
		return lead
	}
	start := skipBlank(lines, lead+1)
	end, commands := stepBlock(lines, start)
	next := skipBlank(lines, end+1)
	if last := paragraphEnd(lines, next); last >= next && mentionsAny(lines[next:last+1], commands) {
		return last
	}
	return end
}

// stepBlock returns the last index of the step block starting at start and the commands it
// runs: the command lines of a fenced block, else the code spans of the paragraph.
func stepBlock(lines []string, start int) (int, []string) {
	if start >= len(lines) {
		return start - 1, nil
	}
	if marker, ok := fenceOpen(lines[start]); ok {
		end, closed := fenceClose(lines, start, marker)
		return end, commandLines(fenceBody(lines[start:end+1], closed))
	}
	end := paragraphEnd(lines, start)
	var commands []string
	for i := start; i <= end; i++ {
		commands = append(commands, codeSpans(lines[i])...)
	}
	return end, commands
}

// paragraphEnd returns the last index of the plain paragraph starting at start, or start-1
// when start opens none: a heading, table, fence or HTML comment ends a paragraph.
func paragraphEnd(lines []string, start int) int {
	end := start - 1
	for i := start; i < len(lines) && !isBlank(lines[i]) && !isStructural(lines[i]); i++ {
		end = i
	}
	return end
}

func isStructural(line string) bool {
	trimmed := strings.TrimSpace(line)
	_, fence := fenceOpen(line)
	return fence || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "|") || strings.HasPrefix(trimmed, "<!--")
}

func skipBlank(lines []string, start int) int {
	for start < len(lines) && isBlank(lines[start]) {
		start++
	}
	return start
}

func isBlank(line string) bool { return strings.TrimSpace(line) == "" }

func mentionsAny(lines []string, commands []string) bool {
	for _, line := range lines {
		for _, command := range commands {
			if command != "" && strings.Contains(line, command) {
				return true
			}
		}
	}
	return false
}

// codeSpans returns the text of each `code span` of line.
func codeSpans(line string) []string {
	parts := strings.Split(line, "`")
	var spans []string
	for i := 1; i < len(parts)-1; i += 2 {
		spans = append(spans, parts[i])
	}
	return spans
}

// commandLines returns the trimmed lines of a fence body that are neither blank nor comments.
func commandLines(body []string) []string {
	var commands []string
	for _, line := range body {
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			commands = append(commands, trimmed)
		}
	}
	return commands
}

// ReadOnlyTarget projects content, the canonical AGENTS.md text, into the CanonicalReadOnlyFile
// target, counted the way the vendor files are (TargetFile.LineCount).
func ReadOnlyTarget(content string) (TargetFile, error) {
	projection, err := ReadOnlyProjection(content)
	if err != nil {
		return TargetFile{}, err
	}
	return TargetFile{RelativePath: CanonicalReadOnlyFile, Content: projection, LineCount: strings.Count(projection, "\n") + 1}, nil
}
