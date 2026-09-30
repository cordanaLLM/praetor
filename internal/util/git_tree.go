// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"fmt"
	"strconv"
	"strings"
)

// Field counts of a `git ls-tree` record before its tab: mode, type and object, plus the size
// column that --long (-l) adds.
const (
	gitTreeShortFields = 3
	gitTreeLongFields  = 4
)

// MinGitTreeRecordBytes is the shortest record of a `git ls-tree -z` listing of SHA-1 objects:
// mode, type, a 40-digit object, a one-byte path, their separators and the NUL. A listing read
// under a byte bound holds at most that bound divided by it records, the maxEntries a caller
// passes ParseGitTreeListing.
const MinGitTreeRecordBytes = 55

// GitTreeEntry is one record of a `git ls-tree -z` listing: "<mode> <type> <object>\t<path>", or
// with --long (-l) "<mode> <type> <object> <size>\t<path>". The path is taken verbatim, as -z
// leaves it unquoted.
type GitTreeEntry struct {
	Mode   string
	Type   string
	Object string
	Path   string
	// Size is the blob's byte count from a --long listing; -1 without the size column and for an
	// entry git prints no size for ("-": a tree or a submodule commit).
	Size int64
}

// RegularBlob reports whether the entry is a regular file, executable or not: never a symlink,
// a submodule or a tree.
func (e GitTreeEntry) RegularBlob() bool {
	return e.Type == "blob" && (e.Mode == "100644" || e.Mode == "100755")
}

// ParseGitTreeEntry parses one record of a `git ls-tree -z` listing; long says whether the
// listing ran with --long, whose records carry the size column. A record without a tab, with the
// wrong field count, an empty path or a size that is neither "-" nor a non-negative count is
// refused, never read as a partial entry.
func ParseGitTreeEntry(record string, long bool) (GitTreeEntry, error) {
	meta, path, found := strings.Cut(record, "\t")
	fields := strings.Fields(meta)
	want := gitTreeShortFields
	if long {
		want = gitTreeLongFields
	}
	if !found || path == "" || len(fields) != want {
		return GitTreeEntry{}, fmt.Errorf("unexpected ls-tree record %q", record)
	}
	entry := GitTreeEntry{Mode: fields[0], Type: fields[1], Object: fields[2], Path: path, Size: -1}
	if !long || fields[3] == "-" {
		return entry, nil
	}
	size, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil || size < 0 {
		return GitTreeEntry{}, fmt.Errorf("unexpected ls-tree object size in %q", record)
	}
	entry.Size = size
	return entry, nil
}

// ParseGitTreeListing parses every record of a `git ls-tree -z` listing (ParseGitTreeEntry). The
// empty record after the final NUL is skipped; more than maxEntries records (HISS-02) or any
// record that does not parse fails the whole listing.
func ParseGitTreeListing(out []byte, long bool, maxEntries int) ([]GitTreeEntry, error) {
	if len(out) == 0 {
		return nil, nil
	}
	records := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(records) > maxEntries {
		return nil, fmt.Errorf("ls-tree listing holds %d records, more than the %d one read accepts", len(records), maxEntries)
	}
	entries := make([]GitTreeEntry, 0, len(records))
	for i := 0; i < len(records); i++ {
		entry, err := ParseGitTreeEntry(records[i], long)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
