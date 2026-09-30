// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"strings"
	"testing"
)

// treeObject is a SHA-1 object name for the tree listing fixtures.
var treeObject = strings.Repeat("a", 40)

// Positive: short and --long records parse into mode, type, object, verbatim path and size; the
// size column is -1 without --long and for a tree or submodule; only regular blobs are regular.
func TestParseGitTreeListing_Positive_ShortAndLongRecords(t *testing.T) {
	long := "100644 blob " + treeObject + "      39\tdeploy/a b\tc.yaml\x00" +
		"100755 blob " + treeObject + "       7\trun.sh\x00" +
		"160000 commit " + treeObject + "       -\tvendor/sub\x00" +
		"120000 blob " + treeObject + "       4\tlink\x00"
	entries, err := ParseGitTreeListing([]byte(long), true, 4)
	if err != nil {
		t.Fatalf("long listing: %v", err)
	}
	want := []GitTreeEntry{
		{Mode: "100644", Type: "blob", Object: treeObject, Path: "deploy/a b\tc.yaml", Size: 39},
		{Mode: "100755", Type: "blob", Object: treeObject, Path: "run.sh", Size: 7},
		{Mode: "160000", Type: "commit", Object: treeObject, Path: "vendor/sub", Size: -1},
		{Mode: "120000", Type: "blob", Object: treeObject, Path: "link", Size: 4},
	}
	if len(entries) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, entries[i], want[i])
		}
	}
	regular := []bool{true, true, false, false}
	for i := range entries {
		if entries[i].RegularBlob() != regular[i] {
			t.Errorf("%s: RegularBlob() = %v, want %v", entries[i].Path, entries[i].RegularBlob(), regular[i])
		}
	}

	short, err := ParseGitTreeEntry("100644 blob "+treeObject+"\t.standards.yaml", false)
	if err != nil || short.Path != ".standards.yaml" || short.Size != -1 || !short.RegularBlob() {
		t.Errorf("short record = %+v, %v; want a regular blob without a size", short, err)
	}
}

// Negative: a record without a tab, with the other listing's field count, an empty path or a
// size that is not a count is refused, and one such record fails the whole listing.
func TestParseGitTreeListing_Negative_MalformedRecords(t *testing.T) {
	for _, tc := range []struct {
		record string
		long   bool
	}{
		{"100644 blob " + treeObject + " 39 no-tab.yaml", true},
		{"100644 blob " + treeObject + "\tshort-in-long.yaml", true},
		{"100644 blob " + treeObject + " 39\tlong-in-short.yaml", false},
		{"100644 blob " + treeObject + " big\ta", true},
		{"100644 blob " + treeObject + " -5\ta", true},
		{"100644 blob " + treeObject + " 1\t", true},
		{"garbage", false},
	} {
		if entry, err := ParseGitTreeEntry(tc.record, tc.long); err == nil {
			t.Errorf("accepted malformed record %q (long %v) as %+v", tc.record, tc.long, entry)
		}
	}
	listing := "100644 blob " + treeObject + " 1\tok\x00garbage\x00"
	if entries, err := ParseGitTreeListing([]byte(listing), true, 10); err == nil || entries != nil {
		t.Errorf("a listing with a malformed record returned %+v, %v; want an error and nothing", entries, err)
	}
}

// Boundary: an empty listing holds no entry; a listing of exactly maxEntries records parses and
// one more is refused; the NUL after the last record is optional.
func TestParseGitTreeListing_Boundary_EmptyAndBound(t *testing.T) {
	for _, empty := range [][]byte{nil, {}} {
		if entries, err := ParseGitTreeListing(empty, true, 1); err != nil || len(entries) != 0 {
			t.Errorf("empty listing = %+v, %v; want nothing and no error", entries, err)
		}
	}
	record := "100644 blob " + treeObject + " 1\tf"
	two := record + "\x00" + record
	if entries, err := ParseGitTreeListing([]byte(two), true, 2); err != nil || len(entries) != 2 {
		t.Errorf("two records at a bound of two = %d, %v; want both", len(entries), err)
	}
	if entries, err := ParseGitTreeListing([]byte(two+"\x00"), true, 1); err == nil || entries != nil {
		t.Errorf("two records past a bound of one = %+v, %v; want a refusal", entries, err)
	}
	if MinGitTreeRecordBytes != len("100644 blob "+treeObject+"\tf\x00") {
		t.Errorf("MinGitTreeRecordBytes = %d, want the length of the shortest record", MinGitTreeRecordBytes)
	}
}
