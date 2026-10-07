// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package backlogcap counts each capped backlog category against the cap the effective policy
// resolved for it (#792): `praetorctl state status` prints the counts, `praetorctl state batch`
// writes one batch file per category over its cap, and `praetorctl audit` fails on a gated one.
//
// The caps are policy keys (internal/config, backlog_caps.go); the counts come from the ledger
// readers internal/state already has. A category praetor cannot count is reported as not
// counted with the reason, never as an empty category: it has no reader, a ledger it reads is
// absent from this checkout, or a ledger could not be read.
package backlogcap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/state"
)

const (
	// evaluateTimeout bounds one evaluation's ledger reads (HISS-02).
	evaluateTimeout = 30 * time.Second
	// maxItems bounds one category's items (HISS-02). It is the largest max a layer may
	// declare, so every declared cap can be evaluated; a ledger holding more fails the count
	// rather than counting low.
	maxItems = config.MaxBacklogCap
)

// State is where a category's count stands against its cap. The cap is inclusive: a count
// equal to max is at the cap and passes; only a count above it is over.
type State string

// The category states.
const (
	StateUnder State = "under the cap"
	StateAt    State = "at the cap"
	StateOver  State = "OVER the cap"
	// StateNotCounted is a category praetor has no reader for, or whose ledger could not be
	// read: a gate on it fails.
	StateNotCounted State = "not counted"
	// StateAbsent is a category a ledger of which is absent from this checkout, such as a CI
	// clone without the private .workingdir: its items are unknown here, not zero, and a gate
	// on it is skipped with that reason.
	StateAbsent State = "ledger not present"
)

// Item is one counted entry of a category.
type Item struct {
	// ID names the entry in its ledger: BUG-012, Q-003, or "task 4" for the number
	// `praetorctl state task complete` takes.
	ID    string
	Title string
	// Group is the file, area or section the batch lists the item under.
	Group string
	// Location is where the ledger says the item lives; empty when it records none.
	Location string
	// Note carries a short qualifier, such as a defect's severity.
	Note string
}

// Category is one capped category, counted or not.
type Category struct {
	Name string
	Cap  config.BacklogCap
	// MaxBy and ActionBy name the policy layers that set the cap's max and action.
	MaxBy, ActionBy []string
	// Ledgers are the repository-relative files the category is counted from; empty when
	// praetor has no reader for it.
	Ledgers []string
	// Absent lists the Ledgers that do not exist in this checkout. A category with an absent
	// ledger is not counted: the ledger's items are unknown, never zero.
	Absent []string
	// Counted is false for a category praetor did not count; Reason says why: it has no
	// reader, a ledger is absent (Absent), or a ledger could not be read.
	Counted bool
	Reason  string
	Items   []Item
	// Findings are entries the count could not classify, such as a bug row without a kind.
	Findings []string
}

// Count is the number of counted items.
func (c *Category) Count() int { return len(c.Items) }

// State places the count against the cap.
func (c *Category) State() State {
	switch {
	case len(c.Absent) > 0:
		return StateAbsent
	case !c.Counted:
		return StateNotCounted
	case c.Cap.Over(c.Count()):
		return StateOver
	case c.Count() == c.Cap.Limit():
		return StateAt
	}
	return StateUnder
}

// Line is the one-line status of the category that `state status` and the audit print.
func (c *Category) Line() string {
	origin := fmt.Sprintf("max %d set by %s, action %s set by %s", c.Cap.Limit(), config.BacklogOrigin(c.MaxBy),
		c.Cap.EffectiveAction(), config.BacklogOrigin(c.ActionBy))
	if !c.Counted {
		return fmt.Sprintf("%s: not counted: %s (%s)", c.Name, c.Reason, origin)
	}
	return fmt.Sprintf("%s: %d of %d, %s (%s)", c.Name, c.Count(), c.Cap.Limit(), c.State(), origin)
}

// Gated reports whether the category's effective action is gate.
func (c *Category) Gated() bool { return c.Cap.EffectiveAction() == config.BacklogGate }

// Report is every capped category of one repository, in config.BacklogCategories order.
type Report struct {
	Categories []Category
}

// Gate returns an error naming every category whose action is gate and that is over its cap
// or cannot be counted, because praetor has no reader for it or a ledger could not be read: a
// gate that cannot count is not a passing gate. A gated category whose ledger is absent from
// this checkout is not failed here; Skipped names it. Nil means none failed.
func (r *Report) Gate() error {
	var failures []error
	for i := 0; r != nil && i < len(r.Categories); i++ {
		category := &r.Categories[i]
		if !category.Gated() {
			continue
		}
		switch category.State() {
		case StateNotCounted:
			failures = append(failures, fmt.Errorf("backlog cap %s has action gate but cannot be counted: %s",
				category.Name, category.Reason))
		case StateOver:
			failures = append(failures, fmt.Errorf("backlog cap %s: %d items, over the cap of %d (action gate, max set by %s)",
				category.Name, category.Count(), category.Cap.Limit(), config.BacklogOrigin(category.MaxBy)))
		}
	}
	return errors.Join(failures...)
}

// Gated counts the categories whose action is gate.
func (r *Report) Gated() int {
	gated := 0
	for i := 0; r != nil && i < len(r.Categories); i++ {
		if r.Categories[i].Gated() {
			gated++
		}
	}
	return gated
}

// Skipped names every category whose action is gate but that was not counted because a ledger
// is absent from this checkout: its gate did not run, so it neither passes nor fails.
func (r *Report) Skipped() []string {
	var skipped []string
	for i := 0; r != nil && i < len(r.Categories); i++ {
		if r.Categories[i].Gated() && r.Categories[i].State() == StateAbsent {
			skipped = append(skipped, r.Categories[i].Name)
		}
	}
	return skipped
}

// counter reads one category from its ledgers. A counter without count is a category praetor
// cannot count yet; reason says why.
type counter struct {
	ledgers []string
	count   func(ctx context.Context, root string) ([]Item, []string, error)
	reason  string
}

// The task ledgers: OPEN.md holds the in-flight tasks, BACKLOG.md the deferred workstreams.
const (
	openLedger    = ".workingdir/OPEN.md"
	backlogLedger = ".workingdir/BACKLOG.md"
)

// counters maps each category to its reader. Tasks are every open item of the state ledger,
// so they are counted from OPEN.md and BACKLOG.md alike. Forge alerts have no reader:
// internal/forge reads no code-scanning, dependency or secret-scanning alert, so the category
// is reported as not counted rather than as zero.
var counters = map[string]counter{
	config.BacklogDefects:   {ledgers: []string{".workingdir/BUGS.md"}, count: countDefects},
	config.BacklogTasks:     {ledgers: []string{openLedger, backlogLedger}, count: countTasks},
	config.BacklogQuestions: {ledgers: []string{".workingdir/QUESTIONS.md"}, count: countQuestions},
	config.BacklogForgeAlerts: {reason: "praetor has no forge alert reader yet; internal/forge reads no " +
		"code-scanning, dependency or secret-scanning alerts"},
}

// Evaluate counts every category policy caps, reading the ledgers under root. A nil policy,
// or one that caps nothing, yields an empty report. A ledger that is absent or cannot be read
// leaves its category not counted with the reason, so one unreadable ledger never hides the
// other categories; the error is only for a category without a registered counter.
func Evaluate(ctx context.Context, root string, policy *config.EffectivePolicy) (*Report, error) {
	if ctx == nil || root == "" {
		return nil, errors.New("backlog cap evaluation requires a context and a repository root")
	}
	report := &Report{}
	if policy == nil {
		return report, nil
	}
	ctx, cancel := context.WithTimeout(ctx, evaluateTimeout)
	defer cancel()
	names := config.BacklogCategories()
	for i, entry := range policy.Policy.Backlog.Caps() {
		if !entry.Declared() {
			continue
		}
		reader, ok := counters[names[i]]
		if !ok {
			return nil, fmt.Errorf("count backlog category %s: no counter is registered for it", names[i])
		}
		category := evaluateCategory(ctx, root, names[i], entry, reader)
		category.MaxBy, category.ActionBy = policy.BacklogSources(names[i])
		report.Categories = append(report.Categories, category)
	}
	return report, nil
}

// evaluateCategory counts one category with its reader. A category it cannot count carries
// the reason instead of an error, so the caller reports it beside the others.
func evaluateCategory(ctx context.Context, root, name string, entry config.BacklogCap, reader counter) Category {
	category := Category{Name: name, Cap: entry, Ledgers: slices.Clone(reader.ledgers), Reason: reader.reason}
	if reader.count == nil {
		return category
	}
	absent, err := absentLedgers(root, reader.ledgers)
	if err != nil {
		category.Reason = err.Error()
		return category
	}
	if len(absent) > 0 {
		category.Absent, category.Reason = absent, absentReason(absent)
		return category
	}
	items, findings, err := reader.count(ctx, root)
	if err == nil && len(items) > maxItems {
		err = fmt.Errorf("%s together hold more than %d open items, the bound one count reads",
			strings.Join(reader.ledgers, " and "), maxItems)
	}
	if err != nil {
		category.Reason = err.Error()
		return category
	}
	category.Counted, category.Items, category.Findings = true, items, findings
	return category
}

// absentLedgers lists the ledgers that do not exist under root.
func absentLedgers(root string, ledgers []string) ([]string, error) {
	var absent []string
	for _, ledger := range ledgers {
		present, err := ledgerPresent(root, ledger)
		if err != nil {
			return nil, err
		}
		if !present {
			absent = append(absent, ledger)
		}
	}
	return absent, nil
}

// absentReason says which ledgers this checkout lacks.
func absentReason(absent []string) string {
	if len(absent) == 1 {
		return absent[0] + " is not present in this checkout"
	}
	return strings.Join(absent, " and ") + " are not present in this checkout"
}

// ledgerPresent reports whether the ledger file exists. A ledger reader treats a missing file
// as empty, so the category is not counted when one is missing rather than counted as zero.
func ledgerPresent(root, rel string) (bool, error) {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// countDefects counts the unresolved bug rows that are not scope. A row without a kind is
// counted as a defect, so a cap cannot be evaded by leaving rows unlabelled, and is reported as
// a finding until it is labelled.
func countDefects(ctx context.Context, root string) ([]Item, []string, error) {
	bugs, err := state.ListBugsContext(ctx, root, "all")
	if err != nil {
		return nil, nil, err
	}
	var items []Item
	var findings []string
	for i := 0; i < len(bugs); i++ {
		bug := bugs[i]
		if !bug.Unresolved() || bug.Kind == state.BugKindScope {
			continue
		}
		if bug.Kind == "" {
			findings = append(findings, fmt.Sprintf("%s has no kind and is counted as a defect; label it with "+
				"`praetorctl state bug kind %s defect` (or scope)", bug.ID, bug.ID))
		}
		items, err = appendBounded(items, Item{ID: bug.ID, Title: bug.Title, Group: locationGroup(bug.Location),
			Location: bug.Location, Note: bug.Severity}, "BUGS.md", "open defect rows")
		if err != nil {
			return nil, nil, err
		}
	}
	return items, findings, nil
}

// appendBounded appends entry to list, failing naming the bound once list already holds
// maxItems entries: a ledger above the bound fails the count instead of dropping the rows
// after it and counting low. what names the entries in the error.
func appendBounded[T any](list []T, entry T, ledger, what string) ([]T, error) {
	if len(list) >= maxItems {
		return nil, fmt.Errorf("%s holds more than %d %s, the bound one count reads", ledger, maxItems, what)
	}
	return append(list, entry), nil
}

// lineSuffix matches the ":<line>" or ":<line>-<line>" a location ends in.
var lineSuffix = regexp.MustCompile(`:\d+(-\d+)?$`)

// locationGroup is the file a location names, without its line, or the location itself when
// it names an area rather than a file.
func locationGroup(location string) string {
	group := lineSuffix.ReplaceAllString(strings.TrimSpace(location), "")
	if group == "" {
		return "(no location)"
	}
	return group
}

// countTasks counts the open items of the state ledger: the pending rows of OPEN.md, the rows
// `praetorctl state task list` numbers, then the pending rows of BACKLOG.md. Each is grouped by
// its ledger and the section heading above it.
func countTasks(ctx context.Context, root string) ([]Item, []string, error) {
	open, err := state.ListTasksContext(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	backlog, err := state.ListBacklogTasksContext(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	items, err := pendingTasks(open, "OPEN.md", func(task state.TaskItem) string { return fmt.Sprintf("task %d", task.Index) })
	if err != nil {
		return nil, nil, err
	}
	deferred, err := pendingTasks(backlog, "BACKLOG.md", backlogTaskID)
	if err != nil {
		return nil, nil, err
	}
	findings, err := dischargedFindings(backlog)
	if err != nil {
		return nil, nil, err
	}
	return append(items, deferred...), findings, nil
}

// backlogTaskID names a BACKLOG.md row by its line; no command numbers BACKLOG.md rows.
func backlogTaskID(task state.TaskItem) string { return fmt.Sprintf("BACKLOG.md:%d", task.Line) }

// pendingTasks turns the pending rows of one task ledger into items, each named by id and
// grouped as "<ledger>: <section>". Every row is examined, completed ones first or not; more
// than maxItems pending rows fail naming the bound.
func pendingTasks(tasks []state.TaskItem, ledger string, id func(state.TaskItem) string) ([]Item, error) {
	var items []Item
	for i := 0; i < len(tasks); i++ {
		if tasks[i].Completed {
			continue
		}
		section := tasks[i].Section
		if section == "" {
			section = "(no section)"
		}
		var err error
		items, err = appendBounded(items, Item{ID: id(tasks[i]), Title: tasks[i].Description, Group: ledger + ": " + section},
			ledger, "pending rows")
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

// dischargedSection opens the heading of every BACKLOG.md section that records finished work:
// the seeded "Discharged Milestones" and each "Discharged Tasks [...]" `state task archive`
// appends.
const dischargedSection = "Discharged"

// dischargedFindings reports each pending BACKLOG.md row under a discharged heading. Archiving
// writes only completed rows there, so a pending one was appended after the last archive or
// never finished; it is counted like any other open item and named so it can be moved.
func dischargedFindings(backlog []state.TaskItem) ([]string, error) {
	var findings []string
	for i := 0; i < len(backlog); i++ {
		task := backlog[i]
		if task.Completed || !strings.HasPrefix(task.Section, dischargedSection) {
			continue
		}
		var err error
		findings, err = appendBounded(findings, fmt.Sprintf("%s is pending under the discharged heading %q and is counted; "+
			"move it under a workstream heading or mark it done", backlogTaskID(task), task.Section),
			"BACKLOG.md", "pending rows under a discharged heading")
		if err != nil {
			return nil, err
		}
	}
	return findings, nil
}

// countQuestions counts the pending questions; QUESTIONS.md records no area, so they form one
// group.
func countQuestions(ctx context.Context, root string) ([]Item, []string, error) {
	questions, err := state.ListQuestionsContext(ctx, root, "pending")
	if err != nil {
		return nil, nil, err
	}
	var items []Item
	for i := 0; i < len(questions); i++ {
		items, err = appendBounded(items, Item{ID: questions[i].ID, Title: questions[i].Question, Group: "pending questions"},
			"QUESTIONS.md", "pending questions")
		if err != nil {
			return nil, nil, err
		}
	}
	return items, nil, nil
}
