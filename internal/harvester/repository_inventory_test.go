package harvester

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

func TestScanLocalWorkstationRepositoryObservations(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	linkedRoot := filepath.Join(root, "worktrees")
	local := filepath.Join(root, "local")
	bare := filepath.Join(root, "bare.git")
	for _, path := range []string{main, local} {
		initTestRepository(t, path)
	}
	if out, err := runTestGit(root, "init", "--bare", bare); err != nil {
		t.Fatalf("init bare: %v (%s)", err, out)
	}
	if err := os.Mkdir(filepath.Join(bare, ".workingdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(linkedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := runTestGit(main, "worktree", "add", "-b", "inventory-linked", filepath.Join(linkedRoot, "linked")); err != nil {
		t.Fatalf("add linked worktree: %v (%s)", err, out)
	}
	if out, err := runTestGit(main, "remote", "add", "origin", "https://user:secret@example.invalid/org/repo.git?token=hidden#frag"); err != nil {
		t.Fatalf("add remote: %v (%s)", err, out)
	}
	if err := os.Mkdir(filepath.Join(main, ".workingdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(main, ".git", "info", "exclude"), []byte(".workingdir/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := ScanLocalWorkstation(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.RepositoryObservations) != 4 || !report.RepositoryInventoryComplete {
		t.Fatalf("observations=%d complete=%v errors=%v", len(report.RepositoryObservations), report.RepositoryInventoryComplete, report.RepositoryInventoryErrors)
	}
	byClass := make(map[string]RepositoryObservation)
	for _, observation := range report.RepositoryObservations {
		byClass[observation.Classification] = observation
	}
	for _, class := range []string{"main", "linked-worktree", "bare", "local-only"} {
		if _, ok := byClass[class]; !ok {
			t.Fatalf("missing %s observation: %+v", class, report.RepositoryObservations)
		}
	}
	mainObservation := byClass["main"]
	if byClass["bare"].DirtyScope != "not-applicable" {
		t.Fatalf("bare repository has working-tree dirty scope: %+v", byClass["bare"])
	}
	bareObservation := byClass["bare"]
	if !bareObservation.WorkingdirPresent || bareObservation.WorkingdirProbeIgnored != nil || bareObservation.WorkingdirTrackedState != "not-applicable" || len(bareObservation.ProbeErrors) != 0 {
		t.Fatalf("bare privacy probe was not not-applicable: %+v", bareObservation)
	}
	if mainObservation.RemoteState != "known" || len(mainObservation.RemoteURLs) != 1 || mainObservation.RemoteURLs[0] != "https://example.invalid/org/repo.git" {
		t.Fatalf("remote was not sanitized: %+v", mainObservation)
	}
	if !mainObservation.WorkingdirPresent || mainObservation.WorkingdirProbeIgnored == nil || !*mainObservation.WorkingdirProbeIgnored || mainObservation.WorkingdirTrackedState != "known" || mainObservation.WorkingdirTrackedEntries != 0 {
		t.Fatalf("privacy observation incorrect: %+v", mainObservation)
	}
	if mainObservation.GitCommonDir != byClass["linked-worktree"].GitCommonDir {
		t.Fatalf("main and linked worktree lost shared identity: %+v %+v", mainObservation, byClass["linked-worktree"])
	}
	if _, err := util.RunGit(context.Background(), main, "remote", "remove", "origin"); err != nil {
		t.Fatal(err)
	}
	transition := inspectRepository(context.Background(), main)
	if transition.RemoteState != "known" || len(transition.RemoteURLs) != 0 || transition.Classification != "local-only" {
		t.Fatalf("remote removal transition incorrect: %+v", transition)
	}
}

func TestScanLocalWorkstationRepositoryInventoryBoundAndFailure(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < MaxDevScanEntries+1; i++ {
		// A zero-padded index stays a legal file name on every platform. The previous scheme
		// appended rune('0'+i/26), which walks past '9' into ':', '<', '>' and '?' -- names
		// POSIX accepts and Windows refuses, so the fixture could not be built there.
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("entry-%03d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	failure := filepath.Join(root, "broken")
	if err := os.Mkdir(failure, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(failure, ".git"), []byte("gitdir: /private/token=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := ScanLocalWorkstation(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if report.RepositoryInventoryComplete || !report.RepositoryInventoryTruncated {
		t.Fatalf("overflow was not explicit: %+v", report)
	}
	if len(report.RepositoryObservations) != 1 || report.RepositoryObservations[0].RemoteState != "unknown" || report.RepositoryObservations[0].Classification == "local-only" {
		t.Fatalf("failed git probe was misclassified: %+v", report.RepositoryObservations)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || containsTestSecret(string(encoded)) {
		t.Fatalf("serialized failure leaked probe input: %s", encoded)
	}
}

func containsTestSecret(value string) bool {
	return strings.Contains(value, "private/token=secret")
}

// TestScanWorktreeContainerReadableStaysComplete is the positive dimension: a readable
// worktree container produces an exhaustive stale list and leaves the report complete.
func TestScanWorktreeContainerReadableStaysComplete(t *testing.T) {
	root, container := newTestWorktreeContainer(t)
	addTestStaleWorktree(t, container, "task-1")
	report, err := ScanLocalWorkstation(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !report.RepositoryInventoryComplete || report.RepositoryInventoryTruncated {
		t.Fatalf("readable worktree container was reported incomplete: %+v", report)
	}
	if len(report.RepositoryInventoryErrors) != 0 || len(report.StaleWorktrees) != 1 {
		t.Fatalf("complete scan lost its result: errors=%v stale=%v", report.RepositoryInventoryErrors, report.StaleWorktrees)
	}
}

// TestScanWorktreeContainerEmptyStaysComplete is the zero-entry boundary: an empty
// container is a complete scan with no stale worktrees and no recorded failure.
func TestScanWorktreeContainerEmptyStaysComplete(t *testing.T) {
	root, _ := newTestWorktreeContainer(t)
	report, err := ScanLocalWorkstation(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !report.RepositoryInventoryComplete || report.RepositoryInventoryTruncated ||
		len(report.RepositoryInventoryErrors) != 0 || len(report.StaleWorktrees) != 0 {
		t.Fatalf("empty worktree container was not an exhaustive empty scan: %+v", report)
	}
}

// TestScanWorktreeContainerUnreadableIsReportedIncomplete is the negative dimension: a
// container the scan cannot read drops every stale candidate, so the report must say so
// instead of publishing the empty list as exhaustive.
func TestScanWorktreeContainerUnreadableIsReportedIncomplete(t *testing.T) {
	testsupport.SkipIfDirectoryModeUnenforced(t)
	root, container := newTestWorktreeContainer(t)
	addTestStaleWorktree(t, container, "task-1")
	if err := os.Chmod(container, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(container, 0o755); err != nil {
			t.Error(err)
		}
	})
	report, err := ScanLocalWorkstation(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if report.RepositoryInventoryComplete || len(report.StaleWorktrees) != 0 {
		t.Fatalf("unreadable worktree container was reported as a complete scan: %+v", report)
	}
	if !hasTestInventoryError(report, "stale worktree scan of demo-worktrees failed") {
		t.Fatalf("read failure was not attributed to the stale worktree scan: %v", report.RepositoryInventoryErrors)
	}
}

// TestScanWorktreeContainerOverLimitIsReportedTruncated is the N_max boundary: a container
// holding more than MaxWorktreeScan entries is reported truncated, not complete.
func TestScanWorktreeContainerOverLimitIsReportedTruncated(t *testing.T) {
	root, container := newTestWorktreeContainer(t)
	for i := 0; i <= MaxWorktreeScan; i++ {
		if err := os.Mkdir(filepath.Join(container, fmt.Sprintf("task-%03d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	report, err := ScanLocalWorkstation(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if report.RepositoryInventoryComplete || !report.RepositoryInventoryTruncated {
		t.Fatalf("over-limit worktree container was not reported truncated: %+v", report)
	}
	want := fmt.Sprintf("stale worktree scan of demo-worktrees exceeds %d entries", MaxWorktreeScan)
	if !hasTestInventoryError(report, want) {
		t.Fatalf("truncation was not attributed to the stale worktree scan: %v", report.RepositoryInventoryErrors)
	}
}

func newTestWorktreeContainer(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	container := filepath.Join(root, "demo-worktrees")
	if err := os.Mkdir(container, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, container
}

func addTestStaleWorktree(t *testing.T, container, name string) {
	t.Helper()
	path := filepath.Join(container, name)
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * DefaultStaleWorktreeAge)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
}

func hasTestInventoryError(report *WorkstationReport, fragment string) bool {
	for i := 0; i < len(report.RepositoryInventoryErrors) && i < MaxDevScanEntries; i++ {
		if strings.Contains(report.RepositoryInventoryErrors[i], fragment) {
			return true
		}
	}
	return false
}

func TestScanLocalWorkstationCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ScanLocalWorkstation(ctx, t.TempDir()); err == nil {
		t.Fatal("cancelled scan unexpectedly succeeded")
	}
}

func initTestRepository(t *testing.T, path string) {
	t.Helper()
	if out, err := runTestGit(filepath.Dir(path), "init", path); err != nil {
		t.Fatalf("init repository: %v (%s)", err, out)
	}
	if out, err := runTestGit(path, "config", "user.email", "test@example.invalid"); err != nil {
		t.Fatalf("config email: %v (%s)", err, out)
	}
	if out, err := runTestGit(path, "config", "user.name", "Inventory Test"); err != nil {
		t.Fatalf("config name: %v (%s)", err, out)
	}
	file := filepath.Join(path, "README")
	if err := os.WriteFile(file, []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runTestGit(path, "add", "README"); err != nil {
		t.Fatalf("add: %v (%s)", err, out)
	}
	if out, err := runTestGit(path, "commit", "-m", "fixture"); err != nil {
		t.Fatalf("commit: %v (%s)", err, out)
	}
}

func runTestGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
