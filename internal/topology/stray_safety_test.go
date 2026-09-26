package topology

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// budgetCtx reports cancellation once its Err budget is spent, so a test can end an
// operation between two specific loop iterations without timing races. A negative budget
// never cancels and only counts the checks.
type budgetCtx struct {
	context.Context
	calls  int
	budget int
}

func newBudgetCtx(budget int) *budgetCtx {
	return &budgetCtx{Context: context.Background(), budget: budget}
}

func (c *budgetCtx) Err() error {
	c.calls++
	if c.budget >= 0 && c.calls > c.budget {
		return context.Canceled
	}
	return nil
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("operator content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeFillerEntries(t *testing.T, dir string, count int) {
	t.Helper()
	for i := range count {
		writeTestFile(t, filepath.Join(dir, fmt.Sprintf("filler-%04d", i)))
	}
}

func findStray(report *TopologyReport, path string) (StrayFile, bool) {
	for _, stray := range report.StrayFiles {
		if stray.Path == path {
			return stray, true
		}
	}
	return StrayFile{}, false
}

func requireSafeStray(t *testing.T, report *TopologyReport, path string) {
	t.Helper()
	stray, ok := findStray(report, path)
	if !ok {
		t.Fatalf("expected %s to be reported as a stray finding", path)
	}
	if !stray.IsSafeToDelete {
		t.Fatalf("tool artifact finding requires review: %+v", stray)
	}
}

// genericOrgEntries plants operator content whose names collide with StrayGovernanceNames
// but which no tool generated (BUG-320). Directories carry a payload file.
func genericOrgEntries(t *testing.T, orgDir string) []string {
	t.Helper()
	dirs := []string{"docs", ".github", ".vscode", "lua", ".config", ".idea", ".agents"}
	files := []string{"Makefile", ".gitignore", ".editorconfig", "CONTRIBUTING.md", "SECURITY.md"}
	entries := make([]string, 0, len(dirs)+len(files))
	for _, dir := range dirs {
		writeTestFile(t, filepath.Join(orgDir, dir, "payload.txt"))
		entries = append(entries, filepath.Join(orgDir, dir))
	}
	for _, file := range files {
		writeTestFile(t, filepath.Join(orgDir, file))
		entries = append(entries, filepath.Join(orgDir, file))
	}
	return entries
}

func TestAuditWorkstationTopology_GenericOrgEntriesRequireManualReview(t *testing.T) {
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "lusoris")
	entries := genericOrgEntries(t, orgDir)
	initTestGit(t, filepath.Join(orgDir, "repo"))

	report, err := AuditWorkstationTopology(context.Background(), devRoot)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	for _, entry := range entries {
		requireUnsafeStray(t, report, entry)
	}
}

func TestCleanWorkstationTopology_PreservesGenericOrgEntries(t *testing.T) {
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "lusoris")
	entries := genericOrgEntries(t, orgDir)

	result, err := CleanWorkstationTopologyDetailed(context.Background(), devRoot, false)
	if err != nil {
		t.Fatalf("CleanWorkstationTopologyDetailed: %v", err)
	}
	if len(result.Cleaned) != 0 {
		t.Fatalf("cleanup removed operator content: %v", result.Cleaned)
	}
	if len(result.Blocked) != len(entries) {
		t.Fatalf("blocked %d findings, want %d: %+v", len(result.Blocked), len(entries), result.Blocked)
	}
	for _, entry := range entries {
		if _, statErr := os.Lstat(entry); statErr != nil {
			t.Errorf("operator content was deleted: %s: %v", entry, statErr)
		}
	}
	assertPathsExist(t, []string{filepath.Join(orgDir, "docs", "payload.txt")})
}

func TestCleanWorkstationTopology_RemovesToolArtifactFiles(t *testing.T) {
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "lusoris")
	artifacts := []string{".standards.yaml", ".standards.lock", ".needs.yaml", "CLAUDE.md", "AGENTS.md", "lefthook.yml"}
	for _, name := range artifacts {
		writeTestFile(t, filepath.Join(orgDir, name))
	}

	report, err := AuditWorkstationTopology(context.Background(), devRoot)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	for _, name := range artifacts {
		requireSafeStray(t, report, filepath.Join(orgDir, name))
	}
	result, err := CleanWorkstationTopologyDetailed(context.Background(), devRoot, false)
	if err != nil {
		t.Fatalf("CleanWorkstationTopologyDetailed: %v", err)
	}
	if len(result.Cleaned) != len(artifacts) || len(result.Blocked) != 0 {
		t.Fatalf("clean result = %+v, want %d removals and no blocked findings", result, len(artifacts))
	}
	for _, name := range artifacts {
		if _, statErr := os.Lstat(filepath.Join(orgDir, name)); !os.IsNotExist(statErr) {
			t.Errorf("tool artifact %s was not removed: %v", name, statErr)
		}
	}
}

// A tool artifact name on a directory is still a directory: removal would recurse.
func TestAuditWorkstationTopology_ToolNamedDirectoryRequiresManualReview(t *testing.T) {
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "lusoris")
	toolDir := filepath.Join(orgDir, "CLAUDE.md")
	writeTestFile(t, filepath.Join(toolDir, "notes.txt"))

	result, err := CleanWorkstationTopologyDetailed(context.Background(), devRoot, false)
	if err != nil {
		t.Fatalf("CleanWorkstationTopologyDetailed: %v", err)
	}
	if len(result.Cleaned) != 0 || len(result.Blocked) != 1 {
		t.Fatalf("tool-named directory result = %+v, want one blocked finding", result)
	}
	assertPathsExist(t, []string{filepath.Join(toolDir, "notes.txt")})
}

func TestCleanWorkstationTopology_SymlinkClassification(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires optional Windows privileges")
	}
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "lusoris")
	target := filepath.Join(t.TempDir(), "AGENTS.md")
	writeTestFile(t, target)
	toolLink := filepath.Join(orgDir, "CLAUDE.md")
	genericLink := filepath.Join(orgDir, "docs")
	if err := os.MkdirAll(orgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{toolLink, genericLink} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}

	result, err := CleanWorkstationTopologyDetailed(context.Background(), devRoot, false)
	if err != nil {
		t.Fatalf("CleanWorkstationTopologyDetailed: %v", err)
	}
	if len(result.Cleaned) != 1 || result.Cleaned[0] != toolLink {
		t.Fatalf("cleaned = %v, want only the tool-named symlink", result.Cleaned)
	}
	if len(result.Blocked) != 1 || result.Blocked[0].Path != genericLink {
		t.Fatalf("blocked = %+v, want only the generic-named symlink", result.Blocked)
	}
	if !isSymlink(genericLink) {
		t.Fatal("generic-named symlink was removed")
	}
	assertPathsExist(t, []string{target})
}

func TestAuditWorkstationTopology_DevRootGenericFileRequiresManualReview(t *testing.T) {
	devRoot := t.TempDir()
	makefile := filepath.Join(devRoot, "Makefile")
	claude := filepath.Join(devRoot, "CLAUDE.md")
	writeTestFile(t, makefile)
	writeTestFile(t, claude)

	report, err := AuditWorkstationTopology(context.Background(), devRoot)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	requireUnsafeStray(t, report, makefile)
	requireSafeStray(t, report, claude)
}

func TestManualReviewReason(t *testing.T) {
	cases := []struct {
		name     string
		lower    string
		mode     fs.FileMode
		reviewed bool
	}{
		{name: "tool artifact regular file", lower: ".standards.yaml", mode: 0, reviewed: false},
		{name: "tool artifact symlink", lower: "claude.md", mode: fs.ModeSymlink, reviewed: false},
		{name: "tool artifact directory", lower: ".standards.yaml", mode: fs.ModeDir, reviewed: true},
		{name: "tool artifact named pipe", lower: "claude.md", mode: fs.ModeNamedPipe, reviewed: true},
		{name: "generic regular file", lower: "makefile", mode: 0, reviewed: true},
		{name: "generic directory", lower: "docs", mode: fs.ModeDir, reviewed: true},
		{name: "empty name", lower: "", mode: 0, reviewed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, reviewed := manualReviewReason(tc.lower, tc.mode, "dev root")
			if reviewed != tc.reviewed {
				t.Fatalf("manualReviewReason(%q, %v) reviewed = %v, want %v", tc.lower, tc.mode, reviewed, tc.reviewed)
			}
			if reviewed == (reason == "") {
				t.Fatalf("reason %q does not match reviewed = %v", reason, reviewed)
			}
		})
	}
}

// The final boundary refuses a directory the audit could not have marked safe, such as a
// file replaced by a directory between audit and removal.
func TestVerifyDeletionSafety_RejectsNonGitDirectory(t *testing.T) {
	devRoot := t.TempDir()
	swapped := filepath.Join(devRoot, "lusoris", "CLAUDE.md")
	writeTestFile(t, filepath.Join(swapped, "payload.txt"))
	if err := verifyDeletionSafety(devRoot, swapped); err == nil {
		t.Fatal("a non-git directory passed the final deletion boundary")
	}
	headless := filepath.Join(devRoot, "lusoris", ".git")
	if err := os.MkdirAll(headless, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyDeletionSafety(devRoot, headless); err != nil {
		t.Fatalf("proven-headless git metadata was rejected: %v", err)
	}
}

func writeCancellationTree(t *testing.T) string {
	t.Helper()
	devRoot := t.TempDir()
	for _, name := range []string{"CLAUDE.md", ".standards.yaml", "lefthook.yml"} {
		writeTestFile(t, filepath.Join(devRoot, name))
	}
	orgDir := filepath.Join(devRoot, "lusoris")
	writeTestFile(t, filepath.Join(orgDir, ".needs.yaml"))
	initTestGit(t, filepath.Join(orgDir, "repo"))
	return devRoot
}

func TestAuditWorkstationTopology_EveryCheckpointHonoursCancellation(t *testing.T) {
	devRoot := writeCancellationTree(t)
	counter := newBudgetCtx(-1)
	if _, err := AuditWorkstationTopology(counter, devRoot); err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	// One entry check, one per dev-root entry (4) and two per organization entry (2 x 2).
	if counter.calls < 9 {
		t.Fatalf("audit consulted the context %d times; scan loops do not check it", counter.calls)
	}
	for budget := range counter.calls {
		report, err := AuditWorkstationTopology(newBudgetCtx(budget), devRoot)
		if !errors.Is(err, context.Canceled) || report != nil {
			t.Fatalf("budget %d: report = %v, err = %v; want no report and context.Canceled", budget, report, err)
		}
	}
	if _, err := AuditWorkstationTopology(newBudgetCtx(counter.calls), devRoot); err != nil {
		t.Fatalf("audit failed with exactly enough budget: %v", err)
	}
}

func auditCheckpoints(t *testing.T, devRoot string) int {
	t.Helper()
	counter := newBudgetCtx(-1)
	if _, err := AuditWorkstationTopology(counter, devRoot); err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	return counter.calls
}

func countExisting(t *testing.T, paths []string) int {
	t.Helper()
	existing := 0
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			existing++
		}
	}
	return existing
}

func TestCleanWorkstationTopologyDetailed_CancellationStopsRemovalMidLoop(t *testing.T) {
	devRoot := writeCancellationTree(t)
	strays := []string{
		filepath.Join(devRoot, "CLAUDE.md"),
		filepath.Join(devRoot, ".standards.yaml"),
		filepath.Join(devRoot, "lefthook.yml"),
		filepath.Join(devRoot, "lusoris", ".needs.yaml"),
	}
	auditCalls := auditCheckpoints(t, devRoot)

	result, err := CleanWorkstationTopologyDetailed(newBudgetCtx(auditCalls+1), devRoot, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled after the first removal", err)
	}
	if result == nil || len(result.Cleaned) != 1 {
		t.Fatalf("result = %+v, want exactly one completed removal", result)
	}
	if got := countExisting(t, strays); got != len(strays)-1 {
		t.Fatalf("%d of %d strays remain, want all but one", got, len(strays))
	}
}

func TestCleanWorkstationTopologyDetailed_CancellationBeforeFirstRemoval(t *testing.T) {
	devRoot := writeCancellationTree(t)
	auditCalls := auditCheckpoints(t, devRoot)

	result, err := CleanWorkstationTopologyDetailed(newBudgetCtx(auditCalls), devRoot, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if result == nil || len(result.Cleaned) != 0 {
		t.Fatalf("result = %+v, want no removals", result)
	}
	assertPathsExist(t, []string{filepath.Join(devRoot, "CLAUDE.md"), filepath.Join(devRoot, "lusoris", ".needs.yaml")})

	cleaned, legacyErr := CleanWorkstationTopology(newBudgetCtx(auditCalls), devRoot, false)
	if !errors.Is(legacyErr, context.Canceled) || len(cleaned) != 0 {
		t.Fatalf("legacy clean = %v, %v; want no removals and context.Canceled", cleaned, legacyErr)
	}
}

func TestAuditWorkstationTopology_DevRootScanBound(t *testing.T) {
	cases := []struct {
		name      string
		entries   int
		truncated bool
	}{
		{name: "exactly MaxScanEntries", entries: MaxScanEntries, truncated: false},
		{name: "one past MaxScanEntries", entries: MaxScanEntries + 1, truncated: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			devRoot := t.TempDir()
			writeFillerEntries(t, devRoot, tc.entries)
			report, err := AuditWorkstationTopology(context.Background(), devRoot)
			if err != nil {
				t.Fatalf("AuditWorkstationTopology: %v", err)
			}
			if report.Truncated != tc.truncated {
				t.Fatalf("Truncated = %v, want %v (reasons %v)", report.Truncated, tc.truncated, report.TruncationReasons)
			}
			if tc.truncated != (len(report.TruncationReasons) == 1) {
				t.Fatalf("truncation reasons = %v", report.TruncationReasons)
			}
			if tc.truncated && !strings.Contains(report.TruncationReasons[0], "dev root") {
				t.Fatalf("reason does not name the dev root: %v", report.TruncationReasons)
			}
		})
	}
}

func TestAuditWorkstationTopology_OrgScanBoundReportsTruncation(t *testing.T) {
	devRoot := t.TempDir()
	writeFillerEntries(t, filepath.Join(devRoot, "local"), MaxScanEntries+1)

	report, err := AuditWorkstationTopology(context.Background(), devRoot)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	if !report.Truncated || len(report.TruncationReasons) != 1 ||
		!strings.Contains(report.TruncationReasons[0], "organization container local") {
		t.Fatalf("truncation = %v %v, want one organization container reason", report.Truncated, report.TruncationReasons)
	}
}

func chmodUnreadable(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs do not implement POSIX chmod permission denial")
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil && !os.IsNotExist(err) {
			t.Errorf("restore permissions: %v", err)
		}
	})
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("current user can read chmod-000 directories")
	}
}

func TestAuditWorkstationTopology_UnreadableOrgReportsTruncation(t *testing.T) {
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "lusoris")
	writeTestFile(t, filepath.Join(orgDir, "CLAUDE.md"))
	chmodUnreadable(t, orgDir)

	report, err := AuditWorkstationTopology(context.Background(), devRoot)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	if !report.Truncated || len(report.TruncationReasons) != 1 ||
		!strings.Contains(report.TruncationReasons[0], "organization container lusoris could not be read") {
		t.Fatalf("truncation = %v %v, want the unreadable organization container", report.Truncated, report.TruncationReasons)
	}
}

func TestAuditWorkstationTopology_UninspectableRootRepoReportsTruncation(t *testing.T) {
	devRoot := t.TempDir()
	rogue := filepath.Join(devRoot, "rogue")
	initTestGit(t, rogue)
	chmodUnreadable(t, rogue)

	report, err := AuditWorkstationTopology(context.Background(), devRoot)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	if len(report.Violations) != 0 {
		t.Fatalf("violations = %v, want none recorded without evidence", report.Violations)
	}
	if !report.Truncated || !strings.Contains(strings.Join(report.TruncationReasons, "\n"), "rogue: DEV-01 not evaluated") {
		t.Fatalf("truncation = %v %v, want the uninspectable directory", report.Truncated, report.TruncationReasons)
	}
}

func TestCleanWorkstationTopologyDetailed_RefusesTruncatedAudit(t *testing.T) {
	devRoot := t.TempDir()
	claude := filepath.Join(devRoot, "CLAUDE.md")
	writeTestFile(t, claude)
	writeFillerEntries(t, devRoot, MaxScanEntries)

	result, err := CleanWorkstationTopologyDetailed(context.Background(), devRoot, false)
	if !errors.Is(err, ErrScanTruncated) || result != nil {
		t.Fatalf("result = %+v, err = %v; want no result and ErrScanTruncated", result, err)
	}
	if !strings.Contains(err.Error(), "MaxScanEntries") {
		t.Fatalf("error does not carry the truncation reason: %v", err)
	}
	assertPathsExist(t, []string{claude})
}

func TestTopologyReportJSON_CompleteScanDeclaresCompleteness(t *testing.T) {
	report, err := AuditWorkstationTopology(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"truncated":false`, `"truncation_reasons":[]`} {
		if !strings.Contains(string(data), field) {
			t.Errorf("report JSON lacks %s: %s", field, data)
		}
	}
}
