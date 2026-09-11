package milestone

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	MilestonesFile = "milestones.json"
	StateOpen      = "open"
	StateClosed    = "closed"
)

// Milestone represents an epic goal or version deliverable.
type Milestone struct {
	Number       int        `json:"number"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	State        string     `json:"state"`
	DueOn        *time.Time `json:"due_on,omitempty"`
	OpenIssues   int        `json:"open_issues"`
	ClosedIssues int        `json:"closed_issues"`
	Progress     float64    `json:"progress"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// MilestoneStore holds the collection of tracked milestones.
type MilestoneStore struct {
	Milestones []Milestone `json:"milestones"`
}

// ListMilestones returns all milestones matching stateFilter ("all", "open", "closed").
func ListMilestones(rootPath, stateFilter string) ([]Milestone, error) {
	store, err := loadStore(rootPath)
	if err != nil {
		return nil, err
	}

	filter := strings.ToLower(strings.TrimSpace(stateFilter))
	if filter == "" || filter == "all" {
		return store.Milestones, nil
	}

	var matched []Milestone
	for _, m := range store.Milestones {
		if strings.ToLower(m.State) == filter {
			matched = append(matched, m)
		}
	}
	return matched, nil
}

// CreateMilestone adds a new milestone to local store and synchronizes BACKLOG.md.
func CreateMilestone(rootPath, title, description string, dueOn *time.Time) (*Milestone, error) {
	trimmedTitle := strings.TrimSpace(title)
	if trimmedTitle == "" {
		return nil, fmt.Errorf("milestone title cannot be empty")
	}

	store, err := loadStore(rootPath)
	if err != nil {
		return nil, err
	}

	for _, existing := range store.Milestones {
		if strings.EqualFold(existing.Title, trimmedTitle) && existing.State == StateOpen {
			return nil, fmt.Errorf("an open milestone with title '%s' already exists", trimmedTitle)
		}
	}

	nextNum := 1
	for _, m := range store.Milestones {
		if m.Number >= nextNum {
			nextNum = m.Number + 1
		}
	}

	now := time.Now().UTC()
	m := Milestone{
		Number:       nextNum,
		Title:        trimmedTitle,
		Description:  strings.TrimSpace(description),
		State:        StateOpen,
		DueOn:        dueOn,
		OpenIssues:   0,
		ClosedIssues: 0,
		Progress:     0.0,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	store.Milestones = append(store.Milestones, m)
	if err := saveStore(rootPath, store); err != nil {
		return nil, err
	}

	if err := SyncToBacklog(rootPath); err != nil {
		return nil, fmt.Errorf("sync to backlog: %w", err)
	}
	return &m, nil
}

// CloseMilestone marks a milestone as closed by number or title substring.
func CloseMilestone(rootPath, selector string) (*Milestone, error) {
	store, err := loadStore(rootPath)
	if err != nil {
		return nil, err
	}

	target := strings.TrimSpace(selector)
	targetNum, parseErr := strconv.Atoi(target)

	var foundIdx = -1
	for i, m := range store.Milestones {
		if parseErr == nil && m.Number == targetNum {
			foundIdx = i
			break
		}
		if strings.Contains(strings.ToLower(m.Title), strings.ToLower(target)) {
			foundIdx = i
			break
		}
	}

	if foundIdx == -1 {
		return nil, fmt.Errorf("no milestone matching '%s' found", selector)
	}

	store.Milestones[foundIdx].State = StateClosed
	store.Milestones[foundIdx].Progress = 100.0
	store.Milestones[foundIdx].UpdatedAt = time.Now().UTC()

	if err := saveStore(rootPath, store); err != nil {
		return nil, err
	}

	if err := SyncToBacklog(rootPath); err != nil {
		return nil, fmt.Errorf("sync to backlog: %w", err)
	}
	return &store.Milestones[foundIdx], nil
}

// SyncToBacklog renders milestone summary into the ## Milestones section of BACKLOG.md.
func SyncToBacklog(rootPath string) error {
	store, err := loadStore(rootPath)
	if err != nil {
		return err
	}

	wDir := filepath.Join(rootPath, state.WorkingDirName)
	if err := os.MkdirAll(wDir, 0755); err != nil {
		return fmt.Errorf("mkdir workingdir: %w", err)
	}

	backlogPath := filepath.Join(wDir, "BACKLOG.md")
	content, err := os.ReadFile(backlogPath)
	if err != nil {
		if os.IsNotExist(err) {
			content = []byte("# Project Backlog\n\n")
		} else {
			return fmt.Errorf("read BACKLOG.md: %w", err)
		}
	}

	milestoneMD := RenderMilestonesMarkdown(store.Milestones)

	// Replace existing ## Milestones section or append
	lines := strings.Split(string(content), "\n")
	var newLines []string
	inMilestoneSection := false

	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "## Active Milestones") {
			inMilestoneSection = true
			continue
		}
		if inMilestoneSection && strings.HasPrefix(strings.TrimSpace(line), "## ") {
			inMilestoneSection = false
		}
		if !inMilestoneSection {
			newLines = append(newLines, line)
		}
	}

	rendered := strings.Join(newLines, "\n")
	if !strings.HasSuffix(rendered, "\n") {
		rendered += "\n"
	}
	rendered += "\n" + milestoneMD + "\n"

	return os.WriteFile(backlogPath, []byte(rendered), 0644)
}

// RenderMilestonesMarkdown converts milestones into GitHub-flavored Markdown table.
func RenderMilestonesMarkdown(milestones []Milestone) string {
	var sb strings.Builder
	sb.WriteString("## Active Milestones\n\n")

	if len(milestones) == 0 {
		sb.WriteString("*No tracked milestones. Use `standardsctl milestone create` to define goals.*\n")
		return sb.String()
	}

	sb.WriteString("| # | Title | State | Due Date | Progress |\n")
	sb.WriteString("| :- | :--- | :--- | :--- | :--- |\n")

	sort.Slice(milestones, func(i, j int) bool {
		return milestones[i].Number < milestones[j].Number
	})

	for _, m := range milestones {
		dueDate := "None"
		if m.DueOn != nil {
			dueDate = m.DueOn.Format("2006-01-02")
		}
		stateBadge := "🟢 Open"
		if m.State == StateClosed {
			stateBadge = "🟣 Closed"
		}
		sb.WriteString(fmt.Sprintf("| %d | **%s** | %s | %s | %.0f%% |\n",
			m.Number, m.Title, stateBadge, dueDate, m.Progress))
	}
	return sb.String()
}

func loadStore(rootPath string) (*MilestoneStore, error) {
	filePath := filepath.Join(rootPath, state.WorkingDirName, MilestonesFile)
	if !util.FileExists(filePath) {
		return &MilestoneStore{Milestones: []Milestone{}}, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read milestones store: %w", err)
	}

	var store MilestoneStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("unmarshal milestones store: %w", err)
	}
	return &store, nil
}

func saveStore(rootPath string, store *MilestoneStore) error {
	wDir := filepath.Join(rootPath, state.WorkingDirName)
	if err := os.MkdirAll(wDir, 0755); err != nil {
		return fmt.Errorf("mkdir workingdir: %w", err)
	}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal milestones: %w", err)
	}

	filePath := filepath.Join(wDir, MilestonesFile)
	return os.WriteFile(filePath, data, 0644)
}
