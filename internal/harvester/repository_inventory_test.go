package harvester

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
		if err := os.Mkdir(filepath.Join(root, "entry-"+string(rune('a'+i%26))+string(rune('0'+i/26))), 0o755); err != nil {
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
