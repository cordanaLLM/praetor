package hiss

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// attributionTestTimeout bounds one attribution test's git commands and scans (HISS-02).
const attributionTestTimeout = 60 * time.Second

// pythonSpin is a Python module whose unbounded loop is one HISS-02 finding.
const pythonSpin = "def spin():\n    while True:\n        pass\n"

// runAttributionGit runs one fixture git command in dir under the hermetic environment.
func runAttributionGit(ctx context.Context, t *testing.T, dir string, args ...string) string {
	t.Helper()
	gitCtx, err := util.WithCommandEnvironment(ctx, testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatalf("git environment: %v", err)
	}
	out, err := util.RunGit(gitCtx, dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(out)
}

// commitAttributionFixture makes dir a repository, writes files, commits them and returns the
// commit, the one a baseline recorded there names.
func commitAttributionFixture(ctx context.Context, t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	runAttributionGit(ctx, t, dir, "init", "-q")
	for rel, content := range files {
		writeAttributionFile(t, dir, rel, content)
	}
	runAttributionGit(ctx, t, dir, "add", "-A")
	runAttributionGit(ctx, t, dir, "commit", "-q", "-m", "fixture")
	return runAttributionGit(ctx, t, dir, "rev-parse", "HEAD")
}

func writeAttributionFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ratchetAgainst scans dir, fingerprints the findings as the audit does, evaluates them against
// b and attributes the rejection. The findings are keyed by file so a test can look them up.
func ratchetAgainst(ctx context.Context, t *testing.T, dir string, b *baseline.Baseline) (*baseline.RatchetResult, map[string]baseline.Attribution) {
	t.Helper()
	return ratchetAttributedWith(ctx, t, dir, b, ScanOptions{})
}

// ratchetAttributedWith is ratchetAgainst with the attribution run under opts, while the ratchet's
// own scan keeps the defaults.
func ratchetAttributedWith(ctx context.Context, t *testing.T, dir string, b *baseline.Baseline, opts ScanOptions) (*baseline.RatchetResult, map[string]baseline.Attribution) {
	t.Helper()
	rep, err := Scan(ctx, dir, ScanOptions{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	current := ConvertToBaseline(rep.Violations)
	for i := range current {
		current[i].Fingerprint = fmt.Sprintf("%s:%d:%s", current[i].FilePath, current[i].LineNumber, current[i].RuleID)
	}
	res := baseline.EvaluateRatchet(b, current, nil)
	AttributeRatchet(ctx, dir, opts, b, current, res)
	byFile := make(map[string]baseline.Attribution, len(res.Attribution))
	for i := 0; i < len(res.Attribution); i++ {
		byFile[baseline.NormalizePath(res.NewViolations[i].FilePath)] = res.Attribution[i]
	}
	return res, byFile
}

// Positive (#599): a finding in a file the baseline's commit holds unchanged is attributed to a
// changed check; a Go call cycle is found at the commit because its whole package is read; a
// finding in a file the commit does not hold is introduced.
func TestAttributeRatchet_Positive_UnchangedCodeBlamesTheCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), attributionTestTimeout)
	defer cancel()
	dir := t.TempDir()
	commit := commitAttributionFixture(ctx, t, dir, map[string]string{
		"hooks/guard.py": pythonSpin,
		"pkg/a.go":       "package pkg\n\nfunc a() { b() }\n",
		"pkg/b.go":       "package pkg\n\nfunc b() { a() }\n",
	})
	writeAttributionFile(t, dir, "hooks/added.py", pythonSpin)

	// The baseline was recorded at the commit by an engine that reported nothing there.
	b := &baseline.Baseline{Version: 1, CommitSHA: commit, Infractions: []baseline.Infraction{}}
	res, byFile := ratchetAgainst(ctx, t, dir, b)
	if res.Passed || res.AttributionNote != "" || res.AttributionCommit != commit {
		t.Fatalf("attribution did not run: passed=%v note=%q commit=%q", res.Passed, res.AttributionNote, res.AttributionCommit)
	}
	want := map[string]baseline.Attribution{
		"hooks/guard.py": baseline.AttributionCheckChanged,
		"pkg/a.go":       baseline.AttributionCheckChanged,
		"hooks/added.py": baseline.AttributionIntroduced,
	}
	for file, attribution := range want {
		if got, ok := byFile[file]; !ok || got != attribution {
			t.Errorf("%s: attribution %q (listed %v), want %q; all: %v", file, got, ok, attribution, byFile)
		}
	}
	if summary := res.Summary(); !strings.Contains(summary, "(check added or changed since the baseline)") {
		t.Errorf("Summary() does not name the changed check:\n%s", summary)
	}
}

// Negative (#599): a finding added to code that changed after the baseline's commit is
// introduced, and a commit the clone does not hold, a baseline that records none and a name that
// is not a commit leave every finding unattributed with the reason.
func TestAttributeRatchet_Negative_ChangedCodeAndUnreadableCommits(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), attributionTestTimeout)
	defer cancel()
	dir := t.TempDir()
	commit := commitAttributionFixture(ctx, t, dir, map[string]string{"lib.py": "def calm():\n    return 1\n"})
	writeAttributionFile(t, dir, "lib.py", "def calm():\n    return 1\n\n"+pythonSpin)

	res, byFile := ratchetAgainst(ctx, t, dir, &baseline.Baseline{Version: 1, CommitSHA: commit, Infractions: []baseline.Infraction{}})
	if got := byFile["lib.py"]; got != baseline.AttributionIntroduced {
		t.Errorf("a loop added after the baseline's commit is %q, want introduced (note %q)", got, res.AttributionNote)
	}

	for name, sha := range map[string]string{
		"not in this clone": strings.Repeat("0", 40),
		"no commit":         "",
		"not a commit name": "-output=x",
	} {
		b := &baseline.Baseline{Version: 1, CommitSHA: sha, Infractions: []baseline.Infraction{}}
		res, _ := ratchetAgainst(ctx, t, dir, b)
		if res.Attribution != nil || res.AttributionNote == "" {
			t.Errorf("%s: attribution %v with note %q, want none with a reason", name, res.Attribution, res.AttributionNote)
		}
		if summary := res.Summary(); strings.Contains(summary, "(new)") || !strings.Contains(summary, res.AttributionNote) {
			t.Errorf("%s: unattributed Summary() claims a new finding or drops the reason:\n%s", name, summary)
		}
	}
}

// Positive: the staged copy of the commit is its own scope. With the temporary directory inside
// a git work tree that ignores it, git lists nothing there, and the staged scan used to read no
// file, so a finding in code unchanged since the commit was tagged new.
func TestAttributeRatchet_Positive_StagingInsideAnIgnoredWorkTree(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), attributionTestTimeout)
	defer cancel()
	dir := t.TempDir()
	commit := commitAttributionFixture(ctx, t, dir, map[string]string{
		".gitignore":     "tmp/\n",
		"hooks/guard.py": pythonSpin,
	})
	staging := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	// os.TempDir reads TMPDIR on Unix and TMP or TEMP on Windows.
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, staging)
	}

	res, byFile := ratchetAgainst(ctx, t, dir, &baseline.Baseline{Version: 1, CommitSHA: commit, Infractions: []baseline.Infraction{}})
	if got := byFile["hooks/guard.py"]; got != baseline.AttributionCheckChanged {
		t.Errorf("unchanged code staged in an ignored temporary directory is %q, want check-changed (note %q):\n%s", got, res.AttributionNote, res.Summary())
	}
}

// Negative: a staged scan that leaves a file unexamined never makes a finding new. A staged file
// the attribution's policy skips, and a file of the commit that does not parse, leave every
// finding untraced with the reason.
func TestAttributeRatchet_Negative_IncompleteStagedScanIsNeutral(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), attributionTestTimeout)
	defer cancel()
	skipped := t.TempDir()
	commit := commitAttributionFixture(ctx, t, skipped, map[string]string{"hooks/guard.py": pythonSpin})
	res, _ := ratchetAttributedWith(ctx, t, skipped, &baseline.Baseline{Version: 1, CommitSHA: commit, Infractions: []baseline.Infraction{}},
		ScanOptions{IgnoreDirs: []string{"hooks"}})
	assertUntraced(t, "a staged file the scan skipped", res, "read 0 of the 1 files")

	unparsed := t.TempDir()
	commit = commitAttributionFixture(ctx, t, unparsed, map[string]string{"pkg/a.go": "package pkg\n\nfunc a( {\n"})
	writeAttributionFile(t, unparsed, "pkg/a.go", "package pkg\n\nfunc a() { a() }\n")
	res, _ = ratchetAgainst(ctx, t, unparsed, &baseline.Baseline{Version: 1, CommitSHA: commit, Infractions: []baseline.Infraction{}})
	assertUntraced(t, "a committed file that does not parse", res, "left part of its files unexamined")
}

// assertUntraced fails unless res carries new violations, none of them attributed, a note
// containing reason, and a Summary that names the note and no new finding.
func assertUntraced(t *testing.T, name string, res *baseline.RatchetResult, reason string) {
	t.Helper()
	if len(res.NewViolations) == 0 {
		t.Fatalf("%s: the fixture produced no new violation", name)
	}
	if res.Attribution != nil || !strings.Contains(res.AttributionNote, reason) {
		t.Errorf("%s: attribution %v with note %q, want none with a note naming %q", name, res.Attribution, res.AttributionNote, reason)
	}
	if summary := res.Summary(); strings.Contains(summary, "(new)") || !strings.Contains(summary, res.AttributionNote) {
		t.Errorf("%s: Summary() claims a new finding or drops the reason:\n%s", name, summary)
	}
}

// Boundary: the tree listing keeps only regular, scannable, in-root blobs within the size bound,
// a Go file of a named package or a named file; the root's own pathspec is "./"; a result with
// no new violation is left alone.
func TestAttributeRatchet_Boundary_TreeRecordsAndScope(t *testing.T) {
	object := strings.Repeat("a", 40)
	listing := strings.Join([]string{
		"100644 blob " + object + "      12\tpkg/a.go",
		"100755 blob " + object + "      12\tpkg/b.go",
		"100644 blob " + object + "      12\tpkg/notes.md",
		"120000 blob " + object + "      12\tpkg/link.go",
		"160000 commit " + object + "       -\tpkg/sub",
		"040000 tree " + object + "       -\tpkg/deeper",
		fmt.Sprintf("100644 blob %s %d\tpkg/huge.go", object, MaxScanFileSize+1),
		"100644 blob " + object + "      12\t../escape.py",
		"100644 blob " + object + "      12\tlib.py",
		"100644 blob " + object + "      12\tother.py",
		"garbage",
	}, "\x00")
	got, err := selectCommittedFiles(listing, map[string]bool{"lib.py": true}, map[string]bool{"pkg": true})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	var paths []string
	for _, entry := range got {
		paths = append(paths, entry.path)
	}
	if strings.Join(paths, ",") != "pkg/a.go,pkg/b.go,lib.py" {
		t.Errorf("selected %v, want pkg/a.go, pkg/b.go and lib.py only", paths)
	}
	assertBlobBound(t, object)
	if directoryPathspec(".") != "./" || directoryPathspec("pkg/x") != "./pkg/x/" {
		t.Errorf("directory pathspecs: %q %q", directoryPathspec("."), directoryPathspec("pkg/x"))
	}

	files, packages := attributionScope([]baseline.Infraction{
		{RuleID: "HISS-01", FilePath: "pkg\\a.go"},
		{RuleID: "HISS-02", FilePath: "pkg/c.go"},
		{RuleID: "HISS-01", FilePath: "tool.py"},
	})
	if !packages["pkg"] || len(packages) != 1 || !files["pkg/c.go"] || !files["tool.py"] || len(files) != 2 {
		t.Errorf("scope: files %v packages %v", files, packages)
	}

	passed := &baseline.RatchetResult{Passed: true}
	AttributeRatchet(t.Context(), t.TempDir(), ScanOptions{}, &baseline.Baseline{CommitSHA: object}, nil, passed)
	AttributeRatchet(t.Context(), t.TempDir(), ScanOptions{}, &baseline.Baseline{CommitSHA: object}, nil, nil)
	if passed.Attribution != nil || passed.AttributionNote != "" {
		t.Errorf("a result without new violations was attributed: %+v", passed)
	}
}

// assertBlobBound checks that exactly maxAttributedBlobs files of a package are copied, and one
// more fails the selection instead of copying the package in part.
func assertBlobBound(t *testing.T, object string) {
	t.Helper()
	records := make([]string, 0, maxAttributedBlobs+1)
	for i := 0; i < maxAttributedBlobs; i++ {
		records = append(records, fmt.Sprintf("100644 blob %s 12\tpkg/f%04d.go", object, i))
	}
	packages := map[string]bool{"pkg": true}
	if got, err := selectCommittedFiles(strings.Join(records, "\x00"), nil, packages); err != nil || len(got) != maxAttributedBlobs {
		t.Errorf("at the bound: %d files, error %v; want %d and none", len(got), err, maxAttributedBlobs)
	}
	records = append(records, fmt.Sprintf("100644 blob %s 12\tpkg/over.go", object))
	if got, err := selectCommittedFiles(strings.Join(records, "\x00"), nil, packages); err == nil || got != nil {
		t.Errorf("past the bound: %d files, error %v; want an error and no partial list", len(got), err)
	}
}
