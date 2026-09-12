package state

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	openFile := filepath.Join(rootPath, WorkingDirName, "OPEN.md")
	if !util.FileExists(openFile) {
		return []TaskItem{}, nil
	}

	content, err := os.ReadFile(openFile)
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
	trimmed := strings.TrimSpace(description)
	if trimmed == "" {
		return fmt.Errorf("task description cannot be empty")
	}

	if err := InitWorkingDir(rootPath); err != nil {
		return err
	}

	openFile := filepath.Join(rootPath, WorkingDirName, "OPEN.md")
	content, err := os.ReadFile(openFile)
	if err != nil {
		content = []byte(defaultOpenMD())
	}

	newEntry := fmt.Sprintf("- [ ] %s\n", trimmed)
	updated := string(content)
	if !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += newEntry

	return os.WriteFile(openFile, []byte(updated), 0644)
}

// CompleteTask marks a task as done in OPEN.md by 1-based index or substring match.
func CompleteTask(rootPath, selector string) error {
	target := strings.TrimSpace(selector)
	if target == "" {
		return fmt.Errorf("task selector cannot be empty")
	}

	openFile := filepath.Join(rootPath, WorkingDirName, "OPEN.md")
	content, err := os.ReadFile(openFile)
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

	return os.WriteFile(openFile, []byte(strings.Join(lines, "\n")), 0644)
}

// ArchiveCompletedTasks moves all completed tasks from OPEN.md to BACKLOG.md.
func ArchiveCompletedTasks(rootPath, commitSHA string) (int, error) {
	openFile := filepath.Join(rootPath, WorkingDirName, "OPEN.md")
	if !util.FileExists(openFile) {
		return 0, nil
	}

	content, err := os.ReadFile(openFile)
	if err != nil {
		return 0, fmt.Errorf("read OPEN.md: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	var remainingLines []string
	var completedTasks []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [x] ") || strings.HasPrefix(trimmed, "- [X] ") {
			completedTasks = append(completedTasks, trimmed)
		} else {
			remainingLines = append(remainingLines, line)
		}
	}

	if len(completedTasks) == 0 {
		return 0, nil
	}

	// Update OPEN.md
	if err := os.WriteFile(openFile, []byte(strings.Join(remainingLines, "\n")), 0644); err != nil {
		return 0, fmt.Errorf("write OPEN.md: %w", err)
	}

	// Append to BACKLOG.md
	backlogFile := filepath.Join(rootPath, WorkingDirName, "BACKLOG.md")
	backlogContent, err := os.ReadFile(backlogFile)
	if err != nil {
		backlogContent = []byte(defaultBacklogMD())
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
	if err := os.WriteFile(backlogFile, []byte(updatedBacklog), 0644); err != nil {
		return 0, fmt.Errorf("write BACKLOG.md: %w", err)
	}

	return len(completedTasks), nil
}
