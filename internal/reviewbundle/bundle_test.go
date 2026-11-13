// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package reviewbundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// bundleGit runs fixture Git with no inherited Git settings and bounded output.
func bundleGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	env := util.FilterEnvironment(os.Environ(), func(name string) bool { return strings.HasPrefix(strings.ToUpper(name), "GIT_") })
	env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	ctx, err := util.WithCommandEnvironment(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	argv := append([]string{"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)
	out, err := util.RunGitProbe(ctx, dir, 1<<20, argv...)
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out.Stderr)
	}
	return strings.TrimSpace(string(out.Stdout))
}

// bundleWrite stages byte-exact fixture content; snapshots handle supported text paths.
// Git stdin stages binary data and control-character paths that text snapshots reject.
func bundleWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if utf8.ValidString(content) && utf8.ValidString(name) && !strings.ContainsRune(content, 0) && !strings.ContainsAny(name, "\n\r") {
		if err := contextopt.WriteSnapshot(t.Context(), filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		bundleGit(t, dir, "--literal-pathspecs", "add", "--", name)
		return
	}
	ctx, err := util.WithCommandStdin(t.Context(), []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	out, err := util.RunGitProbe(ctx, dir, 1024, "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatal(err)
	}
	bundleGit(t, dir, "update-index", "--add", "--cacheinfo", "100644", strings.TrimSpace(string(out.Stdout)), name)
}

// bundleCommit commits all fixture changes without accessing signing credentials.
func bundleCommit(t *testing.T, dir string) {
	t.Helper()
	bundleGit(t, dir, "commit", "-qm", "fixture")
}

// bundleRepo creates a common ancestor and a head branch for independent edits.
func bundleRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bundleGit(t, dir, "init", "-q", "-b", "target")
	bundleWrite(t, dir, "shared.txt", "first\n"+strings.Repeat("middle\n", 12)+"last\n")
	bundleCommit(t, dir)
	bundleGit(t, dir, "checkout", "-qb", "head")
	return dir
}

// TestBundleStaleBranch proves contents come from the predicted merge, preserving target edits.
func TestBundleStaleBranch(t *testing.T) {
	dir := bundleRepo(t)
	bundleWrite(t, dir, "shared.txt", "head-first\n"+strings.Repeat("middle\n", 12)+"last\n")
	bundleCommit(t, dir)
	bundleGit(t, dir, "checkout", "-q", "target")
	bundleWrite(t, dir, "shared.txt", "first\n"+strings.Repeat("middle\n", 12)+"target-last\n")
	bundleWrite(t, dir, "target-only.txt", "new target work\n")
	bundleCommit(t, dir)
	b, err := Build(t.Context(), dir, "target", "head", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 1 || !strings.Contains(b.Files[0].Content, "target-last") || !strings.Contains(b.Files[0].Content, "head-first") {
		t.Fatalf("stale content: %+v", b)
	}
	if raw := bundleGit(t, dir, "show", "head:shared.txt"); strings.Contains(raw, "target-last") {
		t.Fatal("head unexpectedly contains target advance")
	}
	ids := []string{b.Tree, b.MergeBase, b.Target, b.Head}
	for i := 0; i < len(ids); i++ {
		if !util.ValidGitObjectID(ids[i]) {
			t.Fatalf("invalid resolved provenance: %q", ids[i])
		}
	}
	if err := Validate(b); err != nil {
		t.Fatal(err)
	}
}

// TestBundleConflict preserves exit-one merge findings and validates their recorded coverage.
func TestBundleConflict(t *testing.T) {
	dir := bundleRepo(t)
	bundleWrite(t, dir, "shared.txt", "head\n")
	bundleCommit(t, dir)
	bundleGit(t, dir, "checkout", "-q", "target")
	bundleWrite(t, dir, "shared.txt", "target\n")
	bundleCommit(t, dir)
	b, err := Build(t.Context(), dir, "target", "head", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Conflicts) != 1 || b.Conflicts[0].Path != "shared.txt" || b.Conflicts[0].Kind != "CONFLICT (contents)" {
		t.Fatalf("missing conflict: %+v", b)
	}
	if err := Validate(b); err != nil {
		t.Fatal(err)
	}
	b.Conflicts = nil
	if err := Validate(b); err == nil {
		t.Fatal("missing conflict accepted")
	}
}

// TestBundleBounds shares one repository fixture across per-file, file-count and total budgets.
func TestBundleBounds(t *testing.T) {
	dir := bundleRepo(t)
	bundleWrite(t, dir, "a.txt", "12345")
	bundleWrite(t, dir, "b.txt", "1234")
	bundleCommit(t, dir)
	cases := []struct {
		name         string
		limits       Options
		files        []File
		cut, omitted []string
	}{
		{"file", Options{MaxFileBytes: 4}, []File{{Path: "a.txt", Content: "1234", Truncated: true, TruncatedBytes: 1}, {Path: "b.txt", Content: "1234"}}, []string{"a.txt"}, []string{}},
		{"count", Options{MaxFiles: 1}, []File{{Path: "a.txt", Content: "12345"}}, []string{"b.txt"}, []string{"b.txt"}},
		{"total", Options{MaxBundleBytes: 6}, []File{{Path: "a.txt", Content: "12345"}, {Path: "b.txt", Content: "1", Truncated: true, TruncatedBytes: 3}}, []string{"b.txt"}, []string{}},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			b, err := Build(t.Context(), dir, "target", "head", tc.limits)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(b.Files, tc.files) || !slices.Equal(b.Cut, tc.cut) || !slices.Equal(b.OmittedPaths, tc.omitted) {
				t.Fatalf("bounds: %+v", b)
			}
		})
	}
}

// TestBundleValidate rejects planted missing marks, invalid cut counts and oversized content.
func TestBundleValidate(t *testing.T) {
	bad := []Bundle{
		{Files: []File{{Path: "large", Content: strings.Repeat("x", 64<<10+1)}}},
		{Files: []File{{Path: "cut", Truncated: true}}},
		{Files: []File{{Path: "cut", Truncated: true, TruncatedBytes: -1}}},
		{Limits: Options{MaxFileBytes: 4}, Files: []File{{Path: "large", Content: "12345"}}},
	}
	for i := 0; i < len(bad); i++ {
		if err := Validate(bad[i]); err == nil {
			t.Errorf("invalid bundle %d accepted", i)
		}
	}
	if err := Validate(Bundle{Files: []File{{Path: "exact", Content: strings.Repeat("x", 64<<10)}}}); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Bundle{}); err != nil {
		t.Fatal(err)
	}
}

// TestBundleValidateTruncation checks real overflow reads and refuses a planted missing cut mark.
func TestBundleValidateTruncation(t *testing.T) {
	dir := bundleRepo(t)
	bundleWrite(t, dir, "large.txt", strings.Repeat("x", 64<<10+1))
	bundleCommit(t, dir)
	b, err := Build(t.Context(), dir, "target", "head", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 1 || !b.Files[0].Truncated || b.Files[0].TruncatedBytes != 1 || len(b.Files[0].Content) != 64<<10 {
		t.Fatalf("overflow prefix: files=%d", len(b.Files))
	}
	if err := Validate(b); err != nil {
		t.Fatal(err)
	}
	b.Files[0].Truncated = false
	if err := Validate(b); err == nil {
		t.Fatal("missing truncation mark accepted")
	}
}

// TestBundlePathNames keeps NUL-delimited special names literal, including rename deletions.
func TestBundlePathNames(t *testing.T) {
	bundlePathNames(t, []string{"-dash", "[glob].txt", "space name.txt", "é.txt"})
}

// TestBundleLiteralPathNames covers names unsupported by Windows filesystem APIs.
func TestBundleLiteralPathNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot contain colons, newlines or asterisks")
	}
	bundlePathNames(t, []string{"colon:name", "line\nbreak", "[glob]*.txt", ":(literal)file.txt"})
}

// bundlePathNames checks exact names and both sides of a head rename.
func bundlePathNames(t *testing.T, paths []string) {
	t.Helper()
	dir := bundleRepo(t)
	original, err := contextopt.ReadSnapshot(t.Context(), filepath.Join(dir, "shared.txt"))
	if err != nil {
		t.Fatal(err)
	}
	bundleGit(t, dir, "mv", "shared.txt", "renamed.txt")
	expected := map[string]File{"renamed.txt": {Path: "renamed.txt", Content: string(original)}, "shared.txt": {Path: "shared.txt", Deleted: true}}
	if len(paths) > 16 {
		t.Fatal("special-path fixture exceeds 16 paths")
	}
	for i := 0; i < 16 && i < len(paths); i++ {
		expected[paths[i]] = File{Path: paths[i], Content: paths[i] + "\n"}
		bundleWrite(t, dir, paths[i], expected[paths[i]].Content)
	}
	bundleCommit(t, dir)
	b, err := Build(t.Context(), dir, "target", "head", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != len(expected) {
		t.Fatalf("lost paths: %+v", b.Files)
	}
	for i := 0; i < len(b.Files); i++ {
		f := b.Files[i]
		if f != expected[f.Path] {
			t.Fatalf("literal path/content corrupted: %+v", f)
		}
		delete(expected, f.Path)
	}
	if len(expected) != 0 {
		t.Fatalf("missing paths: %v", expected)
	}
}

// TestBundleTargetRename retains head edits at the path selected by the predicted merge.
func TestBundleTargetRename(t *testing.T) {
	dir := bundleRepo(t)
	content := "head-first\n" + strings.Repeat("middle\n", 12) + "last\n"
	bundleWrite(t, dir, "shared.txt", content)
	bundleCommit(t, dir)
	bundleGit(t, dir, "checkout", "-q", "target")
	bundleGit(t, dir, "mv", "shared.txt", "renamed.txt")
	bundleWrite(t, dir, "target-only.txt", "independent target addition\n")
	bundleCommit(t, dir)
	b, err := Build(t.Context(), dir, "target", "head", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 1 || b.Files[0].Path != "renamed.txt" || b.Files[0].Deleted || b.Files[0].Content != content {
		t.Fatalf("lost merged rename content: %+v", b.Files)
	}
}

// TestBundleUTF8Truncation preserves source prefixes and byte budgets through JSON.
func TestBundleUTF8Truncation(t *testing.T) {
	cases := []struct {
		source  string
		limit   int
		content string
		omitted int
	}{
		{"aaaé", 4, "aaa", 2},
		{"aaaé", 5, "aaaé", 0},
		{"aaaé", 3, "aaa", 2},
		{"é", 1, "", 2},
		{"a€z", 3, "a", 4},
		{"a😀z", 4, "a", 5},
		{"a�z", 4, "a�", 1},
		{strings.Repeat("a", 7999) + "é", 8000, strings.Repeat("a", 7999), 2},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		dir := bundleRepo(t)
		bundleWrite(t, dir, "utf8.txt", tc.source)
		bundleCommit(t, dir)
		b, err := Build(t.Context(), dir, "target", "head", Options{MaxFileBytes: tc.limit})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Bundle
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if len(decoded.Files) != 1 {
			t.Fatalf("lost UTF-8 file: %+v", decoded.Files)
		}
		f := decoded.Files[0]
		if f.Content != tc.content || f.TruncatedBytes != tc.omitted || f.Truncated != (tc.omitted > 0) || len(f.Content) > tc.limit {
			t.Errorf("limit %d: %+v", tc.limit, f)
		}
	}
}

// TestBundleUTF8WholeBlobKeepsBytes keeps a blob within the budget byte for byte, an invalid UTF-8 tail
// included, and never marks it cut: only a blob over the budget loses its incomplete final rune.
func TestBundleUTF8WholeBlobKeepsBytes(t *testing.T) {
	cases := []struct {
		source string
		limit  int
	}{
		{"ab\xe9", 3},
		{"ab\xe9", 8},
		{"ab\xc3", 3},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		dir := bundleRepo(t)
		bundleWrite(t, dir, "latin1.txt", tc.source)
		bundleCommit(t, dir)
		b, err := Build(t.Context(), dir, "target", "head", Options{MaxFileBytes: tc.limit})
		if err != nil {
			t.Fatal(err)
		}
		if len(b.Files) != 1 {
			t.Fatalf("lost whole blob: %+v", b.Files)
		}
		encoded, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &b); err != nil {
			t.Fatal(err)
		}
		f := b.Files[0]
		if f.Content != tc.source || f.Truncated || f.TruncatedBytes != 0 || len(b.Cut) != 0 {
			t.Errorf("limit %d: %+v cut %v, want whole %q", tc.limit, f, b.Cut, tc.source)
		}
	}
}

// TestBundleValidateCutConsistency rejects mismatched retained cuts and permits omitted paths.
func TestBundleValidateCutConsistency(t *testing.T) {
	cases := []struct {
		name  string
		file  File
		cut   []string
		valid bool
	}{
		{"unmarked retained cut", File{Path: "a"}, []string{"a"}, false},
		{"unlisted truncated file", File{Path: "a", Truncated: true, TruncatedBytes: 2}, nil, false},
		{"consistent retained cut", File{Path: "a", Truncated: true, TruncatedBytes: 2}, []string{"a"}, true},
		{"unaccounted path", File{Path: "a"}, []string{"omitted"}, false},
		{"no cuts", File{Path: "a"}, nil, true},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(Bundle{Files: []File{tc.file}, Cut: tc.cut})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t: %v", tc.valid, err)
			}
		})
	}
}

// TestBundleMergeParser refuses malformed inventories rather than concealing conflict evidence.
func TestBundleMergeParser(t *testing.T) {
	tree := strings.Repeat("a", 40)
	bad := []string{"", tree + "\x00path", tree + "\x00\x00oops\x00", tree + "\x00\x0099\x00path\x00CONFLICT (contents)\x00message\x00"}
	for i := 0; i < len(bad); i++ {
		if err := parseMerge([]byte(bad[i]), &Bundle{}); err == nil {
			t.Errorf("malformed inventory %d accepted", i)
		}
	}
	b := Bundle{}
	if err := parseMerge([]byte(tree+"\x00\x00"), &b); err != nil || b.Tree != tree {
		t.Fatalf("empty merge: %+v, %v", b, err)
	}
	if err := parseMerge([]byte(tree+"\x00path\x00\x00"), &b); err != nil || len(b.Conflicts) != 1 {
		t.Fatalf("index-only conflict: %+v, %v", b, err)
	}
}

// TestBundleDeletedAndBinary excludes deleted and binary contents, including binary data beyond a cut.
func TestBundleDeletedAndBinary(t *testing.T) {
	dir := bundleRepo(t)
	bundleGit(t, dir, "rm", "--", "shared.txt")
	bundleWrite(t, dir, "binary", "abc\x00def")
	bundleCommit(t, dir)
	b, err := Build(t.Context(), dir, "target", "head", Options{MaxFileBytes: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 2 || !b.Files[0].Binary || b.Files[0].Content != "" || !b.Files[1].Deleted || b.Files[1].Content != "" {
		t.Fatalf("file kinds: %+v", b)
	}
}

// TestBundleErrors distinguishes invalid references, cancellation and invalid bounds.
func TestBundleErrors(t *testing.T) {
	dir := bundleRepo(t)
	refs := []string{"missing-ref", "--all"}
	for i := 0; i < len(refs); i++ {
		ref := refs[i]
		if _, err := Build(t.Context(), dir, "target", ref, Options{}); err == nil || !strings.Contains(err.Error(), ref) {
			t.Fatalf("unknown ref %q: %v", ref, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Build(ctx, dir, "target", "head", Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := Build(t.Context(), dir, "target", "head", Options{MaxFiles: -1}); err == nil {
		t.Fatal("negative limit accepted")
	}
	b, err := Build(t.Context(), dir, "target", "head", Options{})
	if err != nil || len(b.Files) != 0 {
		t.Fatalf("empty diff: %+v, %v", b, err)
	}
}

// TestBundleReviewCutAccounting requires unique, complete cut evidence for retained and omitted paths.
func TestBundleReviewCutAccounting(t *testing.T) {
	cases := []struct {
		name  string
		b     Bundle
		valid bool
	}{
		{"omitted missing cut", Bundle{OmittedPaths: []string{"gone"}}, false},
		{"unaccounted cut", Bundle{Cut: []string{"ghost"}}, false},
		{"duplicate cut", Bundle{Cut: []string{"gone", "gone"}, OmittedPaths: []string{"gone"}}, false},
		{"empty cut", Bundle{Cut: []string{""}}, false},
		{"duplicate omitted", Bundle{Cut: []string{"gone"}, OmittedPaths: []string{"gone", "gone"}}, false},
		{"retained omitted", Bundle{Files: []File{{Path: "gone"}}, Cut: []string{"gone"}, OmittedPaths: []string{"gone"}}, false},
		{"duplicate retained", Bundle{Files: []File{{Path: "a"}, {Path: "a"}}}, false},
		{"complete omitted", Bundle{Cut: []string{"gone"}, OmittedPaths: []string{"gone"}}, true},
		{"retained cut", Bundle{Files: []File{{Path: "a", Truncated: true, TruncatedBytes: 1}}, Cut: []string{"a"}}, true},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(tc.b); (err == nil) != tc.valid {
				t.Fatalf("valid=%t: %v", tc.valid, err)
			}
		})
	}
}

// TestBundleReviewMetadataBounds rejects oversized inventories before map allocation or iteration.
func TestBundleReviewMetadataBounds(t *testing.T) {
	cases := []Bundle{
		{Conflicts: make([]Conflict, 4097)}, {ConflictedPaths: make([]string, 4097)},
		{Cut: make([]string, 4097)}, {OmittedPaths: make([]string, 4097)},
		{Files: make([]File, 4097), Limits: Options{MaxFiles: 4097}},
	}
	for i := 0; i < 4097; i++ {
		cases[0].Conflicts[i] = Conflict{Path: "a", Kind: "CONFLICT (contents)"}
		cases[1].ConflictedPaths[i] = "a"
	}
	cases[1].Conflicts = []Conflict{{Path: "a", Kind: "CONFLICT (contents)"}}
	for i := 0; i < len(cases); i++ {
		if err := Validate(cases[i]); err == nil || !strings.Contains(err.Error(), "metadata") {
			t.Errorf("inventory %d: %v", i, err)
		}
	}
	b := Bundle{Conflicts: make([]Conflict, 4096), ConflictedPaths: make([]string, 4096)}
	for i := 0; i < 4096; i++ {
		b.Conflicts[i] = Conflict{Path: strconv.Itoa(i), Kind: "CONFLICT (contents)"}
		b.ConflictedPaths[i] = strconv.Itoa(i)
	}
	if err := Validate(b); err != nil {
		t.Fatal(err)
	}
}

// TestBundleReviewPathLimit bounds path metadata before splitting or retaining omitted entries.
func TestBundleReviewPathLimit(t *testing.T) {
	cases := []struct {
		count int
		valid bool
	}{{4095, true}, {4096, true}, {4097, false}}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		b := Bundle{Limits: Options{MaxFiles: 1}, Files: []File{{Path: "retained"}}}
		err := appendFiles(t.Context(), "", []byte(strings.Repeat("x\x00", tc.count)), &b)
		if (err == nil) != tc.valid {
			t.Errorf("paths=%d: %v", tc.count, err)
		}
		if !tc.valid && (len(b.Cut) != 0 || len(b.OmittedPaths) != 0) {
			t.Fatal("oversized listing partially retained")
		}
	}
}

// TestBundleReviewMergeLimit bounds records, fields, paths and cumulative conflict classifications.
func TestBundleReviewMergeLimit(t *testing.T) {
	tree := strings.Repeat("a", 40) + "\x00\x00"
	record := "1\x00path\x00Auto-merging\x00message\x00"
	for n := 4095; n <= 4097; n++ {
		err := parseMerge([]byte(tree+strings.Repeat(record, n)), &Bundle{})
		if (err == nil) != (n <= 4096) {
			t.Errorf("records=%d: %v", n, err)
		}
	}
	bad := []string{
		strings.Repeat("a", 40) + "\x00" + strings.Repeat("x\x00", 4097) + "\x00",
		tree + "4097\x00" + strings.Repeat("x\x00", 4097) + "CONFLICT (contents)\x00message\x00",
		tree + "4097\x00" + strings.Repeat("x\x00", 4097) + "Auto-merging\x00message\x00",
		tree + strings.Repeat("2\x00x\x00y\x00CONFLICT (contents)\x00message\x00", 2049),
	}
	for i := 0; i < len(bad); i++ {
		b := Bundle{}
		err := parseMerge([]byte(bad[i]), &b)
		if err == nil {
			t.Errorf("oversized merge %d accepted", i)
		}
		if len(b.ConflictedPaths) > 4096 || len(b.Conflicts) > 4096 {
			t.Errorf("oversized conflict inventory retained: paths=%d conflicts=%d", len(b.ConflictedPaths), len(b.Conflicts))
		}
		if i == 0 && len(b.ConflictedPaths) != 0 {
			t.Fatal("oversized path inventory appended before rejection")
		}
	}
}

// TestBundleReviewMergeFieldLimit rejects excessive fields before changing the bundle or splitting.
func TestBundleReviewMergeFieldLimit(t *testing.T) {
	data := strings.Repeat("a", 40) + "\x00\x00" + strings.Repeat("\x00", 6*4096+4)
	b := Bundle{}
	err := parseMerge([]byte(data), &b)
	if err == nil {
		t.Fatal("oversized fields accepted")
	}
	if b.Tree != "" || !strings.Contains(err.Error(), "field limit") {
		t.Fatalf("fields expanded before rejection: %v", err)
	}
}

// TestBundleReviewJSONBytes round-trips invalid bytes in every path inventory and retained content.
func TestBundleReviewJSONBytes(t *testing.T) {
	values := []string{"valid é", "bad\xff", "partial\xc3", "replacement �"}
	for i := 0; i < len(values); i++ {
		value := values[i]
		b := Bundle{Files: []File{{Path: value, Content: value, Truncated: true, TruncatedBytes: 1}}, Cut: []string{value, "omitted" + value}, OmittedPaths: []string{"omitted" + value}, Conflicts: []Conflict{{Path: value, Kind: "CONFLICT (contents)"}}, ConflictedPaths: []string{value}}
		encoded, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Bundle
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(b, decoded) {
			t.Errorf("bytes changed: want %+v, got %+v", b, decoded)
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(value) && !bytes.Contains(encoded, []byte(`"base64"`)) {
			t.Fatal("missing explicit byte encoding")
		}
	}
}
