package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/buildid"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxRefactorCommits      = 1000
	refactorTestTimeout     = 20 * time.Minute
	refactorWorktreeTimeout = 30 * time.Second
	refactorGitTimeout      = 30 * time.Second
)

var (
	refactorHunk     = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)
	refactorTestFunc = regexp.MustCompile(`^\s*func\s+(Test\w+)\s*\(`)
)

type refactorCommit struct {
	sha    string
	parent string
}

type refactorResult struct {
	sha    string
	status string
	reason string
}

func runForgeRefactorCheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("forge refactor-check", flag.ContinueOnError)
	base := fs.String("base", "", "Base revision of the commit range (required)")
	head := fs.String("head", "HEAD", "Head revision of the commit range")
	repo := fs.String("repo", ".", "Repository directory")
	runTests := fs.Bool("run-tests", false, "Run touched Go package tests at each refactor commit")
	includeBase := fs.Bool("include-base", false, "Include --base; used when the pushed range has no known remote base")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("forge refactor-check accepts no positional arguments: %q", rest)
	}
	if *base == "" {
		return errors.New("usage: standardsctl forge refactor-check --base=<rev> [--head=<rev>] [--run-tests]")
	}
	commits, skipped, err := refactorRange(ctx, *repo, *base, *head, *includeBase)
	if err != nil {
		return err
	}
	results, err := checkRefactorCommits(ctx, *repo, commits, *runTests)
	if err != nil {
		return err
	}
	failed := printRefactorResults(results, skipped)
	if failed > 0 {
		return fmt.Errorf("refactor check failed: %d of %d refactor commit(s)", failed, len(results))
	}
	return nil
}

func refactorRange(ctx context.Context, repo, base, head string, includeBase bool) ([]refactorCommit, int, error) {
	for _, rev := range []string{base, head} {
		if strings.TrimSpace(rev) == "" || strings.HasPrefix(rev, "-") {
			return nil, 0, fmt.Errorf("invalid revision %q", rev)
		}
	}
	args := []string{"rev-list", "--reverse", "--topo-order", "--max-count=" + fmt.Sprint(maxRefactorCommits+1)}
	if includeBase {
		if _, err := refactorGit(ctx, repo, "merge-base", "--is-ancestor", base, head); err != nil {
			return nil, 0, fmt.Errorf("included base %s is not an ancestor of %s: %w", base, head, err)
		}
		args = append(args, head)
	} else {
		args = append(args, base+".."+head)
	}
	out, err := refactorGit(ctx, repo, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("read refactor commit range %s..%s: %w", base, head, err)
	}
	shas := nonemptyLines(out)
	if len(shas) > maxRefactorCommits {
		return nil, 0, fmt.Errorf("commit range exceeds %d commits", maxRefactorCommits)
	}
	return loadRefactorCommits(ctx, repo, shas)
}

func loadRefactorCommits(ctx context.Context, repo string, shas []string) ([]refactorCommit, int, error) {
	commits := make([]refactorCommit, 0, len(shas))
	skipped := 0
	for _, sha := range shas {
		message, err := refactorGit(ctx, repo, "show", "-s", "--format=%B", sha)
		if err != nil {
			return nil, 0, fmt.Errorf("read message for %s: %w", buildid.Short(sha), err)
		}
		if !forge.IsRefactorCommit(string(message)) {
			skipped++
			continue
		}
		parent, err := refactorCommitParent(ctx, repo, sha)
		if err != nil {
			return nil, 0, fmt.Errorf("read first parent for %s: %w", buildid.Short(sha), err)
		}
		commits = append(commits, refactorCommit{sha: sha, parent: parent})
	}
	return commits, skipped, nil
}

func refactorCommitParent(ctx context.Context, repo, sha string) (string, error) {
	line, err := refactorGit(ctx, repo, "rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return "", fmt.Errorf("read commit parent list: %w", err)
	}
	fields := strings.Fields(string(line))
	if len(fields) == 0 || fields[0] != sha {
		return "", errors.New("malformed commit parent list")
	}
	if len(fields) > 1 {
		return fields[1], nil
	}
	return emptyTreeID(ctx, repo)
}

// emptyTreeID asks git for the empty tree's id in repo's object format, the parent a root
// commit is compared against. Without -w nothing is written; standard input is the null device.
func emptyTreeID(ctx context.Context, repo string) (string, error) {
	id, err := refactorGit(ctx, repo, "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", fmt.Errorf("compute empty tree id: %w", err)
	}
	return strings.TrimSpace(string(id)), nil
}

func checkRefactorCommits(ctx context.Context, repo string, commits []refactorCommit, runTests bool) ([]refactorResult, error) {
	results := make([]refactorResult, 0, len(commits))
	for _, commit := range commits {
		result, err := checkRefactorCommit(ctx, repo, commit, runTests)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func checkRefactorCommit(ctx context.Context, repo string, commit refactorCommit, runTests bool) (refactorResult, error) {
	result := refactorResult{sha: commit.sha, status: "PASS", reason: "assertions unchanged"}
	patch, err := refactorGit(ctx, repo, "diff", "--no-ext-diff", "--no-textconv", "--find-renames", "--unified=0", commit.parent, commit.sha, "--", "*_test.go")
	if err != nil {
		return result, fmt.Errorf("diff tests for %s: %w", buildid.Short(commit.sha), err)
	}
	reason, err := refactorAssertionChange(ctx, repo, commit, string(patch))
	if err != nil {
		return result, fmt.Errorf("inspect test changes for %s: %w", buildid.Short(commit.sha), err)
	}
	if reason != "" {
		result.status, result.reason = "FAIL", reason
		return result, nil
	}
	if runTests {
		if reason := runRefactorTests(ctx, repo, commit); reason != "" {
			result.status, result.reason = "FAIL", reason
			return result, nil
		}
		result.reason = "assertions unchanged; touched package tests pass"
	}
	return result, nil
}

type sourceLineSpan struct{ start, end int }

// maxRefactorPatchLines bounds the test diff one refactor commit is checked against (HISS-02).
const maxRefactorPatchLines = 1 << 22

// refactorAssertionChange returns why commit's test diff, patch (unified, no context), breaks
// the assertion lock, or "" when it does not: a removed or added line inside an assertion call
// of the parent's or the commit's file, or a removed test function.
func refactorAssertionChange(ctx context.Context, repo string, commit refactorCommit, patch string) (string, error) {
	lines := strings.Split(patch, "\n")
	if len(lines) > maxRefactorPatchLines {
		return "", fmt.Errorf("test diff exceeds %d lines", maxRefactorPatchLines)
	}
	walk := refactorPatchWalk{repo: repo, commit: commit}
	for i := 0; i < len(lines) && i < maxRefactorPatchLines; i++ {
		reason, err := walk.step(ctx, lines[i])
		if err != nil || reason != "" {
			return reason, err
		}
	}
	return "", nil
}

// refactorPatchWalk follows a unified test diff one line at a time: the file it is in, whether
// it is inside a hunk, and the parent and commit line each body line stands on. Header lines
// are read only before a file's first hunk, so a removed line whose text starts with "-- " is a
// body line.
type refactorPatchWalk struct {
	repo               string
	commit             refactorCommit
	oldPath, newPath   string
	oldLine, newLine   int
	oldSpans, newSpans []sourceLineSpan
	loaded, inHunk     bool
}

// step reads one diff line and returns the lock violation it shows, if any.
func (w *refactorPatchWalk) step(ctx context.Context, line string) (string, error) {
	switch {
	case strings.HasPrefix(line, "diff --git a/"):
		return "", w.startFile(line)
	case strings.HasPrefix(line, "@@ "):
		return "", w.startHunk(ctx, line)
	case !w.inHunk:
		w.header(line)
		return "", nil
	}
	return w.body(line), nil
}

// startFile resets the walk for the file a "diff --git" line opens.
func (w *refactorPatchWalk) startFile(line string) error {
	oldPath, newPath, err := refactorDiffPaths(line)
	if err != nil {
		return err
	}
	*w = refactorPatchWalk{repo: w.repo, commit: w.commit, oldPath: oldPath, newPath: newPath}
	return nil
}

// header records a side the file header marks as absent.
func (w *refactorPatchWalk) header(line string) {
	switch line {
	case "--- /dev/null":
		w.oldPath = "/dev/null"
	case "+++ /dev/null":
		w.newPath = "/dev/null"
	}
}

// startHunk loads the file's assertion spans at its first hunk and moves to the hunk's lines.
func (w *refactorPatchWalk) startHunk(ctx context.Context, line string) error {
	match := refactorHunk.FindStringSubmatch(line)
	if len(match) != 3 {
		return fmt.Errorf("malformed test diff hunk header %q", line)
	}
	if !w.loaded {
		oldSpans, newSpans, err := loadRefactorAssertionSpans(ctx, w.repo, w.commit, w.oldPath, w.newPath)
		if err != nil {
			return err
		}
		w.oldSpans, w.newSpans, w.loaded = oldSpans, newSpans, true
	}
	oldLine, newLine, err := refactorHunkLines(match)
	if err != nil {
		return err
	}
	w.oldLine, w.newLine, w.inHunk = oldLine, newLine, true
	return nil
}

// body checks one hunk line against the assertion spans and advances the line counters.
func (w *refactorPatchWalk) body(line string) string {
	switch {
	case strings.HasPrefix(line, "-"):
		reason := removedTestLine(line[1:], w.oldPath, w.oldLine, w.oldSpans)
		w.oldLine++
		return reason
	case strings.HasPrefix(line, "+"):
		reason := addedTestLine(w.newPath, w.newLine, w.newSpans)
		w.newLine++
		return reason
	case strings.HasPrefix(line, " "):
		w.oldLine++
		w.newLine++
	}
	return ""
}

func removedTestLine(text, path string, line int, assertions []sourceLineSpan) string {
	if containsSourceLine(assertions, line) {
		return fmt.Sprintf("%s:%d removed or edited assertion line", path, line)
	}
	if match := refactorTestFunc.FindStringSubmatch(text); len(match) == 2 {
		return fmt.Sprintf("%s:%d removed test function %s", path, line, match[1])
	}
	return ""
}

func addedTestLine(path string, line int, assertions []sourceLineSpan) string {
	if containsSourceLine(assertions, line) {
		return fmt.Sprintf("%s:%d added or edited assertion line", path, line)
	}
	return ""
}

func refactorHunkLines(match []string) (int, int, error) {
	oldLine, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, 0, fmt.Errorf("parse old test diff line: %w", err)
	}
	newLine, err := strconv.Atoi(match[2])
	if err != nil {
		return 0, 0, fmt.Errorf("parse new test diff line: %w", err)
	}
	return oldLine, newLine, nil
}

func refactorDiffPaths(line string) (string, string, error) {
	oldPath, newPath, ok := strings.Cut(strings.TrimPrefix(line, "diff --git a/"), " b/")
	if !ok {
		return "", "", fmt.Errorf("malformed test diff header %q", line)
	}
	return oldPath, newPath, nil
}

func loadRefactorAssertionSpans(ctx context.Context, repo string, commit refactorCommit, oldPath, newPath string) ([]sourceLineSpan, []sourceLineSpan, error) {
	var oldSpans, newSpans []sourceLineSpan
	if oldPath != "/dev/null" {
		data, err := refactorGit(ctx, repo, "show", commit.parent+":"+oldPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read parent test file %s: %w", oldPath, err)
		}
		oldSpans, err = assertionLineSpans(oldPath, data)
		if err != nil {
			return nil, nil, err
		}
	}
	if newPath != "/dev/null" {
		data, err := refactorGit(ctx, repo, "show", commit.sha+":"+newPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read commit test file %s: %w", newPath, err)
		}
		newSpans, err = assertionLineSpans(newPath, data)
		if err != nil {
			return nil, nil, err
		}
	}
	return oldSpans, newSpans, nil
}

func assertionLineSpans(path string, data []byte) ([]sourceLineSpan, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, data, 0)
	if err != nil {
		return nil, fmt.Errorf("parse test file %s: %w", path, err)
	}
	spans := make([]sourceLineSpan, 0)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !isRefactorAssertion(call.Fun) {
			return true
		}
		spans = append(spans, sourceLineSpan{
			start: fset.Position(call.Pos()).Line,
			end:   fset.Position(call.End()).Line,
		})
		return true
	})
	return spans, nil
}

func isRefactorAssertion(function ast.Expr) bool {
	selector, ok := function.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	name := selector.Sel.Name
	switch packageName.Name {
	case "t":
		return name == "Error" || name == "Errorf" || name == "Fatal" || name == "Fatalf"
	case "assert", "require":
		return true
	case "cmp":
		return name == "Diff"
	default:
		return false
	}
}

func containsSourceLine(spans []sourceLineSpan, line int) bool {
	for _, span := range spans {
		if line >= span.start && line <= span.end {
			return true
		}
	}
	return false
}

func runRefactorTests(ctx context.Context, repo string, commit refactorCommit) string {
	files, err := refactorChangedGoFiles(ctx, repo, commit)
	if err != nil {
		return "cannot identify touched Go packages: " + err.Error()
	}
	packages := packageDirs(files)
	if len(packages) == 0 {
		return "no touched Go package; suite not run"
	}
	worktree, err := os.MkdirTemp("", "praetor-refactor-")
	if err != nil {
		return "create temporary worktree: " + err.Error()
	}
	if err := refactorWorktree(ctx, repo, "add", "--detach", worktree, commit.sha); err != nil {
		removeErr := os.RemoveAll(worktree)
		if removeErr != nil {
			return fmt.Sprintf("create temporary worktree: %v; remove temporary directory: %v", err, removeErr)
		}
		return "create temporary worktree: " + err.Error()
	}
	args := append([]string{"test", "-timeout", refactorTestTimeout.String()}, packages...)
	testErr := refactorGoTest(ctx, worktree, args)
	// The cleanup runs after a cancelled check too, so it keeps ctx's values but not its cancel.
	removeWorktreeErr := refactorWorktree(context.WithoutCancel(ctx), repo, "remove", "--force", worktree)
	removeDirErr := os.RemoveAll(worktree)
	if removeWorktreeErr != nil || removeDirErr != nil {
		return fmt.Sprintf("temporary worktree cleanup failed: git=%v directory=%v", removeWorktreeErr, removeDirErr)
	}
	if testErr != nil {
		return "touched package tests failed or could not run: " + testErr.Error()
	}
	return ""
}

func refactorChangedGoFiles(ctx context.Context, repo string, commit refactorCommit) ([]string, error) {
	out, err := refactorGit(ctx, repo, "diff", "--name-only", "-z", commit.parent, commit.sha, "--", "*.go")
	if err != nil {
		return nil, fmt.Errorf("list changed Go files: %w", err)
	}
	return strings.Split(strings.Trim(string(out), "\x00"), "\x00"), nil
}

func packageDirs(files []string) []string {
	seen := map[string]bool{}
	packages := make([]string, 0, len(files))
	for _, file := range files {
		if file == "" {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(file))
		pkg := "./" + strings.TrimPrefix(dir, "./")
		if dir == "." {
			pkg = "."
		}
		if !seen[pkg] {
			seen[pkg] = true
			packages = append(packages, pkg)
		}
	}
	return packages
}

func printRefactorResults(results []refactorResult, skipped int) int {
	failed := 0
	for _, result := range results {
		fmt.Printf("%s %s %s\n", result.status, buildid.Short(result.sha), result.reason)
		if result.status == "FAIL" {
			failed++
		}
	}
	fmt.Printf("SKIPPED %d non-refactor commit(s)\n", skipped)
	return failed
}

func nonemptyLines(data []byte) []string {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

// refactorGit runs one read-only git inspection in repo through util.RunGitProbeWithin, which
// isolates it from the ambient git configuration and hooks (HISS-19), bounded by
// refactorGitTimeout (HISS-02).
func refactorGit(ctx context.Context, repo string, args ...string) ([]byte, error) {
	result, err := util.RunGitProbeWithin(ctx, repo, util.MaxCommandOutputBytes, refactorGitTimeout, args...)
	if err != nil {
		return nil, util.CommandDiagnostic(err, result.Stderr)
	}
	return result.Stdout, nil
}

// refactorWorktree runs one git worktree subcommand in repo, bounded by refactorWorktreeTimeout.
// util.RunGitBytes drops the variables that bind git to the pushing repository, which a pre-push
// hook exports (BUG-886).
func refactorWorktree(ctx context.Context, repo string, args ...string) error {
	bounded, cancel := context.WithTimeout(ctx, refactorWorktreeTimeout)
	defer cancel()
	result, err := util.RunGitBytes(bounded, repo, util.MaxCommandOutputBytes, append([]string{"worktree"}, args...)...)
	if err != nil {
		return util.CommandDiagnostic(err, result.Stderr)
	}
	return nil
}

// refactorGoTest runs go with args in worktree, bounded by refactorTestTimeout. go test reports
// failures on standard output, so a failure carries both streams.
func refactorGoTest(ctx context.Context, worktree string, args []string) error {
	bounded, cancel := context.WithTimeout(ctx, refactorTestTimeout)
	defer cancel()
	result, err := util.RunCommandBytes(bounded, worktree, "go", util.MaxCommandOutputBytes, args...)
	if err != nil {
		return util.CommandDiagnostic(err, append(result.Stdout, result.Stderr...))
	}
	return nil
}
