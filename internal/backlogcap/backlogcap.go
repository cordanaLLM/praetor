// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package backlogcap counts each capped backlog category against the cap the effective policy
// resolved for it (#792): `praetorctl state status` prints the counts, `praetorctl state batch`
// writes one batch file per category over its cap, and `praetorctl audit` fails on a gated one.
//
// The caps are policy keys (internal/config, backlog_caps.go); the counts come from the ledger
// readers internal/state already has. A category praetor has no reader for is reported as not
// counted with the reason, never as an empty category.
package backlogcap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/state"
)

const (
	// evaluateTimeout bounds one evaluation's ledger reads (HISS-02).
	evaluateTimeout = 30 * time.Second
	// maxItems bounds one category's items; no ledger reader returns more (HISS-02).
	maxItems = 10000
)

// State is where a category's count stands against its cap. The cap is inclusive: a count
// equal to max is at the cap and passes; only a count above it is over.
type State string

// The category states.
const (
	StateUnder      State = "under the cap"
	StateAt         State = "at the cap"
	StateOver       State = "OVER the cap"
	StateNotCounted State = "not counted"
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
	// Ledger is the repository-relative file the category is counted from; empty when praetor
	// has no reader for it.
	Ledger string
	// Present reports whether Ledger exists. An absent ledger holds no items.
	Present bool
	// Counted is false for a category praetor has no reader for; Reason says why.
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
	absent := ""
	if !c.Present {
		absent = fmt.Sprintf("; %s is absent, so it holds no item", c.Ledger)
	}
	return fmt.Sprintf("%s: %d of %d, %s (%s%s)", c.Name, c.Count(), c.Cap.Limit(), c.State(), origin, absent)
}

// Report is every capped category of one repository, in config.BacklogCategories order.
type Report struct {
	Categories []Category
}

// Gate returns an error naming every category whose action is gate and that is over its cap
// or cannot be counted: a gate that cannot count is not a passing gate. Nil means none.
func (r *Report) Gate() error {
	var failures []error
	for i := 0; r != nil && i < len(r.Categories); i++ {
		category := &r.Categories[i]
		if category.Cap.EffectiveAction() != config.BacklogGate {
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

// counter reads one category. A counter without count is a category praetor cannot count
// yet; reason says why.
type counter struct {
	ledger string
	count  func(ctx context.Context, root string) ([]Item, []string, error)
	reason string
}

// counters maps each category to its reader. Forge alerts have none: internal/forge reads no
// code-scanning, dependency or secret-scanning alert, so the category is reported as not
// counted rather than as zero.
var counters = map[string]counter{
	config.BacklogDefects:   {ledger: ".workingdir/BUGS.md", count: countDefects},
	config.BacklogTasks:     {ledger: ".workingdir/OPEN.md", count: countTasks},
	config.BacklogQuestions: {ledger: ".workingdir/QUESTIONS.md", count: countQuestions},
	config.BacklogForgeAlerts: {reason: "praetor has no forge alert reader yet; internal/forge reads no " +
		"code-scanning, dependency or secret-scanning alerts"},
}

// Evaluate counts every category policy caps, reading the ledgers under root. A nil policy,
// or one that caps nothing, yields an empty report.
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
		category, err := evaluateCategory(ctx, root, names[i], entry)
		if err != nil {
			return nil, fmt.Errorf("count backlog category %s: %w", names[i], err)
		}
		category.MaxBy, category.ActionBy = policy.BacklogSources(names[i])
		report.Categories = append(report.Categories, category)
	}
	return report, nil
}

func evaluateCategory(ctx context.Context, root, name string, entry config.BacklogCap) (Category, error) {
	reader, ok := counters[name]
	if !ok {
		return Category{}, fmt.Errorf("no counter is registered for category %q", name)
	}
	category := Category{Name: name, Cap: entry, Ledger: reader.ledger, Reason: reader.reason}
	if reader.count == nil {
		return category, nil
	}
	present, err := ledgerPresent(root, reader.ledger)
	if err != nil {
		return category, err
	}
	items, findings, err := reader.count(ctx, root)
	if err != nil {
		return category, err
	}
	if len(items) > maxItems {
		return category, fmt.Errorf("category holds more than %d items", maxItems)
	}
	category.Present, category.Counted, category.Items, category.Findings = present, true, items, findings
	return category, nil
}

// ledgerPresent reports whether the ledger file exists. A ledger reader treats a missing file
// as empty; the report says which it was.
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
	for i := 0; i < len(bugs) && i <= maxItems; i++ {
		bug := bugs[i]
		if !bug.Unresolved() || bug.Kind == state.BugKindScope {
			continue
		}
		if bug.Kind == "" {
			findings = append(findings, fmt.Sprintf("%s has no kind and is counted as a defect; label it with "+
				"`praetorctl state bug kind %s defect` (or scope)", bug.ID, bug.ID))
		}
		items = append(items, Item{ID: bug.ID, Title: bug.Title, Group: locationGroup(bug.Location),
			Location: bug.Location, Note: bug.Severity})
	}
	return items, findings, nil
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

// countTasks counts the pending rows of OPEN.md, the rows `praetorctl state task list` reads,
// grouped by the section heading above each.
func countTasks(ctx context.Context, root string) ([]Item, []string, error) {
	tasks, err := state.ListTasksContext(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	var items []Item
	for i := 0; i < len(tasks) && i <= maxItems; i++ {
		if tasks[i].Completed {
			continue
		}
		group := tasks[i].Section
		if group == "" {
			group = "(no section)"
		}
		items = append(items, Item{ID: fmt.Sprintf("task %d", tasks[i].Index), Title: tasks[i].Description, Group: group})
	}
	return items, nil, nil
}

// countQuestions counts the pending questions; QUESTIONS.md records no area, so they form one
// group.
func countQuestions(ctx context.Context, root string) ([]Item, []string, error) {
	questions, err := state.ListQuestionsContext(ctx, root, "pending")
	if err != nil {
		return nil, nil, err
	}
	items := make([]Item, 0, len(questions))
	for i := 0; i < len(questions) && i <= maxItems; i++ {
		items = append(items, Item{ID: questions[i].ID, Title: questions[i].Question, Group: "pending questions"})
	}
	return items, nil, nil
}
