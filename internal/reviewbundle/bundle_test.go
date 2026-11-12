// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package reviewbundle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
	out, err := util.RunGitBytes(ctx, dir, 1<<20, argv...)
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out.Stderr)
	}
	return strings.TrimSpace(string(out.Stdout))
}

// bundleWrite plants a byte-exact regular file in a fixture repository.
func bundleWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// bundleCommit commits all fixture changes without accessing signing credentials.
func bundleCommit(t *testing.T, dir string) {
	t.Helper()
	bundleGit(t, dir, "add", "-A")
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
	if len(b.Conflicts) != 1 || b.Conflicts[0].Path != "shared.txt" || b.Conflicts[0].Kind == "" {
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

// TestBundleBounds covers exact and excessive per-file content budgets.
func TestBundleBounds(t *testing.T) {
	dir := bundleRepo(t)
	bundleWrite(t, dir, "a.txt", "12345")
	bundleWrite(t, dir, "b.txt", "1234")
	bundleCommit(t, dir)
	b, err := Build(t.Context(), dir, "target", "head", Options{MaxFileBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 2 || !b.Files[0].Truncated || b.Files[0].TruncatedBytes != 1 || b.Files[0].Content != "1234" || b.Files[1].Truncated {
		t.Fatalf("file boundary: %+v", b)
	}
	if !slices.Contains(b.Cut, "a.txt") {
		t.Fatal("truncated path absent from Cut")
	}
}

// TestBundleCountAndTotalBounds lists omitted files and marks content cut by the total budget.
func TestBundleCountAndTotalBounds(t *testing.T) {
	dir := bundleRepo(t)
	bundleWrite(t, dir, "a.txt", "12345")
	bundleWrite(t, dir, "b.txt", "1234")
	bundleCommit(t, dir)
	b, err := Build(t.Context(), dir, "target", "head", Options{MaxFiles: 1})
	if err != nil || len(b.Files) != 1 || !slices.Contains(b.Cut, "b.txt") {
		t.Fatalf("count bound: %+v, %v", b, err)
	}
	b, err = Build(t.Context(), dir, "target", "head", Options{MaxBundleBytes: 6})
	if err != nil || len(b.Files) != 2 || b.Files[1].Content != "1" || b.Files[1].TruncatedBytes != 3 {
		t.Fatalf("total bound: %+v, %v", b, err)
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
	dir := bundleRepo(t)
	bundleGit(t, dir, "mv", "shared.txt", "renamed.txt")
	paths := []string{"-dash", "colon:name", "line\nbreak", "[glob]*.txt"}
	for i := 0; i < len(paths); i++ {
		bundleWrite(t, dir, paths[i], paths[i]+"\n")
	}
	bundleCommit(t, dir)
	b, err := Build(t.Context(), dir, "target", "head", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != len(paths)+2 {
		t.Fatalf("lost paths: %+v", b.Files)
	}
	for i := 0; i < len(b.Files); i++ {
		f := b.Files[i]
		if f.Path == "shared.txt" {
			if !f.Deleted {
				t.Fatal("rename source not deleted")
			}
			continue
		}
		if f.Deleted || f.Content == "" {
			t.Fatalf("literal path lost: %+v", f)
		}
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
	if err := os.Remove(filepath.Join(dir, "shared.txt")); err != nil {
		t.Fatal(err)
	}
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
