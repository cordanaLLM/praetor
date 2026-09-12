package state

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
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

	var items []TaskItem
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	idx := 1

	for lines := 0; lines < maxScannedLines && scanner.Scan(); lines++ {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "- [ ] ") {
			desc := strings.TrimPrefix(line, "- [ ] ")
			items = append(items, TaskItem{
				Index:       idx,
				Description: desc,
				Completed:   false,
			})
			idx++
		} else if strings.HasPrefix(line, "- [x] ") || strings.HasPrefix(line, "- [X] ") {
			desc := strings.TrimPrefix(line, "- [x] ")
			desc = strings.TrimPrefix(desc, "- [X] ")
			items = append(items, TaskItem{
				Index:       idx,
				Description: desc,
				Completed:   true,
			})
			idx++
		}
	}

	return items, scanner.Err()
}

// AddTask appends a new pending task item to OPEN.md.
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

	newEntry := fmt.Sprintf("- [ ] %s\n", trimmed)
	updated := string(content)
	if !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += newEntry

	return contextopt.ReplaceSnapshot(ctx, openFile, []byte(updated), contextopt.ReplaceOptions{Expected: content, Exists: true, Mode: 0o600})
}

// CompleteTask marks a task as done in OPEN.md by 1-based index or substring match.
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
	targetIndex, errParse := strconv.Atoi(target)
	currentTaskIdx := 0
	found := false
	today := time.Now().UTC().Format("2006-01-02")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ] ") {
			currentTaskIdx++
			matches := false
			if errParse == nil && currentTaskIdx == targetIndex {
				matches = true
			} else if strings.Contains(strings.ToLower(trimmed), strings.ToLower(target)) {
				matches = true
			}

			if matches {
				desc := strings.TrimPrefix(trimmed, "- [ ] ")
				lines[i] = fmt.Sprintf("- [x] %s (completed: %s)", desc, today)
				found = true
				break
			}
		}
	}

	if !found {
		return fmt.Errorf("no pending task matched selector '%s'", selector)
	}

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

	remainingLines, completedTasks := splitCompletedTasks(string(content))

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

func splitCompletedTasks(content string) (remaining, completed []string) {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [x] ") || strings.HasPrefix(trimmed, "- [X] ") {
			completed = append(completed, trimmed)
		} else {
			remaining = append(remaining, line)
		}
	}
	return remaining, completed
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
