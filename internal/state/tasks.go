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

// The task ledgers parseLedgerTaskLines reads: OPEN.md holds the in-flight tasks the task
// commands number, BACKLOG.md the deferred workstreams and the discharged records.
const (
	openLedgerName    = "OPEN.md"
	backlogLedgerName = "BACKLOG.md"
)

// TaskItem represents an actionable task in OPEN.md or BACKLOG.md. Section is the text of the
// nearest Markdown heading above the row, empty when none precedes it. Line is the 1-based
// line of the row in its ledger.
type TaskItem struct {
	Index         int    `json:"index"`
	Description   string `json:"description"`
	Completed     bool   `json:"completed"`
	CompletedDate string `json:"completed_date,omitempty"`
	Section       string `json:"section,omitempty"`
	Line          int    `json:"line,omitempty"`
}

// ListTasks parses OPEN.md and returns all task items, bounded by the deadline
// contextopt gives one read (HISS-02).
func ListTasks(rootPath string) ([]TaskItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDuration)
	defer cancel()
	return ListTasksContext(ctx, rootPath)
}

// ListTasksContext reads a bounded task snapshot under the caller's deadline.
func ListTasksContext(ctx context.Context, rootPath string) ([]TaskItem, error) {
	return listLedgerTasks(ctx, rootPath, openLedgerName)
}

// ListBacklogTasksContext reads the checkbox rows of BACKLOG.md under the caller's deadline,
// through the parser OPEN.md is read with: rows inside a code fence are examples, an
// unterminated fence is an error, and pending and completed rows are numbered alike. The
// discharged records `state task archive` appends are completed rows. An absent BACKLOG.md
// holds no row.
func ListBacklogTasksContext(ctx context.Context, rootPath string) ([]TaskItem, error) {
	return listLedgerTasks(ctx, rootPath, backlogLedgerName)
}

// listLedgerTasks reads one task ledger of the working directory; an absent ledger holds no row.
func listLedgerTasks(ctx context.Context, rootPath, name string) ([]TaskItem, error) {
	content, err := readTaskLedger(ctx, filepath.Join(ledgerRoot(ctx, rootPath), WorkingDirName, name))
	if errors.Is(err, os.ErrNotExist) {
		return []TaskItem{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}

	parsed, err := parseLedgerTaskLines(ctx, name, strings.Split(string(content), "\n"))
	if err != nil {
		return nil, err
	}
	items := make([]TaskItem, 0, len(parsed))
	for _, task := range parsed {
		items = append(items, TaskItem{
			Index:       task.index,
			Description: task.description,
			Completed:   task.completed,
			Section:     task.section,
			Line:        task.line + 1,
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

	lRoot := ledgerRoot(ctx, rootPath)
	if err := InitWorkingDirContext(ctx, lRoot); err != nil {
		return err
	}

	openFile := filepath.Join(lRoot, WorkingDirName, "OPEN.md")
	content, err := contextopt.ReadSnapshot(ctx, openFile)
	if err != nil {
		return fmt.Errorf("read OPEN.md: %w", err)
	}

	if _, err := parseTaskLines(ctx, strings.Split(string(content), "\n")); err != nil {
		return err
	}

	newEntry := fmt.Sprintf("- [ ] %s\n", trimmed)
	updated := string(content)
	if !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += newEntry

	return contextopt.ReplaceSnapshot(ctx, openFile, []byte(updated), contextopt.ReplaceOptions{Expected: content, Exists: true, Mode: util.SecureFilePerm})
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

	openFile := filepath.Join(ledgerRoot(ctx, rootPath), WorkingDirName, "OPEN.md")
	content, err := contextopt.ReadSnapshot(ctx, openFile)
	if err != nil {
		return fmt.Errorf("read OPEN.md: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	parsed, err := parseTaskLines(ctx, lines)
	if err != nil {
		return err
	}
	task, err := selectTask(parsed, target)
	if err != nil {
		return err
	}
	today := time.Now().UTC().Format("2006-01-02")
	lines[task.line] = fmt.Sprintf("- [x] %s (completed: %s)", task.description, today)

	return contextopt.ReplaceSnapshot(ctx, openFile, []byte(strings.Join(lines, "\n")), contextopt.ReplaceOptions{Expected: content, Exists: true, Mode: util.SecureFilePerm})
}

// ArchiveCompletedTasks moves completed rows into BACKLOG.md, retaining one
// record for each still-existing worktree named by a completed row.
func ArchiveCompletedTasks(rootPath, commitSHA string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return archiveCompletedTasks(ctx, rootPath, commitSHA, defaultArchiveWorktreeOps())
}

// archiveCompletedTasks revalidates live worktrees immediately before publishing the archive.
func archiveCompletedTasks(ctx context.Context, rootPath, commitSHA string, ops archiveWorktreeOps) (int, error) {
	openFile := filepath.Join(ledgerRoot(ctx, rootPath), WorkingDirName, openLedgerName)
	content, err := readArchiveOpen(ctx, openFile)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read OPEN.md: %w", err)
	}
	plan, err := planTaskArchive(ctx, string(content), ops)
	if err != nil {
		return 0, err
	}
	if len(plan.archive) == 0 {
		return 0, nil
	}
	if err := retainWorktreeRows(ctx, rootPath, plan.index, plan.archive, ops); err != nil {
		return 0, err
	}
	// A new worktree can appear after the first listing; protect it before either write.
	if err := retainWorktreeRows(ctx, rootPath, plan.index, plan.archive, ops); err != nil {
		return 0, err
	}
	remaining, completed, err := plan.split(ctx)
	if err != nil || len(completed) == 0 {
		return 0, err
	}
	return publishTaskArchive(ctx, rootPath, openFile, commitSHA, content, remaining, completed)
}

// publishTaskArchive preserves completed rows first, then atomically replaces the observed OPEN.md.
func publishTaskArchive(ctx context.Context, rootPath, openFile, commitSHA string, content []byte, remaining, completed []string) (int, error) {
	updated := []byte(strings.Join(remaining, "\n"))
	if len(updated) > maxLedgerBytes {
		return 0, fmt.Errorf("OPEN.md recovery retains %d bytes; pending and live rows exceed the %d-byte read limit", len(updated), maxLedgerBytes)
	}
	if err := appendCompletedTasks(ctx, rootPath, commitSHA, completed); err != nil {
		return 0, err
	}
	options := contextopt.ReplaceOptions{Expected: content, Exists: true, Mode: util.SecureFilePerm, ExpectedLimit: maxArchiveRecoveryBytes}
	if err := contextopt.ReplaceSnapshot(ctx, openFile, updated, options); err != nil {
		return 0, fmt.Errorf("write OPEN.md: %w", err)
	}
	return len(completed), nil
}

// taskArchivePlan indexes rows once and carries the mutable set of completed row positions.
type taskArchivePlan struct {
	lines   []string
	archive map[int]bool
	index   map[string][]int
}

// planTaskArchive validates the shared task grammar before indexing structural record paths.
func planTaskArchive(ctx context.Context, content string, ops archiveWorktreeOps) (taskArchivePlan, error) {
	plan := taskArchivePlan{lines: strings.Split(content, "\n"), archive: make(map[int]bool)}
	if err := ctx.Err(); err != nil {
		return plan, err
	}
	if _, err := parseTaskLines(ctx, plan.lines); err != nil {
		return plan, err
	}
	rows, err := archiveTaskRows(ctx, plan.lines, plan.archive)
	if err != nil {
		return plan, err
	}
	if len(plan.archive) > 0 {
		plan.index, err = indexWorktreeRows(ctx, rows, ops)
		if err != nil {
			return plan, err
		}
		err = indexRejectedWorktreeRows(ctx, rows, plan.index)
	}
	return plan, err
}

// split partitions the bounded plan without changing any retained line's bytes.
func (plan taskArchivePlan) split(ctx context.Context) (remaining, completed []string, err error) {
	for i := 0; i < len(plan.lines) && i < maxScannedLines; i++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if plan.archive[i] {
			completed = append(completed, strings.TrimSpace(plan.lines[i]))
			continue
		}
		remaining = append(remaining, plan.lines[i])
	}
	return remaining, completed, nil
}

// splitCompletedTasks separates completed rows using the same grammar and retention index as archiving.
func splitCompletedTasks(ctx context.Context, rootPath, content string) (remaining, completed []string, err error) {
	ops := defaultArchiveWorktreeOps()
	plan, err := planTaskArchive(ctx, content, ops)
	if err != nil {
		return nil, nil, err
	}
	if len(plan.archive) > 0 {
		if err := retainWorktreeRows(ctx, rootPath, plan.index, plan.archive, ops); err != nil {
			return nil, nil, err
		}
	}
	return plan.split(ctx)
}

// appendCompletedTasks publishes completed records or bounded archive links in BACKLOG.md.
func appendCompletedTasks(ctx context.Context, rootPath, commitSHA string, completedTasks []string) error {
	backlogFile := filepath.Join(ledgerRoot(ctx, rootPath), WorkingDirName, "BACKLOG.md")
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
	archiveText, err := completedTaskText(ctx, archiveHeader, completedTasks)
	if err != nil {
		return err
	}
	if len(backlogContent)+len(archiveText) > maxLedgerBytes {
		archiveText, err = archiveCompletedChunks(ctx, rootPath, archiveHeader, backlogContent, completedTasks)
		if err != nil {
			return err
		}
		backlogContent = nil
	}
	updatedBacklog := string(backlogContent) + archiveText

	if err := contextopt.ReplaceSnapshot(ctx, backlogFile, []byte(updatedBacklog), contextopt.ReplaceOptions{Expected: expected, Exists: exists, Mode: util.SecureFilePerm}); err != nil {
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
	section     string
}

// parseTaskLines is the single numbering authority for OPEN.md. Listing and
// completion both resolve rows through it, so the number `state task list`
// prints is the number `state task complete` acts on. Pending and completed
// rows are numbered alike, and checkbox lines inside a fenced code block are
// examples, never tasks.
func parseTaskLines(ctx context.Context, lines []string) ([]taskLine, error) {
	return parseLedgerTaskLines(ctx, openLedgerName, lines)
}

// parseLedgerTaskLines parses the checkbox rows of the task ledger name, which its errors
// name. OPEN.md and BACKLOG.md share it, so both read fences, headings and rows alike.
func parseLedgerTaskLines(ctx context.Context, name string, lines []string) ([]taskLine, error) {
	if len(lines) > maxScannedLines {
		return nil, fmt.Errorf("%s exceeds the %d line scan bound", name, maxScannedLines)
	}
	tasks := make([]taskLine, 0, len(lines))
	var fence util.MarkdownFence
	opened, section := 0, ""
	for i := 0; i < len(lines); i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(lines[i]) >= maxTaskLineBytes {
			return nil, fmt.Errorf("%s line %d reaches the %d byte line bound", name, i+1, maxTaskLineBytes)
		}
		trimmed := strings.TrimSpace(lines[i])
		if !fence.Open() {
			opened = i + 1
		}
		if fence.Inside(trimmed) {
			continue
		}
		if heading, ok := util.MarkdownHeadingText(trimmed); ok {
			section = heading
			continue
		}
		description, completed, ok := taskCheckbox(trimmed)
		if !ok {
			continue
		}
		tasks = append(tasks, taskLine{index: len(tasks) + 1, line: i, description: description, completed: completed, section: section})
	}
	if fence.Open() {
		return nil, fmt.Errorf("%s has an unterminated code fence opened at line %d; every row after it would be read as an example", name, opened)
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
