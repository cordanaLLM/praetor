package main

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

// The HISS-14 commit gate is only real if the command exists and refuses bad input.
func TestForgeCheckCommits_Positive_EmptyRange(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	if err := dispatchCommand("forge", []string{"check-commits", "--repo=" + dir, "--base=HEAD", "--head=HEAD"}); err != nil {
		t.Fatalf("expected an empty commit range to pass, got %v", err)
	}
}

func TestForgeCheckCommits_Negative_MissingBaseAndHostileRevision(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	if err := dispatchCommand("forge", []string{"check-commits"}); err == nil {
		t.Fatal("expected an error when --base is missing")
	}

	err := dispatchCommand("forge", []string{"check-commits", "--repo=" + dir, "--base=HEAD;rm -rf /", "--head=HEAD"})
	if err == nil {
		t.Fatal("expected a shell metacharacter in a revision to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid git revision") {
		t.Fatalf("expected the revision validation error, got %v", err)
	}
}

func TestForgeCheckCommits_Boundary_UnknownRevision(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	err := dispatchCommand("forge", []string{
		"check-commits", "--repo=" + dir, "--base=0000000000000000000000000000000000000000", "--head=HEAD",
	})
	if err == nil {
		t.Fatal("expected an error for a revision that does not exist")
	}
}

func TestForgeCheckCommits_EnforcesBreakingMigrationFooter(t *testing.T) {
	dir := t.TempDir()
	env := initGitFixture(t, dir)
	writeFixtureFile(t, dir, "change.txt", "with migration\n")
	gitCommitAll(t, dir, env, "feat!: change interface\n\nMigration: switch to the replacement API")
	args := []string{"check-commits", "--repo=" + dir, "--base=HEAD~1", "--head=HEAD"}
	if err := dispatchCommand("forge", args); err != nil {
		t.Fatalf("breaking commit with migration footer: %v", err)
	}
	writeFixtureFile(t, dir, "change.txt", "missing migration\n")
	gitCommitAll(t, dir, env, "feat!: change interface again")
	if err := dispatchCommand("forge", args); err == nil || !strings.Contains(err.Error(), "HISS-14 violation") {
		t.Fatalf("expected missing Migration footer failure, got %v", err)
	}
	if err := dispatchCommand("forge", append(args, "ignored")); err == nil {
		t.Fatal("unexpected positional argument must fail")
	}
}

// importCommitRange builds a real, bounded history in a temporary repository in one
// Git process. No developer repository, credentials or hooks are involved.
func importCommitRange(t *testing.T, count int) string {
	t.Helper()
	if count < 1 || count > maxAnalyzedCommits+1 {
		t.Fatal("fixture count outside the bounded test range")
	}
	dir := t.TempDir()
	env := initGitFixture(t, dir)
	var input strings.Builder
	for i := 0; i < count; i++ {
		message := fmt.Sprintf("chore: range fixture %d", i)
		fragment := fmt.Sprintf("commit refs/heads/range\ncommitter Test <test@example.invalid> 1700000000 +0000\ndata %d\n%s\n", len(message), message)
		input.WriteString(fragment)
		if i == 0 {
			input.WriteString("from HEAD\n")
		}
		input.WriteString("\n")
	}
	input.WriteString("done\n")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	envCtx, err := util.WithCommandEnvironment(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	stdinCtx, err := util.WithCommandStdin(envCtx, []byte(input.String()))
	if err != nil {
		t.Fatal(err)
	}
	res, err := util.RunCommandBytes(stdinCtx, dir, "git", util.MaxCommandOutputBytes, "fast-import", "--quiet")
	if err != nil {
		t.Fatalf("import commit fixture: %v (%s)", err, string(append(res.Stdout, res.Stderr...)))
	}
	return dir
}

func TestForgeCheckCommits_Boundary_CommitLimit(t *testing.T) {
	dir := importCommitRange(t, maxAnalyzedCommits+1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	messages, err := commitRange(ctx, dir, "HEAD", "refs/heads/range~1")
	if err != nil || len(messages) != maxAnalyzedCommits {
		t.Fatalf("exactly %d commits: got %d, %v", maxAnalyzedCommits, len(messages), err)
	}
	if _, err := commitRange(ctx, dir, "HEAD", "refs/heads/range"); err == nil || !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("range overflow must fail instead of omitting old commits, got %v", err)
	}
}

// TestForgeCheckCommits_Negative_SubjectBreaksPolicy (#61): CI re-runs the commit message policy
// over every commit of a pull request, so a commit made without its commit-msg hook (this fixture
// installs none) and with a subject the hook refuses fails the range, named by its short SHA,
// while the range of the commit before it still passes.
func TestForgeCheckCommits_Negative_SubjectBreaksPolicy(t *testing.T) {
	dir := t.TempDir()
	env := initGitFixture(t, dir)
	writeFixtureFile(t, dir, "change.txt", "good\n")
	gitCommitAll(t, dir, env, "fix(change): keep the policy")
	writeFixtureFile(t, dir, "change.txt", "bad\n")
	gitCommitAll(t, dir, env, "update change.txt")
	out, err := captureStdout(t, func() error {
		return dispatchCommand("forge", []string{"check-commits", "--repo=" + dir, "--base=HEAD~2", "--head=HEAD"})
	})
	if err == nil || !strings.Contains(err.Error(), "commit message policy violation: 1 of 2 commit(s) fail") ||
		!strings.Contains(err.Error(), `Commit subject must be 'type(scope): description' (scope optional)`) {
		t.Fatalf("expected the bad subject to fail the range, got %v\n%s", err, out)
	}
	if !strings.Contains(out, `got "update change.txt"`) {
		t.Fatalf("the failure must quote the subject:\n%s", out)
	}
	if err := dispatchCommand("forge", []string{"check-commits", "--repo=" + dir, "--base=HEAD~2", "--head=HEAD~1"}); err != nil {
		t.Fatalf("the conventional commit alone: %v", err)
	}
}

// TestForgeCheckMessage_Positive_CleansTheHookMessage (#61): the commit-msg hook hands git's
// message file before cleanup; its comment lines and everything after the scissors line are not
// part of the message.
func TestForgeCheckMessage_Positive_CleansTheHookMessage(t *testing.T) {
	path := writeFixtureFile(t, t.TempDir(), "COMMIT_EDITMSG", "# Use type(scope): description\nfix(hooks): gate changes\n\n"+
		"Signed-off-by: Test <test@example.invalid>\n# Please enter the commit message\n"+
		"# ------------------------ >8 ------------------------\nfeat!: a diff line, not the message\n")
	if err := dispatchCommand("forge", []string{"check-message", path}); err != nil {
		t.Fatalf("clean conventional message: %v", err)
	}
}

// TestForgeCheckMessage_BodyWidth refuses planted 73-column prose through the hook command.
func TestForgeCheckMessage_BodyWidth(t *testing.T) {
	dir := t.TempDir()
	for _, width := range []int{72, 73} {
		body := "A " + strings.Repeat("x", width-2)
		path := writeFixtureFile(t, dir, "COMMIT_EDITMSG", "fix: wrap body\n\n"+body+"\n")
		err := dispatchCommand("forge", []string{"check-message", "--repo=" + dir, path})
		if width == 72 && err != nil {
			t.Fatalf("72-column body: %v", err)
		}
		if width == 73 && (err == nil || !strings.Contains(err.Error(), "body line 3 is 73 columns")) {
			t.Fatalf("73-column body must identify line 3 and width: %v", err)
		}
	}
}

func TestForgeCheckMessage_Positive_BareClosingReferenceWarnsOffline(t *testing.T) {
	path := writeFixtureFile(t, t.TempDir(), "COMMIT_EDITMSG", "fix(hooks): close the issue\n\nCloses #28\n")
	out, err := captureStdout(t, func() error {
		return dispatchCommand("forge", []string{"check-message", path})
	})
	if err != nil || !strings.Contains(out, "[WARN]") || !strings.Contains(out, "praetorctl forge check-commits") {
		t.Fatalf("bare closing ref offline = %v\n%s", err, out)
	}
}

// TestForgeCheckMessage_Negative_PolicyAndUsage (#61): a subject outside the policy and a
// breaking change without its Migration: footer fail; a missing file, no file and two files are
// refused.
func TestForgeCheckMessage_Negative_PolicyAndUsage(t *testing.T) {
	dir := t.TempDir()
	for message, want := range map[string]string{
		"bad subject\n":             "Commit subject must be 'type(scope): description' (scope optional)",
		"feat(api)!: drop a call\n": "HISS-14 violation",
	} {
		path := writeFixtureFile(t, dir, "COMMIT_EDITMSG", message)
		if err := dispatchCommand("forge", []string{"check-message", path}); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%q: got %v, want %q", message, err, want)
		}
	}
	for _, args := range [][]string{{"check-message"}, {"check-message", "a", "b"}, {"check-message", filepath.Join(dir, "missing")}} {
		if err := dispatchCommand("forge", args); err == nil {
			t.Fatalf("%v must fail", args)
		}
	}
}

// TestForgeCheckMessage_Boundary_EmptyAndOversizedMessages (#61): a message of comments only is
// empty, a file past the read bound is refused rather than read in part, and one of exactly the
// bound is read.
func TestForgeCheckMessage_Boundary_EmptyAndOversizedMessages(t *testing.T) {
	dir := t.TempDir()
	comments := writeFixtureFile(t, dir, "comments", "# nothing but comments\n")
	if err := dispatchCommand("forge", []string{"check-message", comments}); err == nil || !strings.Contains(err.Error(), "empty commit message") {
		t.Fatalf("comments only: %v", err)
	}
	subject := "fix: at the bound\n"
	exact := writeFixtureFile(t, dir, "exact", subject+strings.Repeat("x", maxCommitMessageBytes-len(subject)))
	if err := dispatchCommand("forge", []string{"check-message", exact}); err != nil {
		t.Fatalf("a message of exactly the bound: %v", err)
	}
	oversized := writeFixtureFile(t, dir, "oversized", subject+strings.Repeat("x", maxCommitMessageBytes))
	if err := dispatchCommand("forge", []string{"check-message", oversized}); err == nil {
		t.Fatal("a message past the read bound must fail")
	}
	// Within the byte bound but past the line bound of forge.CleanCommitMessage: the message is
	// refused, so the BREAKING CHANGE: footer after the bound cannot pass unread without its
	// Migration: footer.
	manyLines := writeFixtureFile(t, dir, "many-lines", subject+strings.Repeat("b\n", 200000)+"BREAKING CHANGE: past the line bound\n")
	if err := dispatchCommand("forge", []string{"check-message", manyLines}); err == nil ||
		!strings.Contains(err.Error(), "lines before the scissors line") {
		t.Fatalf("a message past the line bound: %v", err)
	}
}

// commitExcludeFixture builds 301 commits without hooks or ambient Git configuration.
// Lane isolation permits only stripping GIT_* and null global/no-system configuration.
func commitExcludeFixture(t *testing.T) (context.Context, string, []string) {
	t.Helper()
	dir := t.TempDir()
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "GIT_") {
			env = append(env, entry)
		}
	}
	env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	ctx, err := util.WithCommandEnvironment(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(ctx, dir, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	stdinCtx, err := util.WithCommandStdin(ctx, commitExcludeImport())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(stdinCtx, dir, "fast-import", "--quiet"); err != nil {
		t.Fatal(err)
	}
	refs := make([]string, 300)
	for i := 0; i < len(refs); i++ {
		refs[i] = fmt.Sprintf("refs/heads/excluded-%d", i+1)
	}
	return ctx, dir, refs
}

// commitExcludeImport renders 300 independent exclusions merged into one outgoing head.
func commitExcludeImport() []byte {
	var input strings.Builder
	for i := 0; i <= 300; i++ {
		branch := "base"
		if i > 0 {
			branch = fmt.Sprintf("excluded-%d", i)
		}
		message := fmt.Sprintf("chore: exclude fixture %d", i)
		fmt.Fprintf(&input, "commit refs/heads/%s\nmark :%d\ncommitter Test <test@example.invalid> 1700000000 +0000\ndata %d\n%s\n", branch, i+1, len(message), message)
		if i > 0 {
			input.WriteString("from :1\n")
		}
		input.WriteString("\n")
	}
	message := "chore: own outgoing commit"
	fmt.Fprintf(&input, "commit refs/heads/range\ncommitter Test <test@example.invalid> 1700000000 +0000\ndata %d\n%s\nfrom :1\n", len(message), message)
	for mark := 2; mark <= 301; mark++ {
		fmt.Fprintf(&input, "merge :%d\n", mark)
	}
	input.WriteString("\n")
	return []byte(input.String())
}

// chunkedExcludeRange intersects small raw-argv Git walks as an independent union-exclusion oracle.
func chunkedExcludeRange(t *testing.T, ctx context.Context, dir string, refs []string) []string {
	t.Helper()
	var result []string
	for offset := 0; offset < len(refs) && offset < 300; offset += 100 {
		args := []string{"log", "--format=%H%x1f%B%x00", "refs/heads/base..refs/heads/range", "--not"}
		args = append(args, refs[offset:min(offset+100, len(refs))]...)
		out, err := util.RunGit(ctx, dir, append(args, "--")...)
		if err != nil {
			t.Fatal(err)
		}
		messages, err := parseCommitRecords(strings.Split(out, commitRecordSep))
		if err != nil {
			t.Fatal(err)
		}
		if offset == 0 {
			result = messages
		} else {
			result = slices.DeleteFunc(result, func(message string) bool { return !slices.Contains(messages, message) })
		}
	}
	return result
}

// TestForgeCheckCommits_ExcludeFile300 proves the file path and Git stdin preserve range semantics.
func TestForgeCheckCommits_ExcludeFile300(t *testing.T) {
	ctx, dir, refs := commitExcludeFixture(t)
	path := writeFixtureFile(t, t.TempDir(), "excludes", strings.Join(refs, "\n")+"\n")
	loaded, err := loadCommitExcludes(ctx, path, nil)
	if err != nil || !slices.Equal(loaded, refs) {
		t.Fatalf("load 300 revisions: %v, %v", loaded, err)
	}
	got, err := commitRange(ctx, dir, "refs/heads/base", "refs/heads/range", loaded)
	if err != nil || len(got) != 1 || !slices.Equal(got, chunkedExcludeRange(t, ctx, dir, refs)) {
		t.Fatalf("stdin range differs from chunked argv: %v, %v", got, err)
	}
	out, err := captureStdout(t, func() error {
		return runForgeCheckCommits(ctx, []string{"--repo=" + dir, "--base=refs/heads/base", "--head=refs/heads/range", "--exclude-file=" + path})
	})
	if err != nil || !strings.Contains(out, "300 commit(s)") || !strings.Contains(out, "[PASS]") {
		t.Fatalf("300 excluded commits must pass and report left out: %v\n%s", err, out)
	}
}

// TestForgeCheckCommits_ExcludeFileCommandForms exercises the new source through the public command.
func TestForgeCheckCommits_ExcludeFileCommandForms(t *testing.T) {
	ctx, dir, _ := commitExcludeFixture(t)
	for _, tc := range []struct {
		name, text string
		flags      []string
		count      int
	}{
		{name: "empty", count: 301},
		{name: "blank", text: "\n \n\r\n", count: 301},
		{name: "duplicate", text: "refs/heads/excluded-1\nrefs/heads/excluded-1\n", count: 300},
		{name: "merged", text: "refs/heads/excluded-2\n", flags: []string{"--exclude=refs/heads/excluded-1"}, count: 299},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFixtureFile(t, t.TempDir(), "excludes", tc.text)
			args := []string{"--repo=" + dir, "--base=refs/heads/base", "--head=refs/heads/range", "--exclude-file=" + path}
			out, err := captureStdout(t, func() error { return runForgeCheckCommits(ctx, append(args, tc.flags...)) })
			want := fmt.Sprintf("Commit Message Policy: %d commit(s)", tc.count)
			if err != nil || !strings.Contains(out, want) {
				t.Fatalf("want %s: %v\n%s", want, err, out)
			}
		})
	}
}

// TestForgeCheckCommits_ExcludeFileForms covers empty, blank, duplicate, merged and CRLF input.
func TestForgeCheckCommits_ExcludeFileForms(t *testing.T) {
	for _, tc := range []struct {
		name, text    string
		initial, want []string
	}{
		{name: "empty"},
		{name: "blank", text: "\n \n\r\n"},
		{name: "duplicate", text: "HEAD\nHEAD\n", want: []string{"HEAD", "HEAD"}},
		{name: "merged", text: "HEAD~1\r\n", initial: []string{"HEAD"}, want: []string{"HEAD", "HEAD~1"}},
		{name: "no final newline", text: "HEAD", want: []string{"HEAD"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFixtureFile(t, t.TempDir(), "excludes", tc.text)
			got, err := loadCommitExcludes(t.Context(), path, tc.initial)
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

// TestForgeCheckCommits_ExcludeFileRefusals pins line, count, byte and source diagnostics.
func TestForgeCheckCommits_ExcludeFileRefusals(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"invalid", "HEAD\n--all\n", "line 2: invalid git revision"},
		{"control", "HEAD\nHE\x00AD\n", "line 2: invalid git revision"},
		{"long line", strings.Repeat("x", util.MaxExecArgLen+1), "line 1 exceeds revision line bound"},
		{"revision overflow", strings.Repeat("HEAD\n", maxCommitExcludeRevisions+1), "maxCommitExcludeRevisions"},
		{"byte overflow", strings.Repeat("\n", maxCommitExcludeFileBytes+1), "maxCommitExcludeFileBytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFixtureFile(t, t.TempDir(), "excludes", tc.text)
			_, err := loadCommitExcludes(t.Context(), path, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), strconv.Quote(path)) {
				t.Fatalf("want --exclude-file path and %q, got %v", tc.want, err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "missing")
	if _, err := loadCommitExcludes(t.Context(), path, nil); err == nil || !strings.Contains(err.Error(), strconv.Quote(path)) {
		t.Fatalf("unreadable source must name path: %v", err)
	}
}

// TestForgeCheckCommits_ExcludeFileSourceErrors refuses nonregular sources and expired reads.
func TestForgeCheckCommits_ExcludeFileSourceErrors(t *testing.T) {
	path := t.TempDir()
	if _, err := loadCommitExcludes(t.Context(), path, nil); err == nil || !strings.Contains(err.Error(), strconv.Quote(path)) {
		t.Fatalf("directory source must name its path: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := loadCommitExcludes(ctx, path, nil); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expired read must fail: %v", err)
	}
}

// TestForgeCheckCommits_ExcludeFileBounds accepts exact bounds and refuses merged overflow.
func TestForgeCheckCommits_ExcludeFileBounds(t *testing.T) {
	ctx := t.Context()
	for _, text := range []string{strings.Repeat("HEAD\n", maxCommitExcludeRevisions), strings.Repeat("\n", maxCommitExcludeFileBytes), strings.Repeat("x", util.MaxExecArgLen)} {
		path := writeFixtureFile(t, t.TempDir(), "excludes", text)
		if _, err := loadCommitExcludes(ctx, path, nil); err != nil {
			t.Fatalf("exact boundary refused: %v", err)
		}
	}
	path := writeFixtureFile(t, t.TempDir(), "excludes", strings.Repeat("HEAD\n", maxCommitExcludeRevisions))
	if _, err := loadCommitExcludes(ctx, path, []string{"HEAD"}); err == nil || !strings.Contains(err.Error(), "maxCommitExcludeRevisions") {
		t.Fatalf("merged revision overflow must fail: %v", err)
	}
}

// TestForgeCheckCommits_RealArgvCap preserves exactly 256 tokens and rejects 257 or 300 excludes.
func TestForgeCheckCommits_RealArgvCap(t *testing.T) {
	for _, count := range []int{256, 257, 300} {
		fs := newQuietFlagSet("cap")
		var excludes repeatedStringFlag
		fs.Var(&excludes, "exclude", "")
		args := make([]string, count)
		for i := 0; i < count; i++ {
			args[i] = "--exclude=HEAD"
		}
		_, err := parseInterspersed(fs, args)
		if count == 256 {
			if err != nil || len(excludes) != count {
				t.Fatalf("256 args refused: %v", err)
			}
		} else if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("too many arguments: %d exceeds the 256-argument limit", count)) {
			t.Fatalf("%d argv tokens must fail by name: %v", count, err)
		}
	}
	args := append([]string{"--base=HEAD"}, slices.Repeat([]string{"--exclude=HEAD"}, 300)...)
	if err := runForgeCheckCommits(t.Context(), args); err == nil || !strings.Contains(err.Error(), "too many arguments: 301 exceeds the 256-argument limit") {
		t.Fatalf("public command must preserve cap: %v", err)
	}
}

// TestForgeCheckCommits_Complexity pins every changed Go function to the repository's HISS-04 measure.
func TestForgeCheckCommits_Complexity(t *testing.T) {
	checkCommitComplexityFile(t, "forge.go", []string{"commitRange", "runForgeCheckCommits", "commitExcludeInput", "reportCommitMessageResult"})
	checkCommitComplexityFile(t, "forge_commits_test.go", []string{"checkCommitComplexityFile", "TestForgeCheckCommits_Complexity", "TestForgeCheckCommits_RevisionStdin", "TestForgeCheckCommits_RevisionPayload", "TestForgeCheckCommits_RevisionSpellings", "TestForgeCheckCommits_DottedExcludeArgvBound", "TestForgeCheckCommits_ResultReporting"})
}

// checkCommitComplexityFile checks named functions, refusing absent names as well as exceeded bounds.
func checkCommitComplexityFile(t *testing.T, path string, names []string) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, unit := range hiss.MeasureFile(fset, file) {
		if !slices.Contains(names, unit.Name) {
			continue
		}
		found = append(found, unit.Name)
		t.Logf("%s: %+v", unit.Name, unit.Metrics)
		if exceeded := unit.Metrics.Exceeded(hiss.ComplexityLimits{}); len(exceeded) != 0 {
			t.Errorf("%s exceeds HISS-04: %+v", unit.Name, exceeded)
		}
	}
	for _, name := range names {
		if !slices.Contains(found, name) {
			t.Errorf("complexity contract function missing: %s", name)
		}
	}
}

// TestForgeCheckCommits_RevisionStdin compares option-free negatives with the command-line exclusion form.
func TestForgeCheckCommits_RevisionStdin(t *testing.T) {
	ctx, dir, refs := commitExcludeFixture(t)
	// The third tip is an ancestor of the first: fast-import attached each tip to base.
	refs = []string{refs[0], refs[1], refs[0] + "~1"}
	args := []string{"log", "--format=%H", "refs/heads/base..refs/heads/range"}
	negative, _ := commitExcludeInput(refs)
	want, err := util.RunGit(ctx, dir, append(append(slices.Clone(args), "--not"), append(refs, "--")...)...)
	if err != nil {
		t.Fatal(err)
	}
	input, err := util.WithCommandStdin(ctx, negative)
	if err != nil {
		t.Fatal(err)
	}
	got, err := util.RunGit(input, dir, append(slices.Clone(args), "--stdin", "--")...)
	if err != nil || got != want {
		t.Fatalf("stdin revisions differ from argv: %v", err)
	}

	t.Log("three excluded tips (one ancestor); caret stdin = --not argv; nonempty result")
	if want == "" {
		t.Fatal("fixture must leave commits to compare")
	}
}

// TestForgeCheckCommits_RevisionPayload refuses stdin option lines and pins the Python-compatible shape.
func TestForgeCheckCommits_RevisionPayload(t *testing.T) {
	refs := []string{"refs/heads/tip", "refs/heads/ancestor", "refs/heads/side"}
	input, ranges := commitExcludeInput(refs)
	payload := string(input)
	if len(ranges) != 0 {
		t.Fatalf("simple exclusions must stay off argv: %v", ranges)
	}
	for _, line := range strings.Split(payload, "\n") {
		if strings.HasPrefix(line, "-") {
			t.Fatalf("stdin option line forbidden: %q", line)
		}
	}
	if want := "^" + strings.Join(refs, "\n^") + "\n"; payload != want {
		t.Fatalf("payload %q; want Python form %q", payload, want)
	}
	signed, _ := commitExcludeInput([]string{"^refs/heads/tip", "^", "^^tip", "^--all"})
	if want := "refs/heads/tip\n^^\n^^^tip\n^^--all\n"; string(signed) != want {
		t.Fatalf("invalid signed revisions must not become options or empty input: %q", signed)
	}

}

// TestForgeCheckCommits_RevisionSpellings preserves signed, dotted and parent-set revision semantics.
func TestForgeCheckCommits_RevisionSpellings(t *testing.T) {
	ctx, dir, refs := commitExcludeFixture(t)
	for _, ref := range []string{refs[0], "^" + refs[0], "refs/heads/base.." + refs[0], "refs/heads/base..." + refs[0], "refs/heads/range^!", "refs/heads/range^@", "refs/heads/range^-1"} {
		got, err := commitRange(ctx, dir, "refs/heads/base", "refs/heads/range", []string{ref, refs[2]})
		want := chunkedExcludeRange(t, ctx, dir, []string{ref, refs[2]})
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("revision %q differs from argv: %v", ref, err)
		}
	}
}

// TestForgeCheckCommits_DottedExcludeArgvBound accepts the exact argv cap and refuses the next range.
func TestForgeCheckCommits_DottedExcludeArgvBound(t *testing.T) {
	ctx, dir, _ := commitExcludeFixture(t)
	for _, count := range []int{maxCLIArgs - 8, maxCLIArgs - 7} {
		refs := slices.Repeat([]string{"refs/heads/base..refs/heads/base"}, count)
		_, err := commitRange(ctx, dir, "refs/heads/base", "refs/heads/range", refs)
		if count == maxCLIArgs-8 && err != nil {
			t.Fatalf("exact argv cap: %v", err)
		}
		if count == maxCLIArgs-7 && (err == nil || !strings.Contains(err.Error(), "256-argument limit")) {
			t.Fatalf("argv overflow must fail by name: %v", err)
		}
	}
}

// TestForgeCheckCommits_ResultReporting retains both passing and failing extracted reports.
func TestForgeCheckCommits_ResultReporting(t *testing.T) {
	out, err := captureStdout(t, func() error { return reportCommitMessageResult(nil, 0) })
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("passing result: %v, %s", err, out)
	}
	err = reportCommitMessageResult([]string{"planted failure"}, 3)
	if err == nil || !strings.Contains(err.Error(), "1 of 3 commit(s) fail, first planted failure") {
		t.Fatalf("failing result: %v", err)
	}
}
