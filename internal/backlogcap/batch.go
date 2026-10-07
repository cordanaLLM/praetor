// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package backlogcap

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/bugledger"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// BatchDir is the repository-relative directory batch files are written to. It lies inside the
// private, Git-ignored .workingdir.
const BatchDir = ".workingdir/batches"

// batchFileMode keeps a batch as private as the ledger it lists.
const batchFileMode = 0o600

// recheckers re-read each item of a category against the tree before it is batched, because
// a stale ledger counts items that are already fixed (#158). A category without one marks
// every item not re-checked and still lists it.
var recheckers = map[string]func(root string, item Item) string{
	config.BacklogDefects: recheckLocation,
}

// recheckLocation is the bug ledger's own location re-check (bugledger.CheckLocation).
func recheckLocation(root string, item Item) string {
	check := bugledger.CheckLocation(root, item.Location)
	switch {
	case !check.Checked:
		return "not re-checked: the location is not a Go file:line"
	case check.Stale:
		return "the recorded location no longer resolves: " + check.Detail
	}
	return "location resolves"
}

// BatchPath is the repository-relative path of one category's batch for date.
func BatchPath(category, date string) string {
	return path.Join(BatchDir, category+"-"+date+".md")
}

// WriteBatches writes one batch file for every counted category that is over its cap and
// whose action includes batch (batch or gate), and returns their repository-relative paths in
// category order. date (YYYY-MM-DD) names the file and heads it; the caller passes it, so the
// output is deterministic. A file of the same name is replaced.
func WriteBatches(ctx context.Context, root string, report *Report, date string) ([]string, error) {
	if ctx == nil || root == "" || report == nil {
		return nil, errors.New("writing backlog batches requires a context, a repository root and a report")
	}
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return nil, fmt.Errorf("batch date %q must be YYYY-MM-DD", date)
	}
	var written []string
	for i := 0; i < len(report.Categories); i++ {
		category := &report.Categories[i]
		if category.State() != StateOver || !category.Cap.EffectiveAction().Includes(config.BacklogBatch) {
			continue
		}
		rel := BatchPath(category.Name, date)
		data := []byte(renderBatch(root, category, date))
		if err := contextopt.WriteSnapshotIn(ctx, root, filepath.FromSlash(rel), data, batchFileMode); err != nil {
			return written, fmt.Errorf("write %s: %w", rel, err)
		}
		written = append(written, rel)
	}
	return written, nil
}

// batchGroup is one heading of a batch: the items that share a file, area or section.
type batchGroup struct {
	name  string
	items []Item
}

// groupItems groups items by Group, groups sorted by name and items kept in ledger order.
func groupItems(items []Item) []batchGroup {
	index := map[string]int{}
	var groups []batchGroup
	for i := 0; i < len(items) && i < maxItems; i++ {
		position, ok := index[items[i].Group]
		if !ok {
			position = len(groups)
			index[items[i].Group] = position
			groups = append(groups, batchGroup{name: items[i].Group})
		}
		groups[position].items = append(groups[position].items, items[i])
	}
	slices.SortStableFunc(groups, func(a, b batchGroup) int { return strings.Compare(a.name, b.name) })
	return groups
}

// renderBatch writes the batch: a header naming the count, the cap and where both came from,
// then every item under its group with its re-check verdict, then the findings.
func renderBatch(root string, category *Category, date string) string {
	var text strings.Builder
	fmt.Fprintf(&text, "# Backlog batch: %s, %s\n\n", category.Name, date)
	fmt.Fprintf(&text, "`%s` holds %d items in `%s`, over its cap of %d (max set by %s, action %s set by %s). "+
		"The cap is inclusive: a count of %d is at the cap and passes.\n\n",
		category.Name, category.Count(), strings.Join(category.Ledgers, "` and `"), category.Cap.Limit(), config.BacklogOrigin(category.MaxBy),
		category.Cap.EffectiveAction(), config.BacklogOrigin(category.ActionBy), category.Cap.Limit())
	text.WriteString("Every counted item is listed once, grouped by file, area or section. Before this batch was " +
		"written each item was re-checked against the tree where a resolver exists; an item without one is " +
		"marked not re-checked.\n")
	recheck := recheckers[category.Name]
	for _, group := range groupItems(category.Items) {
		fmt.Fprintf(&text, "\n## %s (%d)\n\n", group.name, len(group.items))
		for _, item := range group.items {
			text.WriteString(renderItem(root, item, recheck) + "\n")
		}
	}
	if len(category.Findings) > 0 {
		text.WriteString("\n## Findings\n\n")
		for _, finding := range category.Findings {
			text.WriteString("- " + finding + "\n")
		}
	}
	return text.String()
}

// renderItem is one list entry: ID, qualifier, title, location and re-check verdict.
func renderItem(root string, item Item, recheck func(string, Item) string) string {
	entry := "- `" + item.ID + "`"
	if item.Note != "" {
		entry += " (" + item.Note + ")"
	}
	entry += " " + item.Title
	if item.Location != "" {
		entry += "; location `" + item.Location + "`"
	}
	verdict := "not re-checked: no resolver for this category"
	if recheck != nil {
		verdict = recheck(root, item)
	}
	return entry + "; re-check: " + verdict
}
