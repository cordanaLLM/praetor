package state

import (
	"fmt"
	"strings"
)

// stateTimeLayout is the timestamp inside every STATE.md entry heading.
const stateTimeLayout = "2006-01-02 15:04:05 UTC"

// stateEntry is one STATE.md log entry. The heading keeps its structured
// `### [time]` shape; the body is one compact line an agent reads at a glance.
type stateEntry struct {
	Time, Head, Branch, Activity string
	// Tasks holds open and completed counts; nil for entries written before tasks were counted.
	Tasks           *[2]int
	Bugs, Questions int
	// Git is nil for entries written before Git state was recorded.
	Git *stateGitEntry
}

type stateGitEntry struct {
	State string
	Clean bool
	Dirty int
}

// activityShortForms maps the engine's own automated activity texts to their
// compact form. Operator-written activity text is kept as written.
var activityShortForms = map[string]string{
	"":                                "sync",
	"Automated state synchronization": "sync",
	"Git post-commit synchronization": "post-commit sync",
}

func entryFromSnapshot(snap *StateSnapshot, summary string) stateEntry {
	return stateEntry{
		Time: snap.LastUpdated.Format(stateTimeLayout), Head: snap.HeadSHA, Branch: snap.Branch,
		Activity: summary, Tasks: &[2]int{snap.OpenTasks, snap.CompletedTasks},
		Bugs: snap.OpenBugs, Questions: snap.PendingQs,
		Git: &stateGitEntry{State: snap.GitState, Clean: snap.Clean, Dirty: snap.DirtyCount},
	}
}

// compactActivity keeps an activity on one line, so free text can never forge
// an entry heading or a sync marker, and shortens the engine's automated texts.
func compactActivity(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if short, ok := activityShortForms[text]; ok {
		return short
	}
	return text
}

// render writes the entry as a heading plus one line, each newline-terminated.
func (e stateEntry) render() string {
	var line strings.Builder
	line.WriteString("- " + compactActivity(e.Activity))
	if e.Tasks != nil {
		fmt.Fprintf(&line, " | tasks %d open %d done", e.Tasks[0], e.Tasks[1])
	}
	fmt.Fprintf(&line, " | bugs %d open | qs %d pending", e.Bugs, e.Questions)
	if e.Git != nil {
		fmt.Fprintf(&line, " | git %s %s", e.Git.State, e.Git.tree())
	}
	return fmt.Sprintf("### [%s] `%s` on `%s`\n%s\n", e.Time, e.Head, e.Branch, line.String())
}

func (g stateGitEntry) tree() string {
	if g.Clean {
		return "clean"
	}
	return fmt.Sprintf("dirty %d", g.Dirty)
}
