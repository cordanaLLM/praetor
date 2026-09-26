package milestone

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	MilestonesFile = "milestones.json"
	BacklogFile    = "BACKLOG.md"
	StateOpen      = "open"
	StateClosed    = "closed"

	// MaxMilestonesLimit bounds every iteration over the milestone store (HISS-02).
	MaxMilestonesLimit = 10000
	// MaxBacklogLines bounds the line scan over BACKLOG.md (HISS-02).
	MaxBacklogLines = 200000

	// milestoneSectionStart and milestoneSectionEnd delimit the generated block inside
	// BACKLOG.md. Only the text between them is replaced on a sync, so every other
	// ledger block in the file - including the "###" task-discharge history that
	// internal/state appends after it - survives untouched.
	milestoneSectionStart = "<!-- praetor:milestones:start -->"
	milestoneSectionEnd   = "<!-- praetor:milestones:end -->"
	milestoneHeading      = "## Active Milestones"

	// workingDirPerm is the mode applied to the working directory holding the ledger.
	workingDirPerm = 0o750
	// ledgerFilePerm is the mode applied to milestones.json and BACKLOG.md.
	ledgerFilePerm = 0o644
)

// Milestone represents an epic goal or version deliverable.
type Milestone struct {
	// Number is the stable local identifier. It is assigned once at creation and never
	// rewritten by a remote sync, so `milestone close <number>` always addresses the
	// same milestone.
	Number int `json:"number"`
	// RemoteNumber is the number the forge assigned to the published milestone, or 0
	// when the milestone exists only locally.
	RemoteNumber int        `json:"remote_number,omitempty"`
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
func ListMilestones(ctx context.Context, rootPath, stateFilter string) ([]Milestone, error) {
	store, err := loadStore(ctx, rootPath)
	if err != nil {
		return nil, err
	}

	filter := strings.ToLower(strings.TrimSpace(stateFilter))
	if filter == "" || filter == "all" {
		return store.Milestones, nil
	}

	matched := make([]Milestone, 0, len(store.Milestones))
	for i := 0; i < len(store.Milestones) && i < MaxMilestonesLimit; i++ {
		if strings.EqualFold(store.Milestones[i].State, filter) {
			matched = append(matched, store.Milestones[i])
		}
	}
	return matched, nil
}

// CreateMilestone adds a new milestone to local store and synchronizes BACKLOG.md.
func CreateMilestone(ctx context.Context, rootPath, title, description string, dueOn *time.Time) (*Milestone, error) {
	trimmedTitle := sanitizeTitle(title)
	if trimmedTitle == "" {
		return nil, fmt.Errorf("milestone title cannot be empty")
	}

	store, err := loadStore(ctx, rootPath)
	if err != nil {
		return nil, err
	}
	if len(store.Milestones) >= MaxMilestonesLimit {
		return nil, fmt.Errorf("milestone store holds the maximum of %d entries", MaxMilestonesLimit)
	}

	nextNum := 1
	for i := 0; i < len(store.Milestones) && i < MaxMilestonesLimit; i++ {
		existing := store.Milestones[i]
		if strings.EqualFold(existing.Title, trimmedTitle) && existing.State == StateOpen {
			return nil, fmt.Errorf("an open milestone with title '%s' already exists", trimmedTitle)
		}
		if existing.Number >= nextNum {
			nextNum = existing.Number + 1
		}
	}

	now := time.Now().UTC()
	m := Milestone{
		Number:      nextNum,
		Title:       trimmedTitle,
		Description: strings.TrimSpace(description),
		State:       StateOpen,
		DueOn:       dueOn,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	store.Milestones = append(store.Milestones, m)
	if err := commitStoreAndBacklog(ctx, rootPath, store); err != nil {
		return nil, err
	}
	return &m, nil
}

// CloseMilestone marks a milestone as closed by local number or by title substring.
//
// A numeric selector is matched against the local number only: it never falls through to
// a substring match, which would let "1" close a milestone titled "v1.0". A textual
// selector must match exactly one title, and an empty selector is rejected instead of
// matching every milestone.
func CloseMilestone(ctx context.Context, rootPath, selector string) (*Milestone, error) {
	target := strings.TrimSpace(selector)
	if target == "" {
		return nil, fmt.Errorf("milestone selector cannot be empty")
	}

	store, err := loadStore(ctx, rootPath)
	if err != nil {
		return nil, err
	}

	foundIdx, err := selectMilestone(store.Milestones, target)
	if err != nil {
		return nil, err
	}

	store.Milestones[foundIdx].State = StateClosed
	store.Milestones[foundIdx].Progress = 100.0
	store.Milestones[foundIdx].UpdatedAt = time.Now().UTC()

	if err := commitStoreAndBacklog(ctx, rootPath, store); err != nil {
		return nil, err
	}
	return &store.Milestones[foundIdx], nil
}

// selectMilestone resolves a selector to exactly one milestone index.
func selectMilestone(milestones []Milestone, target string) (int, error) {
	if num, parseErr := strconv.Atoi(target); parseErr == nil {
		for i := 0; i < len(milestones) && i < MaxMilestonesLimit; i++ {
			if milestones[i].Number == num {
				return i, nil
			}
		}
		return -1, fmt.Errorf("no milestone with number %d found", num)
	}

	matches := make([]int, 0, 2)
	needle := strings.ToLower(target)
	for i := 0; i < len(milestones) && i < MaxMilestonesLimit; i++ {
		if strings.Contains(strings.ToLower(milestones[i].Title), needle) {
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 0:
		return -1, fmt.Errorf("no milestone matching '%s' found", target)
	case 1:
		return matches[0], nil
	default:
		return -1, fmt.Errorf("selector '%s' matches %d milestones; use the milestone number", target, len(matches))
	}
}

// SyncToBacklog renders the milestone summary into the delimited milestone block of
// BACKLOG.md, leaving every other section of the ledger untouched.
func SyncToBacklog(ctx context.Context, rootPath string) error {
	store, err := loadStore(ctx, rootPath)
	if err != nil {
		return err
	}
	update, err := prepareBacklog(rootPath, store)
	if err != nil {
		return err
	}
	return writeBacklog(ctx, update)
}

type backlogUpdate struct {
	root string
	data []byte
}

func prepareBacklog(rootPath string, store *MilestoneStore) (*backlogUpdate, error) {
	backlogPath, err := workingDirFile(rootPath, BacklogFile)
	if err != nil {
		return nil, err
	}
	content := "# Project Backlog\n\n"
	if util.PathExists(backlogPath) {
		data, readErr := util.ReadFileNoFollow(backlogPath)
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", BacklogFile, readErr)
		}
		content = string(data)
	}
	rendered, err := renderBacklog(content, store.Milestones)
	if err != nil {
		return nil, err
	}
	return &backlogUpdate{root: rootPath, data: []byte(rendered)}, nil
}

func renderBacklog(content string, milestones []Milestone) (string, error) {
	first, _, err := util.FindMarkedBlockWithinBudget(content, milestoneSectionStart, milestoneSectionEnd, MaxBacklogLines)
	if err != nil {
		return "", fmt.Errorf("validate milestone block in %s: %w", BacklogFile, err)
	}
	base := content
	if first >= 0 {
		base, _, err = util.ReplaceMarkedBlockWithinBudget(content, milestoneSectionStart, milestoneSectionEnd,
			milestoneSectionStart+"\n"+milestoneSectionEnd, MaxBacklogLines)
		if err != nil {
			return "", fmt.Errorf("clear milestone block in %s: %w", BacklogFile, err)
		}
	}
	base, err = util.RemoveMarkdownSection(base, milestoneHeading, MaxBacklogLines)
	if err != nil {
		return "", fmt.Errorf("remove legacy milestone section in %s: %w", BacklogFile, err)
	}
	rendered, _, err := util.ReplaceMarkedBlockWithinBudget(base, milestoneSectionStart, milestoneSectionEnd, milestoneBlock(milestones), MaxBacklogLines)
	if err != nil {
		return "", fmt.Errorf("render milestone block in %s: %w", BacklogFile, err)
	}
	return rendered, nil
}

func commitStoreAndBacklog(ctx context.Context, rootPath string, store *MilestoneStore) error {
	update, err := prepareBacklog(rootPath, store)
	if err != nil {
		return fmt.Errorf("sync to backlog: %w", err)
	}
	if err := saveStore(ctx, rootPath, store); err != nil {
		return err
	}
	if err := writeBacklog(ctx, update); err != nil {
		return fmt.Errorf("sync to backlog: %w", err)
	}
	return nil
}

func writeBacklog(ctx context.Context, update *backlogUpdate) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before writing backlog: %w", err)
	}
	if err := writeWorkingDirFile(update.root, BacklogFile, update.data); err != nil {
		return fmt.Errorf("write %s: %w", BacklogFile, err)
	}
	return nil
}

// milestoneBlock renders the marker-delimited milestone block.
func milestoneBlock(milestones []Milestone) string {
	return milestoneSectionStart + "\n" + RenderMilestonesMarkdown(milestones) + milestoneSectionEnd + "\n"
}

// RenderMilestonesMarkdown converts milestones into a GitHub-flavored Markdown table.
// Titles are sanitized: a forge-supplied title carrying a newline or a pipe would
// otherwise break the table and the block scan that replaces it.
func RenderMilestonesMarkdown(milestones []Milestone) string {
	var sb strings.Builder
	sb.WriteString(milestoneHeading + "\n\n")

	if len(milestones) == 0 {
		sb.WriteString("*No tracked milestones. Use `praetorctl milestone create` to define goals.*\n")
		return sb.String()
	}

	sb.WriteString("| # | Title | State | Due Date | Progress |\n")
	sb.WriteString("| :- | :--- | :--- | :--- | :--- |\n")

	ordered := slices.Clone(milestones)
	slices.SortStableFunc(ordered, func(a, b Milestone) int { return a.Number - b.Number })

	for i := 0; i < len(ordered) && i < MaxMilestonesLimit; i++ {
		m := ordered[i]
		dueDate := "None"
		if m.DueOn != nil {
			dueDate = m.DueOn.Format("2006-01-02")
		}
		stateBadge := "🟢 Open"
		if m.State == StateClosed {
			stateBadge = "🟣 Closed"
		}
		row := fmt.Sprintf("| %d | **%s** | %s | %s | %.0f%% |\n",
			m.Number, sanitizeCell(m.Title), stateBadge, dueDate, m.Progress)
		sb.WriteString(row)
	}
	return sb.String()
}

// sanitizeTitle collapses the line breaks and control characters a title must never
// carry into the ledger.
func sanitizeTitle(title string) string {
	replaced := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, title)
	return strings.TrimSpace(replaced)
}

// sanitizeCell renders a value safely inside a Markdown table cell.
func sanitizeCell(value string) string {
	return strings.ReplaceAll(sanitizeTitle(value), "|", `\|`)
}

// workingDirFile resolves a ledger file inside the repository working directory,
// refusing a path that escapes rootPath lexically or through a symbolic link.
func workingDirFile(rootPath, name string) (string, error) {
	path, err := util.ConfinePath(rootPath, filepath.Join(state.WorkingDirName, name))
	if err != nil {
		return "", fmt.Errorf("resolve %s under %q: %w", name, rootPath, err)
	}
	return path, nil
}

// writeWorkingDirFile creates the working directory when absent and replaces one ledger
// file in it atomically. Both steps resolve every path component through a pinned handle on
// rootPath, so neither .workingdir nor the ledger can be redirected outside the repository,
// not even by a link swapped in after a check (BUG-826).
func writeWorkingDirFile(rootPath, name string, data []byte) error {
	if err := util.MkdirConfined(rootPath, state.WorkingDirName, workingDirPerm); err != nil {
		return fmt.Errorf("mkdir workingdir: %w", err)
	}
	return util.WriteFileConfined(rootPath, filepath.Join(state.WorkingDirName, name), data, ledgerFilePerm)
}

func loadStore(ctx context.Context, rootPath string) (*MilestoneStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before reading the milestone store: %w", err)
	}

	filePath, err := workingDirFile(rootPath, MilestonesFile)
	if err != nil {
		return nil, err
	}
	if !util.FileExists(filePath) {
		return &MilestoneStore{Milestones: []Milestone{}}, nil
	}

	data, err := util.ReadFileNoFollow(filePath)
	if err != nil {
		return nil, fmt.Errorf("read milestones store: %w", err)
	}

	var store MilestoneStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("unmarshal milestones store: %w", err)
	}
	if len(store.Milestones) > MaxMilestonesLimit {
		return nil, fmt.Errorf("milestone store exceeds maximum of %d entries", MaxMilestonesLimit)
	}
	return &store, nil
}

func saveStore(ctx context.Context, rootPath string, store *MilestoneStore) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before writing the milestone store: %w", err)
	}
	if len(store.Milestones) > MaxMilestonesLimit {
		return fmt.Errorf("milestone store exceeds maximum of %d entries", MaxMilestonesLimit)
	}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal milestones: %w", err)
	}

	if err := writeWorkingDirFile(rootPath, MilestonesFile, data); err != nil {
		return fmt.Errorf("write milestones store: %w", err)
	}
	return nil
}
