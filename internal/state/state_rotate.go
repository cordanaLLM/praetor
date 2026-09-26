package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// STATE.md history rotation bounds (HISS-02). Sync rewrites the log only once it crosses a
// trigger, and then keeps the newest entries within the keep bounds, so the rewrite happens
// once every stateRotateEntries-stateKeepEntries syncs rather than on every sync.
const (
	// stateRotateEntries and stateRotateBytes trigger a rotation when either is exceeded.
	stateRotateEntries = 200
	stateRotateBytes   = 256 << 10
	// stateKeepEntries and stateKeepBytes bound the entries a rotation keeps in STATE.md.
	stateKeepEntries = 100
	stateKeepBytes   = 128 << 10
	// maxStateEntryBytes bounds one rendered entry, so a single --log text can never
	// exceed the bound rotation keeps the log under.
	maxStateEntryBytes = 16 << 10
	// stateHistoryPrefix names the archive files in the working directory.
	stateHistoryPrefix = "STATE.history-"
	// stateHistoryStamp is the UTC timestamp in an archive name; it carries no ':' so
	// the name is valid on Windows too.
	stateHistoryStamp = "20060102T150405.000000000Z"
)

// stateRotation is the plan for one history rotation.
type stateRotation struct {
	// body is STATE.md before the new entry: the preamble and the kept entries.
	body string
	// archived is the verbatim run of the oldest entries moved out; empty when the log is
	// within both triggers.
	archived string
	// entries counts the archived entries.
	entries int
}

// planStateRotation splits body, STATE.md without its trailing sync marker, into what stays
// and what moves to the archive. Below both triggers it returns body unchanged. Only whole
// entries move, oldest first, as one contiguous run copied byte for byte; the preamble
// before the first entry always stays.
func planStateRotation(body string) (stateRotation, error) {
	starts, err := entryOffsets(body)
	if err != nil {
		return stateRotation{}, err
	}
	if len(starts) <= stateRotateEntries && len(body) <= stateRotateBytes {
		return stateRotation{body: body}, nil
	}
	first := firstKeptEntry(len(body), starts)
	if first == 0 {
		return stateRotation{body: body}, nil
	}
	end := len(body)
	if first < len(starts) {
		end = starts[first]
	}
	return stateRotation{body: body[:starts[0]] + body[end:], archived: body[starts[0]:end], entries: first}, nil
}

// entryOffsets returns the byte offset of every entry heading line in body.
func entryOffsets(body string) ([]int, error) {
	offsets := make([]int, 0, stateRotateEntries+1)
	pos := 0
	for lines := 0; pos < len(body); lines++ {
		if lines >= maxScannedLines {
			return nil, fmt.Errorf("STATE.md exceeds %d lines", maxScannedLines)
		}
		next := len(body)
		if end := strings.IndexByte(body[pos:], '\n'); end >= 0 {
			next = pos + end + 1
		}
		if isEntryHeading(body[pos:next]) {
			offsets = append(offsets, pos)
		}
		pos = next
	}
	return offsets, nil
}

// firstKeptEntry returns the index of the oldest entry a rotation keeps: the newest entries,
// at most stateKeepEntries of them, whose bytes up to the end of the log stay within
// stateKeepBytes. An entry larger than that bound on its own is archived rather than kept,
// so a rotation always brings the log back under its bounds.
func firstKeptEntry(size int, starts []int) int {
	first := len(starts)
	for i := len(starts) - 1; i >= 0 && len(starts)-i <= stateKeepEntries; i-- {
		if size-starts[i] > stateKeepBytes {
			break
		}
		first = i
	}
	return first
}

// archiveStateHistory publishes the rotated entries under a fresh name in the working
// directory and returns that name. The name is claimed exclusively: an existing file is
// never replaced, so archived history is only ever added, never overwritten or deleted.
func archiveStateHistory(ctx context.Context, rootPath, archived string, now time.Time) (name string, err error) {
	root, err := contextopt.OpenDirectoryIn(ctx, rootPath, WorkingDirName)
	if err != nil {
		return "", fmt.Errorf("open %s for the STATE.md history archive: %w", WorkingDirName, err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	name = stateHistoryPrefix + now.UTC().Format(stateHistoryStamp) + ".md"
	created, err := contextopt.CreateRootSnapshot(ctx, root, name, []byte(archived), 0o600)
	if err != nil {
		return "", fmt.Errorf("archive STATE.md history to %s: %w", name, err)
	}
	if !created {
		return "", fmt.Errorf("STATE.md history archive %s already exists; nothing was rotated", name)
	}
	return name, nil
}

// rotateStateHistory archives the oldest entries once the log crosses a trigger and
// returns the body the new entry is appended to. The archive is written before STATE.md is
// replaced: a failed replacement leaves the rotated entries in both files, never in
// neither. It records the rotation on snap and in the activity text of the new entry.
func rotateStateHistory(ctx context.Context, rootPath, body string, snap *StateSnapshot, summary string) (string, string, error) {
	rotation, err := planStateRotation(body)
	if err != nil || rotation.entries == 0 {
		return rotation.body, summary, err
	}
	name, err := archiveStateHistory(ctx, rootPath, rotation.archived, snap.LastUpdated)
	if err != nil {
		return "", "", err
	}
	snap.ArchivedEntries, snap.HistoryArchive = rotation.entries, name
	return rotation.body, fmt.Sprintf("%s; archived %d entries to %s", compactActivity(summary), rotation.entries, name), nil
}
