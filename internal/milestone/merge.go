package milestone

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// remoteMergePlan maps every fetched remote milestone onto the local row it updates,
// computed before anything is mutated so that an over-capacity merge fails with the store
// untouched.
type remoteMergePlan struct {
	// targets holds, per remote, the index of the local row it updates. An index at or
	// beyond the current store length names a row the merge appends, in order.
	targets []int
	// appended counts the rows the merge adds.
	appended int
	// stale lists local rows whose forge binding duplicates another row's binding.
	stale []StaleBinding
	// staleIdx holds the store index of each stale row, parallel to stale.
	staleIdx []int
}

// mergeRemoteMilestones folds the forge listing into the local store. A local row bound to
// a remote milestone (RemoteNumber > 0) is matched by that number, so a remote rename
// updates the bound row and a title swap between two remotes keeps both bindings. Only an
// unbound row is matched by title, which binds it. A remote matching neither becomes a
// new local row with a fresh local number.
func mergeRemoteMilestones(store *MilestoneStore, remotes []RemoteMilestone) (*SyncResult, error) {
	if len(store.Milestones) > MaxMilestonesLimit || len(remotes) > MaxMilestonesLimit {
		return nil, fmt.Errorf("milestone merge input exceeds maximum of %d entries", MaxMilestonesLimit)
	}
	plan := planRemoteMerge(store.Milestones, remotes)
	if len(store.Milestones)+plan.appended > MaxMilestonesLimit {
		return nil, fmt.Errorf("merged milestone store exceeds maximum of %d entries", MaxMilestonesLimit)
	}

	result := &SyncResult{StaleBindings: plan.stale}
	for _, idx := range plan.staleIdx {
		store.Milestones[idx].RemoteNumber = 0
	}
	nextNum := nextLocalNumber(store.Milestones)
	now := time.Now().UTC()
	for i := 0; i < len(remotes) && i < MaxMilestonesLimit; i++ {
		target := plan.targets[i]
		if target >= len(store.Milestones) {
			store.Milestones = append(store.Milestones, Milestone{Number: nextNum, CreatedAt: now})
			nextNum++
		}
		m := &store.Milestones[target]
		if applyRemote(m, remotes[i], sanitizeTitle(remotes[i].Title)) && !slices.Contains(result.PendingCloses, m.Number) {
			result.PendingCloses = append(result.PendingCloses, m.Number)
		}
	}
	return result, nil
}

// planRemoteMerge resolves the local row for every remote without mutating the store.
func planRemoteMerge(local []Milestone, remotes []RemoteMilestone) remoteMergePlan {
	plan := remoteMergePlan{targets: make([]int, len(remotes))}
	remoteTitles := make(map[int]string, len(remotes))
	for i := 0; i < len(remotes) && i < MaxMilestonesLimit; i++ {
		remoteTitles[remotes[i].Number] = sanitizeTitle(remotes[i].Title)
	}
	byRemote, byTitle := indexLocalRows(local, remoteTitles, &plan)

	for i := 0; i < len(remotes) && i < MaxMilestonesLimit; i++ {
		number := remotes[i].Number
		if idx, ok := byRemote[number]; ok {
			plan.targets[i] = idx
			continue
		}
		key := strings.ToLower(sanitizeTitle(remotes[i].Title))
		idx, ok := byTitle[key]
		if ok {
			delete(byTitle, key)
		} else {
			idx = len(local) + plan.appended
			plan.appended++
		}
		plan.targets[i] = idx
		if number > 0 {
			byRemote[number] = idx
		}
	}
	return plan
}

// indexLocalRows indexes bound rows by remote number and unbound rows by lower-cased
// title. When two rows share a remote number, the row whose title matches the remote's
// current title keeps the binding (the earlier row on a tie) and the other is recorded as
// stale in plan.
func indexLocalRows(local []Milestone, remoteTitles map[int]string, plan *remoteMergePlan) (map[int]int, map[string]int) {
	byRemote := make(map[int]int, len(local))
	byTitle := make(map[string]int, len(local))
	for i := 0; i < len(local) && i < MaxMilestonesLimit; i++ {
		number := local[i].RemoteNumber
		if number <= 0 {
			byTitle[strings.ToLower(local[i].Title)] = i
			continue
		}
		kept, bound := byRemote[number]
		if !bound {
			byRemote[number] = i
			continue
		}
		stale := i
		if titleMatches(local[i], remoteTitles, number) && !titleMatches(local[kept], remoteTitles, number) {
			byRemote[number], stale = i, kept
		}
		plan.stale = append(plan.stale, StaleBinding{Local: local[stale].Number, Remote: number, Kept: local[byRemote[number]].Number})
		plan.staleIdx = append(plan.staleIdx, stale)
	}
	return byRemote, byTitle
}

// titleMatches reports whether the local row carries the remote milestone's current title.
func titleMatches(m Milestone, remoteTitles map[int]string, number int) bool {
	title, ok := remoteTitles[number]
	return ok && strings.EqualFold(m.Title, title)
}

// nextLocalNumber returns the next unused local milestone number.
func nextLocalNumber(milestones []Milestone) int {
	next := 1
	for i := 0; i < len(milestones) && i < MaxMilestonesLimit; i++ {
		if milestones[i].Number >= next {
			next = milestones[i].Number + 1
		}
	}
	return next
}
