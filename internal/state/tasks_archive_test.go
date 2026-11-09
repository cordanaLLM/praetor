package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// archiveTestReadLimit bounds each fixture archive read; the archives these tests write are a few hundred bytes.
const archiveTestReadLimit = 1 << 20

// TestTaskArchiveMonthlyAllFiles moves completed records once, preserving live prose.
func TestTaskArchiveMonthlyAllFiles(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, WorkingDirName)
	fixtures := map[string]string{
		"OPEN.md":    "# Open\n- [ ] pending\n- [x] shipped (completed: 2026-09-12)\n",
		"BACKLOG.md": "# Later\n- [ ] deferred\n- [x] delivered (completed: 2026-08-01)\n",
		"BUGS.md":    defaultBugsMD() + "| `BUG-001` | fixed | p2 | resolved | file | fixed 2026-07-02 |\n",
	}
	for name, content := range fixtures {
		writeIntegrityFile(t, filepath.Join(dir, name), content)
	}
	count, err := ArchiveCompletedTasks(root, "fixture")
	if err != nil || count != 3 {
		t.Fatalf("archive = %d, %v; want three records", count, err)
	}
	assertMonthlyArchiveFiles(t, dir)
	if got := readLedgerFile(t, filepath.Join(dir, "OPEN.md")); got != "# Open\n- [ ] pending\n" {
		t.Fatalf("live OPEN changed unrelated content: %q", got)
	}
	if got := readLedgerFile(t, filepath.Join(dir, "BACKLOG.md")); got != "# Later\n- [ ] deferred\n" {
		t.Fatalf("live BACKLOG changed unrelated content: %q", got)
	}
	if got := readLedgerFile(t, filepath.Join(dir, "BUGS.md")); !strings.Contains(got, bugTableHeader) || strings.Contains(got, "BUG-001") {
		t.Fatalf("live BUGS lost header or retained resolved row: %q", got)
	}
	assertTaskArchiveNoChange(t, root)
}

// assertMonthlyArchiveFiles requires every expected month before checking the remaining live text.
func assertMonthlyArchiveFiles(t *testing.T, dir string) {
	t.Helper()
	archiveDir := filepath.Join(dir, "archive")
	for _, name := range []string{"OPEN-2026-09.md", "BACKLOG-2026-08.md", "BUGS-2026-07.md"} {
		validateMonthlyArchiveName(t, name)
		// The confined read refuses a name that leaves the archive directory.
		data, err := util.ReadConfinedLimited(archiveDir, name, archiveTestReadLimit)
		if err != nil || len(data) == 0 {
			t.Fatalf("monthly archive %s missing: %v", name, err)
		}
	}
}

// validateMonthlyArchiveName validates the archive file name against the monthly format written by tasks_archive.
func validateMonthlyArchiveName(t *testing.T, name string) {
	t.Helper()
	if !strings.HasSuffix(name, ".md") {
		t.Fatalf("archive file name %q missing .md suffix", name)
	}
	base := strings.TrimSuffix(name, ".md")
	prefix, month, found := strings.Cut(base, "-")
	if !found {
		t.Fatalf("archive file name %q missing month separator", name)
	}
	validPrefix := prefix == strings.TrimSuffix(openLedgerName, ".md") ||
		prefix == strings.TrimSuffix(bugLedgerName, ".md") ||
		prefix == strings.TrimSuffix(backlogLedgerName, ".md")
	if !validPrefix {
		t.Fatalf("archive file name %q has unexpected prefix %q", name, prefix)
	}
	if _, err := time.Parse(archiveMonthLayout, month); err != nil {
		t.Fatalf("archive file name %q has invalid month format %q: %v", name, month, err)
	}
}

// assertTaskArchiveNoChange compares all temporary ledger bytes across an empty second run.
func assertTaskArchiveNoChange(t *testing.T, root string) {
	t.Helper()
	before := taskArchiveFixtureFiles(t, root)
	if count, err := ArchiveCompletedTasks(root, "fixture"); err != nil || count != 0 {
		t.Fatalf("second archive = %d, %v", count, err)
	}
	after := taskArchiveFixtureFiles(t, root)
	if len(after) != len(before) {
		t.Fatalf("second archive changed file count: %d -> %d", len(before), len(after))
	}
	for path, content := range before {
		if after[path] != content {
			t.Fatalf("second archive changed %s", path)
		}
	}
}

// taskArchiveFixtureFiles snapshots two flat fixture directories without recursive traversal.
func taskArchiveFixtureFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	dir := filepath.Join(root, WorkingDirName)
	files := map[string]string{}
	for _, folder := range []string{dir, filepath.Join(dir, "archive")} {
		entries, err := os.ReadDir(folder)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(entries) && i < maxScannedLines; i++ {
			if !entries[i].IsDir() {
				path := filepath.Join(folder, entries[i].Name())
				files[path] = readLedgerFile(t, path)
			}
		}
	}
	return files
}

// TestTaskArchiveBugMetadataAndReservedIDs preserves metadata and never reuses archived IDs.
func TestTaskArchiveBugMetadataAndReservedIDs(t *testing.T) {
	root := t.TempDir()
	resolved := time.Date(2025, 7, 31, 23, 59, 0, 0, time.FixedZone("offset", -3600))
	bug := BugEntry{ID: "BUG-009", Title: "fixed", Severity: "p2", Status: "resolved", Context: "preserve | context", ResolvedAt: resolved}
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	row, err := encodeBugRow(bug)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := encodeBugMeta(ledgerMetaIndex{bug.ID: metadataOf(bug)})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, WorkingDirName)
	writeIntegrityFile(t, filepath.Join(dir, bugLedgerName), defaultBugsMD()+row+"\n")
	writeIntegrityFile(t, filepath.Join(dir, bugMetaName), string(meta))
	if _, err := ArchiveCompletedTasks(root, "fixture"); err != nil {
		t.Fatal(err)
	}
	archive := readLedgerFile(t, filepath.Join(dir, "archive", "BUGS-2025-08.md"))
	entries, err := ParseBugsMarkdownStrict(archive)
	if err != nil || len(entries) != 1 {
		t.Fatalf("archive lost sidecar metadata: %+v, %v", entries, err)
	}
	if err := sameBugRecord(bug, entries[0]); err != nil {
		t.Fatal(err)
	}
	added, err := AddBug(root, BugEntry{Title: "next", Severity: "p2"})
	if err != nil || added.ID != "BUG-010" {
		t.Fatalf("archived ID reused: %+v, %v", added, err)
	}
}

// TestTaskArchiveFailureKeepsAllLiveRows proves a later archive failure precedes live writes.
func TestTaskArchiveFailureKeepsAllLiveRows(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, WorkingDirName)
	open := "- [x] keep me (completed: 2026-09-12)\n"
	bugs := defaultBugsMD() + "| `BUG-001` | fixed | p2 | resolved | file | 2026-09-12 |\n"
	writeIntegrityFile(t, filepath.Join(dir, "OPEN.md"), open)
	writeIntegrityFile(t, filepath.Join(dir, "BUGS.md"), bugs)
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
	if err := os.MkdirAll(filepath.Join(dir, "archive", "BUGS-2026-09.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ArchiveCompletedTasks(root, "fixture"); err == nil {
		t.Fatal("archive destination directory accepted")
	}
	assertIntegrityFile(t, filepath.Join(dir, "OPEN.md"), open)
	assertIntegrityFile(t, filepath.Join(dir, "BUGS.md"), bugs)
	if got := readLedgerFile(t, filepath.Join(dir, "archive", "OPEN-2026-09.md")); !strings.Contains(got, "keep me") {
		t.Fatal("first archive not preserved on later failure")
	}
}

// TestTaskArchiveFallbackAndFences preserves examples and CRLF while grouping missing dates.
func TestTaskArchiveFallbackAndFences(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, WorkingDirName)
	const examples = "# Open\r\n```\r\n- [x] example\r\n```\r\n"
	writeIntegrityFile(t, filepath.Join(dir, "OPEN.md"), examples+"- [X] done (completed: 2026-99-99)")
	writeIntegrityFile(t, filepath.Join(dir, "BACKLOG.md"), "- [x] delivered\n- [x] delivered\n")
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	reports, err := archiveCompletedTasksAt(t.Context(), root, true, now)
	if err != nil || reports[0].Entries != 1 || reports[2].Entries != 2 {
		t.Fatalf("fallback archive = %+v, %v", reports, err)
	}
	assertIntegrityFile(t, filepath.Join(dir, "OPEN.md"), examples)
	archive := readLedgerFile(t, filepath.Join(dir, "archive", "BACKLOG-2026-11.md"))
	if strings.Count(archive, "- [x] delivered") != 2 {
		t.Fatal("identical records collapsed")
	}
	if _, err := os.Stat(filepath.Join(dir, "archive", "OPEN-2026-11.md")); err != nil {
		t.Fatal(err)
	}
	assertTaskArchiveNoChange(t, root)
}
