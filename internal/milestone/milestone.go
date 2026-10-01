package milestone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
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
	workingDirPerm = util.SecureDirPerm
	// ledgerFilePerm is the mode applied to milestones.json and BACKLOG.md.
	ledgerFilePerm = util.SecureFilePerm

	// restoreTimeout bounds the settlement a failed commit runs: the BACKLOG.md read-back
	// and the store restore (HISS-02). Both run on a context detached from the caller's
	// cancellation, so this is their only deadline.
	restoreTimeout = 30 * time.Second
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
	// PendingRemoteClose is true while a local close has not been confirmed by the forge.
	// A remote sync keeps the local closed state instead of reverting it to the forge's
	// open state and reports the divergence; `milestone close --publish` or a forge that
	// reports the milestone closed clears it.
	PendingRemoteClose bool      `json:"pending_remote_close,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
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

	for i := 0; i < len(store.Milestones) && i < MaxMilestonesLimit; i++ {
		existing := store.Milestones[i]
		if strings.EqualFold(existing.Title, trimmedTitle) && existing.State == StateOpen {
			return nil, fmt.Errorf("an open milestone with title '%s' already exists", trimmedTitle)
		}
	}

	now := time.Now().UTC()
	m := Milestone{
		Number:      nextLocalNumber(store.Milestones),
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

// CloseMilestone marks a milestone as closed by local number or by title substring. The
// close is local: an open milestone it closes is flagged PendingRemoteClose, so a later
// remote sync reports the forge's still-open state instead of silently reopening it.
// PublishClose carries the close to the forge.
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

	if store.Milestones[foundIdx].State != StateClosed {
		store.Milestones[foundIdx].PendingRemoteClose = true
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
	update, err := prepareBacklog(ctx, rootPath, store)
	if err != nil {
		return err
	}
	return writeBacklog(ctx, update)
}

// backlogUpdate is a rendered BACKLOG.md together with the snapshot it was rendered
// from, so the write can refuse to replace a file another writer changed in between.
type backlogUpdate struct {
	root     string
	data     []byte
	expected []byte
	exists   bool
}

func prepareBacklog(ctx context.Context, rootPath string, store *MilestoneStore) (*backlogUpdate, error) {
	observed, exists, err := observeBacklog(ctx, rootPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", BacklogFile, err)
	}
	content := "# Project Backlog\n\n"
	if exists {
		content = string(observed)
	}
	rendered, err := renderBacklog(content, store.Milestones)
	if err != nil {
		return nil, err
	}
	return &backlogUpdate{root: rootPath, data: []byte(rendered), expected: observed, exists: exists}, nil
}

// observeBacklog reads BACKLOG.md through the same pinned working-directory handle
// writeBacklog publishes through, so an in-repository .workingdir link resolves and an
// escaping one is refused (BUG-826). A working directory that does not exist yet reads as
// an absent BACKLOG.md; a file that vanishes once the directory is open is an error.
func observeBacklog(ctx context.Context, rootPath string) (data []byte, exists bool, err error) {
	opened := false
	err = util.InConfinedDirectory(rootPath, state.WorkingDirName, func(dir *os.Root) error {
		opened = true
		var readErr error
		data, exists, readErr = contextopt.ObserveRootSnapshot(ctx, dir, BacklogFile)
		return readErr
	})
	if !opened && errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return data, exists, err
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

// storeSnapshot is milestones.json as one read found it: its bytes, or its absence.
type storeSnapshot struct {
	data   []byte
	exists bool
}

// holds reports whether the snapshot is a present store carrying exactly data.
func (s storeSnapshot) holds(data []byte) bool {
	return s.exists && bytes.Equal(s.data, data)
}

// same reports whether two snapshots agree on existence and bytes.
func (s storeSnapshot) same(other storeSnapshot) bool {
	return s.exists == other.exists && bytes.Equal(s.data, other.data)
}

// ledgerWriters are the file writes a milestone commit makes. Production always uses the
// writers commitWriters starts with; the indirection is the seam a test fails one commit
// step through, deterministically and on every platform (#412). A test that swaps it must
// not run in parallel with another milestone test.
type ledgerWriters struct {
	writeStore   func(rootPath string, data []byte) error
	removeStore  func(rootPath string) error
	writeBacklog func(ctx context.Context, update *backlogUpdate) error
}

var commitWriters = ledgerWriters{
	writeStore:   writeStoreFile,
	removeStore:  removeStoreFile,
	writeBacklog: writeBacklog,
}

// commitStoreAndBacklog persists store and the BACKLOG.md block rendered from it as one
// unit for create, close and remote sync. Both outputs are rendered and validated before
// either file changes, and the milestones.json bytes (or its absence) are read first. When
// the store write fails, restoreStore puts milestones.json back; when the BACKLOG.md write
// fails, settleBacklogFailure first checks whether BACKLOG.md landed anyway. A returned
// error therefore never leaves the two ledgers disagreeing on this change, and a retry
// after a restore starts from the ledger the failed command found (#412).
//
// Both settle on a context detached from the caller's cancellation, because a cancelled or
// expired caller is one of the failures they handle, and under their own restoreTimeout.
func commitStoreAndBacklog(ctx context.Context, rootPath string, store *MilestoneStore) error {
	update, err := prepareBacklog(ctx, rootPath, store)
	if err != nil {
		return fmt.Errorf("sync to backlog: %w", err)
	}
	data, err := encodeStore(ctx, store)
	if err != nil {
		return err
	}
	prior, err := readStoreSnapshot(ctx, rootPath)
	if err != nil {
		return err
	}
	if err := commitWriters.writeStore(rootPath, data); err != nil {
		settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
		defer cancel()
		return restoreStore(settle, rootPath, prior, data, err)
	}
	if err := commitWriters.writeBacklog(ctx, update); err != nil {
		settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
		defer cancel()
		return settleBacklogFailure(settle, update, prior, data, fmt.Errorf("sync to backlog: %w", err))
	}
	return nil
}

// settleBacklogFailure decides what a failed BACKLOG.md write left behind before it undoes
// the store write. The locked compare-and-swap writer can fail after its rename or link
// already published BACKLOG.md: its directory sync reports a caller cancelled in that
// window, and a failed fsync, unlock or staging cleanup is reported after the publish too.
// The write error alone therefore does not prove BACKLOG.md is unchanged, so it is read
// back. Holding this commit's render as a change, BACKLOG.md landed and the store write
// is not undone, so both carry the change. Holding anything else, nothing of this commit
// landed there and restoreStore puts the store back. Unreadable, the outcome is unknown
// and the store is left with its change rather than restored blind; the error says which
// case applied.
func settleBacklogFailure(ctx context.Context, update *backlogUpdate, prior storeSnapshot, written []byte, cause error) error {
	landed, err := backlogLanded(ctx, update)
	if err != nil {
		return fmt.Errorf("%w; %s could not be read back, so %s keeps this change: %w", cause, BacklogFile, MilestonesFile, err)
	}
	if landed {
		return fmt.Errorf("%w (%s was published before the failure, so the %s write is not undone)", cause, BacklogFile, MilestonesFile)
	}
	return restoreStore(ctx, update.root, prior, written, cause)
}

// backlogLanded reports whether BACKLOG.md now holds update's render where the file it was
// rendered from did not. A render byte-identical to that file proves nothing about the
// write and leaves BACKLOG.md as found either way, so it counts as not landed and the
// store is restored to match.
func backlogLanded(ctx context.Context, update *backlogUpdate) (bool, error) {
	if update.exists && bytes.Equal(update.expected, update.data) {
		return false, nil
	}
	observed, exists, err := observeBacklog(ctx, update.root)
	if err != nil {
		return false, err
	}
	return exists && bytes.Equal(observed, update.data), nil
}

// restoreStore undoes the store write of a commit that failed with cause and returns cause
// together with what the restore did. A store still holding prior needs nothing. A store
// holding written, the bytes this commit wrote, is put back to prior: rewritten with the
// prior bytes, or removed when it did not exist before. A store another writer changed in
// between is left as found and reported, so the restore never discards that writer's
// update.
//
// ctx is the caller's detached settlement context (see commitStoreAndBacklog). The restore
// uses the lock-free writer every milestones.json write uses, so a BACKLOG.md write that
// failed on a busy working-directory lock cannot block it.
func restoreStore(ctx context.Context, rootPath string, prior storeSnapshot, written []byte, cause error) error {
	current, err := readStoreSnapshot(ctx, rootPath)
	if err != nil {
		return fmt.Errorf("%w; %s could not be checked and may keep this change: %w", cause, MilestonesFile, err)
	}
	if current.same(prior) {
		return cause
	}
	if !current.holds(written) {
		return fmt.Errorf("%w; %s changed under this command and was left as found", cause, MilestonesFile)
	}
	if err := putStore(rootPath, prior); err != nil {
		return fmt.Errorf("%w; restoring %s failed and it keeps this change: %w", cause, MilestonesFile, err)
	}
	return fmt.Errorf("%w (%s restored to its prior state)", cause, MilestonesFile)
}

// putStore makes milestones.json match snapshot: its bytes when it existed, absent when it
// did not. A working directory the failed commit created stays in place, empty of
// milestone state again.
func putStore(rootPath string, snapshot storeSnapshot) error {
	if snapshot.exists {
		return commitWriters.writeStore(rootPath, snapshot.data)
	}
	return commitWriters.removeStore(rootPath)
}

// writeBacklog publishes the rendered BACKLOG.md through the same locked
// compare-and-swap writer internal/state uses to append its task-discharge history
// (state.appendCompletedTasks), so neither writer can overwrite the other's concurrent
// update: a BACKLOG.md that changed since it was read fails the write instead. The
// working directory is opened through a pinned handle on the repository root, as every
// other ledger write here is, so a .workingdir swapped for an escaping link after
// prepareBacklog checked it is refused rather than followed (BUG-826).
func writeBacklog(ctx context.Context, update *backlogUpdate) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before writing backlog: %w", err)
	}
	if err := ensureWorkingDir(update.root); err != nil {
		return err
	}
	options := contextopt.ReplaceOptions{Expected: update.expected, Exists: update.exists, Mode: ledgerFilePerm}
	err := util.InConfinedDirectory(update.root, state.WorkingDirName, func(dir *os.Root) error {
		return contextopt.ReplaceRootSnapshot(ctx, dir, BacklogFile, update.data, options)
	})
	if err != nil {
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
	if err := ensureWorkingDir(rootPath); err != nil {
		return err
	}
	return util.WriteFileConfined(rootPath, filepath.Join(state.WorkingDirName, name), data, ledgerFilePerm)
}

// ensureWorkingDir creates the working directory below rootPath when absent, resolving
// every component through a pinned handle on rootPath (BUG-826).
func ensureWorkingDir(rootPath string) error {
	if err := util.MkdirConfined(rootPath, state.WorkingDirName, workingDirPerm); err != nil {
		return fmt.Errorf("mkdir workingdir: %w", err)
	}
	return nil
}

// readStoreSnapshot reads milestones.json through the confined path and without following
// a link. It is the one read of the store: loadStore parses it and a commit keeps it to
// restore from. An absent store is a snapshot, not an error.
func readStoreSnapshot(ctx context.Context, rootPath string) (storeSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return storeSnapshot{}, fmt.Errorf("context cancelled before reading the milestone store: %w", err)
	}
	filePath, err := workingDirFile(rootPath, MilestonesFile)
	if err != nil {
		return storeSnapshot{}, err
	}
	if !util.FileExists(filePath) {
		return storeSnapshot{}, nil
	}
	data, err := util.ReadFileNoFollow(filePath)
	if err != nil {
		return storeSnapshot{}, fmt.Errorf("read milestones store: %w", err)
	}
	return storeSnapshot{data: data, exists: true}, nil
}

func loadStore(ctx context.Context, rootPath string) (*MilestoneStore, error) {
	snapshot, err := readStoreSnapshot(ctx, rootPath)
	if err != nil {
		return nil, err
	}
	if !snapshot.exists {
		return &MilestoneStore{Milestones: []Milestone{}}, nil
	}

	var store MilestoneStore
	if err := json.Unmarshal(snapshot.data, &store); err != nil {
		return nil, fmt.Errorf("unmarshal milestones store: %w", err)
	}
	if len(store.Milestones) > MaxMilestonesLimit {
		return nil, fmt.Errorf("milestone store exceeds maximum of %d entries", MaxMilestonesLimit)
	}
	return &store, nil
}

func saveStore(ctx context.Context, rootPath string, store *MilestoneStore) error {
	data, err := encodeStore(ctx, store)
	if err != nil {
		return err
	}
	return writeStoreFile(rootPath, data)
}

// encodeStore checks the store against its bound and renders the bytes milestones.json
// holds. Every store write encodes through it, so a commit can compare what it wrote.
func encodeStore(ctx context.Context, store *MilestoneStore) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before writing the milestone store: %w", err)
	}
	if len(store.Milestones) > MaxMilestonesLimit {
		return nil, fmt.Errorf("milestone store exceeds maximum of %d entries", MaxMilestonesLimit)
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal milestones: %w", err)
	}
	return data, nil
}

// writeStoreFile replaces milestones.json with data through the confined atomic writer.
func writeStoreFile(rootPath string, data []byte) error {
	if err := writeWorkingDirFile(rootPath, MilestonesFile, data); err != nil {
		return fmt.Errorf("write milestones store: %w", err)
	}
	return nil
}

// removeStoreFile deletes milestones.json through a pinned handle on the working
// directory, so a .workingdir swapped for an escaping link is refused rather than followed
// (BUG-826). The name itself is removed, never a link's target.
func removeStoreFile(rootPath string) error {
	err := util.InConfinedDirectory(rootPath, state.WorkingDirName, func(dir *os.Root) error {
		return dir.Remove(MilestonesFile)
	})
	if err != nil {
		return fmt.Errorf("remove milestones store: %w", err)
	}
	return nil
}
