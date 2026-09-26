package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxScannedLines is the scalar upper bound (HISS-02) on the number of lines a single
// file scan reads. No source or manifest file in a governed repository approaches it;
// the constant exists so every scanner loop has a statically verifiable bound.
const maxScannedLines = 200000

// TaskItem represents an actionable task in OPEN.md or BACKLOG.md.
type TaskItem struct {
	Index         int    `json:"index"`
	Description   string `json:"description"`
	Completed     bool   `json:"completed"`
	CompletedDate string `json:"completed_date,omitempty"`
}

// ListTasks parses OPEN.md and returns all task items.
func ListTasks(rootPath string) ([]TaskItem, error) {
	return ListTasksContext(context.Background(), rootPath)
}

// ListTasksContext reads a bounded task snapshot under the caller's deadline.
func ListTasksContext(ctx context.Context, rootPath string) ([]TaskItem, error) {
	openFile := filepath.Join(rootPath, WorkingDirName, "OPEN.md")
	content, err := contextopt.ReadSnapshot(ctx, openFile)
	if errors.Is(err, os.ErrNotExist) {
		return []TaskItem{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read OPEN.md: %w", err)
	}

	parsed, err := parseTaskLines(strings.Split(string(content), "\n"))
	if err != nil {
		return nil, err
	}
	items := make([]TaskItem, 0, len(parsed))
	for _, task := range parsed {
		items = append(items, TaskItem{
			Index:       task.index,
			Description: task.description,
			Completed:   task.completed,
		})
	}
	return items, nil
}

// AddTask appends a new pending task item to OPEN.md. The file is parsed through
// parseTaskLines first, exactly as listing and completion parse it, so a ledger
// the other commands refuse is not appended to: a row written under an
// unterminated code fence lands inside the fence, reads as an example to every
// later scan, and reports success while doing it.
func AddTask(rootPath, description string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	trimmed := strings.TrimSpace(description)
	if trimmed == "" {
		return fmt.Errorf("task description cannot be empty")
	}

	if err := InitWorkingDirContext(ctx, rootPath); err != nil {
		return err
	}

	openFile := filepath.Join(rootPath, WorkingDirName, "OPEN.md")
	content, err := contextopt.ReadSnapshot(ctx, openFile)
	if err != nil {
		return fmt.Errorf("read OPEN.md: %w", err)
	}

	if _, err := parseTaskLines(strings.Split(string(content), "\n")); err != nil {
		return err
	}

	newEntry := fmt.Sprintf("- [ ] %s\n", trimmed)
	updated := string(content)
	if !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += newEntry

	return contextopt.ReplaceSnapshot(ctx, openFile, []byte(updated), contextopt.ReplaceOptions{Expected: content, Exists: true, Mode: 0o600})
}

// CompleteTask marks exactly one pending task done in OPEN.md. The selector is
// either a task number as ListTasks reports it, or a substring that matches a
// single pending description; anything ambiguous or unmatched is an error and
// leaves the file untouched.
func CompleteTask(rootPath, selector string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	target := strings.TrimSpace(selector)
	if target == "" {
		return fmt.Errorf("task selector cannot be empty")
	}

	openFile := filepath.Join(rootPath, WorkingDirName, "OPEN.md")
	content, err := contextopt.ReadSnapshot(ctx, openFile)
	if err != nil {
		return fmt.Errorf("read OPEN.md: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	parsed, err := parseTaskLines(lines)
	if err != nil {
		return err
	}
	task, err := selectTask(parsed, target)
	if err != nil {
		return err
	}
	today := time.Now().UTC().Format("2006-01-02")
	lines[task.line] = fmt.Sprintf("- [x] %s (completed: %s)", task.description, today)

	return contextopt.ReplaceSnapshot(ctx, openFile, []byte(strings.Join(lines, "\n")), contextopt.ReplaceOptions{Expected: content, Exists: true, Mode: 0o600})
}

// ArchiveCompletedTasks moves all completed tasks from OPEN.md to BACKLOG.md.
func ArchiveCompletedTasks(rootPath, commitSHA string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	openFile := filepath.Join(rootPath, WorkingDirName, "OPEN.md")
	content, err := contextopt.ReadSnapshot(ctx, openFile)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read OPEN.md: %w", err)
	}

	remainingLines, completedTasks, err := splitCompletedTasks(string(content))
	if err != nil {
		return 0, err
	}

	if len(completedTasks) == 0 {
		return 0, nil
	}

	// Preserve completed records in BACKLOG.md before removing them from OPEN.md.
	// A failed second write can leave duplicates; it must never lose the records.
	if err := appendCompletedTasks(ctx, rootPath, commitSHA, completedTasks); err != nil {
		return 0, err
	}
	if err := contextopt.ReplaceSnapshot(ctx, openFile, []byte(strings.Join(remainingLines, "\n")), contextopt.ReplaceOptions{Expected: content, Exists: true, Mode: 0o600}); err != nil {
		return 0, fmt.Errorf("write OPEN.md: %w", err)
	}
	return len(completedTasks), nil
}

// splitCompletedTasks separates archivable rows from the rest of OPEN.md. It
// resolves rows through the shared numbering authority, so a completed checkbox
// inside a fenced code block stays where it is instead of being archived.
func splitCompletedTasks(content string) (remaining, completed []string, err error) {
	lines := strings.Split(content, "\n")
	parsed, err := parseTaskLines(lines)
	if err != nil {
		return nil, nil, err
	}
	archive := make(map[int]bool, len(lines))
	for _, task := range parsed {
		if task.completed {
			archive[task.line] = true
		}
	}
	for i, line := range lines {
		if archive[i] {
			completed = append(completed, strings.TrimSpace(line))
			continue
		}
		remaining = append(remaining, line)
	}
	return remaining, completed, nil
}

func appendCompletedTasks(ctx context.Context, rootPath, commitSHA string, completedTasks []string) error {
	backlogFile := filepath.Join(rootPath, WorkingDirName, "BACKLOG.md")
	backlogContent, err := contextopt.ReadSnapshot(ctx, backlogFile)
	expected := append([]byte{}, backlogContent...)
	exists := !errors.Is(err, os.ErrNotExist)
	if errors.Is(err, os.ErrNotExist) {
		backlogContent = []byte(defaultBacklogMD())
	} else if err != nil {
		return fmt.Errorf("read BACKLOG.md: %w", err)
	}

	timeStr := time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
	shaLabel := commitSHA
	if shaLabel == "" {
		shaLabel = "local"
	}

	archiveHeader := fmt.Sprintf("\n### Discharged Tasks [%s, commit `%s`]\n", timeStr, shaLabel)
	for _, t := range completedTasks {
		archiveHeader += fmt.Sprintf("%s\n", t)
	}

	updatedBacklog := string(backlogContent) + archiveHeader
	if err := contextopt.ReplaceSnapshot(ctx, backlogFile, []byte(updatedBacklog), contextopt.ReplaceOptions{Expected: expected, Exists: exists, Mode: 0o600}); err != nil {
		return fmt.Errorf("write BACKLOG.md: %w", err)
	}
	return nil
}

// maxSelectorCandidates bounds how many ambiguous matches an error message
// enumerates (HISS-02); further matches are reported as an ellipsis.
const maxSelectorCandidates = 8

// maxTaskLineBytes is the scalar upper bound (HISS-02) on a single OPEN.md
// line. It is bufio.MaxScanTokenSize, the bound the task scanner enforced
// before listing and completion were merged onto one parser: a line that fills
// the whole scan buffer leaves no room for its terminator, so a line of this
// size or larger is a corrupt ledger and is reported rather than truncated.
const maxTaskLineBytes = 64 * 1024

// taskLine is one checkbox row of OPEN.md, carrying the number every command
// reports for it and the line it occupies in the file.
type taskLine struct {
	index       int
	line        int
	description string
	completed   bool
}

// parseTaskLines is the single numbering authority for OPEN.md. Listing and
// completion both resolve rows through it, so the number `state task list`
// prints is the number `state task complete` acts on. Pending and completed
// rows are numbered alike, and checkbox lines inside a fenced code block are
// examples, never tasks.
func parseTaskLines(lines []string) ([]taskLine, error) {
	if len(lines) > maxScannedLines {
		return nil, fmt.Errorf("OPEN.md exceeds the %d line scan bound", maxScannedLines)
	}
	tasks := make([]taskLine, 0, len(lines))
	var fence util.MarkdownFence
	opened := 0
	for i := 0; i < len(lines); i++ {
		if len(lines[i]) >= maxTaskLineBytes {
			return nil, fmt.Errorf("OPEN.md line %d reaches the %d byte line bound", i+1, maxTaskLineBytes)
		}
		trimmed := strings.TrimSpace(lines[i])
		if !fence.Open() {
			opened = i + 1
		}
		if fence.Inside(trimmed) {
			continue
		}
		description, completed, ok := taskCheckbox(trimmed)
		if !ok {
			continue
		}
		tasks = append(tasks, taskLine{index: len(tasks) + 1, line: i, description: description, completed: completed})
	}
	if fence.Open() {
		return nil, fmt.Errorf("OPEN.md has an unterminated code fence opened at line %d; every row after it would be read as an example", opened)
	}
	return tasks, nil
}

// taskCheckbox splits a trimmed line into its checkbox state and description,
// reporting whether the line is a checkbox row at all.
func taskCheckbox(trimmed string) (description string, completed, ok bool) {
	switch {
	case strings.HasPrefix(trimmed, "- [ ] "):
		return strings.TrimPrefix(trimmed, "- [ ] "), false, true
	case strings.HasPrefix(trimmed, "- [x] "):
		return strings.TrimPrefix(trimmed, "- [x] "), true, true
	case strings.HasPrefix(trimmed, "- [X] "):
		return strings.TrimPrefix(trimmed, "- [X] "), true, true
	}
	return "", false, false
}

// selectTask resolves a selector to exactly one pending task. A numeric
// selector resolves only by task number and never falls back to matching a
// digit inside a description; a text selector matching more than one pending
// row is an error naming the candidates, as internal/milestone.selectMilestone
// does for milestones.
func selectTask(tasks []taskLine, target string) (taskLine, error) {
	if number, err := strconv.Atoi(target); err == nil {
		return taskByNumber(tasks, number)
	}
	matches := make([]taskLine, 0, 2)
	needle := strings.ToLower(target)
	for _, task := range tasks {
		if !task.completed && strings.Contains(strings.ToLower(task.description), needle) {
			matches = append(matches, task)
		}
	}
	switch len(matches) {
	case 0:
		return taskLine{}, fmt.Errorf("no pending task matched selector '%s'", target)
	case 1:
		return matches[0], nil
	default:
		return taskLine{}, fmt.Errorf("selector '%s' matches %d pending tasks (%s); use the task number", target, len(matches), taskCandidates(matches))
	}
}

// taskByNumber resolves a task number against the shared numbering. A number
// naming an already completed row is refused rather than silently retargeted.
func taskByNumber(tasks []taskLine, number int) (taskLine, error) {
	for _, task := range tasks {
		if task.index != number {
			continue
		}
		if task.completed {
			return taskLine{}, fmt.Errorf("task %d is already completed", number)
		}
		return task, nil
	}
	return taskLine{}, fmt.Errorf("no task numbered %d; %d tasks are listed", number, len(tasks))
}

// taskCandidates renders a bounded, numbered list of ambiguous matches so the
// operator can rerun the command with an exact number.
func taskCandidates(matches []taskLine) string {
	labels := make([]string, 0, len(matches))
	for i, task := range matches {
		if i >= maxSelectorCandidates {
			labels = append(labels, "...")
			break
		}
		labels = append(labels, fmt.Sprintf("%d: %s", task.index, task.description))
	}
	return strings.Join(labels, "; ")
}
