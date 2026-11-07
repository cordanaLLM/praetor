package prwait

import (
	"fmt"
	"strconv"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// classify evaluates pull request snapshots to determine wait termination status and result items.
func classify(repo string, snapshots []forge.ReviewSnapshot, seen map[string]bool, initial bool, now time.Time) Result {
	var reviews, closed, checks []string
	terminal := true
	for _, snapshot := range snapshots {
		prefix := fmt.Sprintf("%s#%d", repo, snapshot.Number)
		if snapshot.State == "CLOSED" {
			closed = append(closed, "closed "+prefix)
			continue
		}
		if snapshot.State == "MERGED" {
			closed = append(closed, "merged "+prefix)
			continue
		}
		reviews = append(reviews, reviewItems(prefix, snapshot, seen, initial, now)...)
		terminal = terminal && snapshot.ChecksTerminal
		checks = append(checks, "checks "+prefix+": "+snapshot.ChecksConclusion)
	}
	switch {
	case len(reviews) > 0:
		return Result{Code: 10, Items: reviews}
	case len(closed) > 0:
		return Result{Code: 11, Items: closed}
	case terminal:
		if len(checks) == 0 {
			checks = []string{"checks " + repo + ": no own open pull requests"}
		}
		return Result{Code: 0, Items: checks}
	default:
		return Result{Code: -1}
	}
}

// reviewItems extracts unresolved threads and newly submitted review items for a pull request snapshot.
func reviewItems(prefix string, snapshot forge.ReviewSnapshot, seen map[string]bool, initial bool, now time.Time) []string {
	var items []string
	for _, thread := range snapshot.Threads {
		if thread.Resolved {
			continue
		}
		kind := "new thread"
		if initial {
			kind = "unresolved thread"
		}
		items = append(items, fmt.Sprintf("review %s: %s %s:%d by %s (%s)",
			prefix, kind, thread.Path, thread.Line, thread.Author, forge.ReviewAge(now, thread.CreatedAt)))
	}
	for _, review := range snapshot.Reviews {
		key := prefix + "/" + review.ID + "/" + review.State + "/" + strconv.FormatInt(review.SubmittedAt.UnixNano(), 10)
		if !initial && !seen[key] {
			items = append(items, fmt.Sprintf("review %s: new review %s by %s", prefix, review.State, review.Author))
		}
		seen[key] = true
	}
	return items
}
