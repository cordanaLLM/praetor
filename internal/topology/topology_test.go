package topology

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func initTestGit(t *testing.T, dir string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("failed to create test git dir: %v", err)
	}
	headFile := filepath.Join(gitDir, "HEAD")
	if err := os.WriteFile(headFile, []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatalf("failed to create test HEAD file: %v", err)
	}
}

func setupMockDevEnvironment(t *testing.T) string {
	t.Helper()
	devRoot := t.TempDir()

	// Dev root files
	if err := os.WriteFile(filepath.Join(devRoot, "AGENTS.md"), []byte("# Workstation"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devRoot, "workstation.code-workspace"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	// vmafx org container
	vmafxOrg := filepath.Join(devRoot, "vmafx")
	if err := os.MkdirAll(vmafxOrg, 0755); err != nil {
		t.Fatal(err)
	}

	// Child repos inside vmafx
	vmafxCore := filepath.Join(vmafxOrg, "vmafx")
	initTestGit(t, vmafxCore)
	pelorusRepo := filepath.Join(vmafxOrg, "pelorus")
	initTestGit(t, pelorusRepo)

	// Stray governance files inside vmafx org root
	strayFiles := []string{
		"AGENTS.md",
		".standards.yaml",
		".standards-baseline.json",
		"Makefile",
		"lefthook.yml",
		".needs.yaml",
	}
	for _, sf := range strayFiles {
		if err := os.WriteFile(filepath.Join(vmafxOrg, sf), []byte("stray"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Stray docs directory in vmafx
	if err := os.MkdirAll(filepath.Join(vmafxOrg, "docs", "adr"), 0755); err != nil {
		t.Fatal(err)
	}

	// golusoris org container with stray headless .git
	golusorisOrg := filepath.Join(devRoot, "golusoris")
	if err := os.MkdirAll(filepath.Join(golusorisOrg, ".git", "hooks"), 0755); err != nil {
		t.Fatal(err)
	}
	// Child repo inside golusoris
	goenvoyRepo := filepath.Join(golusorisOrg, "goenvoy")
	initTestGit(t, goenvoyRepo)

	// Symlink in dev root (DEV-02)
	symlinkPath := filepath.Join(devRoot, "pelorus")
	if err := os.Symlink(pelorusRepo, symlinkPath); err != nil {
		t.Fatal(err)
	}

	return devRoot
}

func TestAuditWorkstationTopology_Positive(t *testing.T) {
	devRoot := setupMockDevEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	report, err := AuditWorkstationTopology(ctx, devRoot)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology failed: %v", err)
	}

	if len(report.ValidRepos) != 3 {
		t.Errorf("expected 3 valid repos, got %d: %v", len(report.ValidRepos), report.ValidRepos)
	}

	if len(report.Symlinks) != 1 || report.Symlinks[0] != "pelorus" {
		t.Errorf("expected symlink 'pelorus', got %v", report.Symlinks)
	}

	// Verify stray files detected
	if len(report.StrayFiles) < 7 {
		t.Errorf("expected at least 7 stray files/dirs, got %d", len(report.StrayFiles))
	}

	if !containsStrayBase(report.StrayFiles, ".git") {
		t.Error("expected stray headless .git in golusoris to be detected")
	}
	if !containsStrayBase(report.StrayFiles, ".standards-baseline.json") {
		t.Error("expected stray .standards-baseline.json to be detected")
	}
	assertStringSet(t, "OrgContainers", report.OrgContainers, []string{"golusoris", "vmafx"})
	assertStringSet(t, "Violations", report.Violations,
		[]string{"DEV-02: root compatibility symlink pelorus violates canonical path invariant"})
}

func assertStringSet(t *testing.T, field string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
	seen := make(map[string]bool, len(got))
	for _, value := range got {
		seen[value] = true
	}
	for _, value := range want {
		if !seen[value] {
			t.Fatalf("%s = %v, want %v", field, got, want)
		}
	}
}

// TestAuditWorkstationTopology_DEV01RootRepositories pins the DEV-01 branch: a repository
// directly in the dev root is a violation whether its .git is a directory or a gitlink,
// a plain directory is not, and organization containers match case-insensitively.
func TestAuditWorkstationTopology_DEV01RootRepositories(t *testing.T) {
	devRoot := t.TempDir()
	initTestGit(t, filepath.Join(devRoot, "rogue"))
	writeGitlink(t, filepath.Join(devRoot, "linked"), "gitdir: /elsewhere/.git\n")
	if err := os.MkdirAll(filepath.Join(devRoot, "plain", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	initTestGit(t, filepath.Join(devRoot, "cordanaLLM", "praetor"))

	report, err := AuditWorkstationTopology(context.Background(), devRoot)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	assertStringSet(t, "Violations", report.Violations, []string{
		"DEV-01: repository linked is located directly in dev root instead of an org folder",
		"DEV-01: repository rogue is located directly in dev root instead of an org folder",
	})
	assertStringSet(t, "OrgContainers", report.OrgContainers, []string{"cordanaLLM"})
	assertStringSet(t, "ValidRepos", report.ValidRepos, []string{filepath.Join("cordanaLLM", "praetor")})
	if len(report.StrayFiles) != 0 || report.Truncated {
		t.Fatalf("DEV-01 fixture produced strays %v or truncation %v", report.StrayFiles, report.TruncationReasons)
	}
}

func TestAuditWorkstationTopology_Boundary_EmptyDevRootHasNoFindings(t *testing.T) {
	report, err := AuditWorkstationTopology(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	assertStringSet(t, "Violations", report.Violations, nil)
	assertStringSet(t, "OrgContainers", report.OrgContainers, nil)
	if report.Violations == nil || report.OrgContainers == nil {
		t.Fatal("empty report must carry empty, non-nil slices")
	}
}

func containsStrayBase(files []StrayFile, name string) bool {
	for _, file := range files {
		if filepath.Base(file.Path) == name {
			return true
		}
	}
	return false
}

func TestCleanWorkstationTopology_DryRun(t *testing.T) {
	devRoot := setupMockDevEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cleaned, err := CleanWorkstationTopology(ctx, devRoot, true)
	if err != nil {
		t.Fatalf("CleanWorkstationTopology (dry run) failed: %v", err)
	}

	if len(cleaned) == 0 {
		t.Fatal("expected cleaned items in dry run, got 0")
	}

	// Verify files still exist on disk because it was dry-run
	strayBaseline := filepath.Join(devRoot, "vmafx", ".standards-baseline.json")
	if _, err := os.Stat(strayBaseline); os.IsNotExist(err) {
		t.Error("dry run must not delete files from disk")
	}
}

func TestCleanWorkstationTopology_Apply(t *testing.T) {
	devRoot := setupMockDevEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cleaned, err := CleanWorkstationTopology(ctx, devRoot, false)
	if err != nil {
		t.Fatalf("CleanWorkstationTopology (apply) failed: %v", err)
	}

	if len(cleaned) == 0 {
		t.Fatal("expected cleaned items, got 0")
	}

	// Verify stray files are gone
	strayBaseline := filepath.Join(devRoot, "vmafx", ".standards-baseline.json")
	if _, err := os.Stat(strayBaseline); !os.IsNotExist(err) {
		t.Errorf("expected %s to be deleted", strayBaseline)
	}

	strayGit := filepath.Join(devRoot, "golusoris", ".git")
	if _, err := os.Stat(strayGit); !os.IsNotExist(err) {
		t.Errorf("expected stray .git %s to be deleted", strayGit)
	}

	// Verify child repos are 100% intact
	vmafxCoreGit := filepath.Join(devRoot, "vmafx", "vmafx", ".git", "HEAD")
	if _, err := os.Stat(vmafxCoreGit); err != nil {
		t.Errorf("child repo vmafx was corrupted: %v", err)
	}

	goenvoyGit := filepath.Join(devRoot, "golusoris", "goenvoy", ".git", "HEAD")
	if _, err := os.Stat(goenvoyGit); err != nil {
		t.Errorf("child repo goenvoy was corrupted: %v", err)
	}

	// Verify stray symlink in dev root was cleaned
	symlinkPath := filepath.Join(devRoot, "pelorus")
	if isSymlink(symlinkPath) {
		t.Errorf("stray symlink %s should have been removed", symlinkPath)
	}
}

func TestCleanWorkstationTopology_PreservesLiveOrganizationRepository(t *testing.T) {
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "golusoris")
	initTestGit(t, orgDir)
	governancePaths := writeGovernancePayload(t, orgDir)
	childRepo := filepath.Join(orgDir, "goenvoy")
	initTestGit(t, childRepo)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := CleanWorkstationTopologyDetailed(ctx, devRoot, false)
	if err != nil {
		t.Fatalf("CleanWorkstationTopology: %v", err)
	}
	if len(result.Cleaned) != 0 || len(result.Blocked) != 1 {
		t.Fatalf("live metadata result = %+v, want one blocked finding", result)
	}

	orgHead := filepath.Join(orgDir, ".git", "HEAD")
	if _, err := os.Stat(orgHead); err != nil {
		t.Fatalf("live organization repository metadata was deleted: %v", err)
	}
	childHead := filepath.Join(childRepo, ".git", "HEAD")
	if _, err := os.Stat(childHead); err != nil {
		t.Fatalf("child repository metadata was deleted: %v", err)
	}
	assertPathsExist(t, governancePaths)
}

func TestCleanWorkstationTopology_PreservesLiveOrganizationRepositoryContents(t *testing.T) {
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "golusoris")
	initTestGit(t, orgDir)
	governancePaths := writeGovernancePayload(t, orgDir)

	result, err := CleanWorkstationTopologyDetailed(context.Background(), devRoot, false)
	if err != nil {
		t.Fatalf("CleanWorkstationTopology: %v", err)
	}
	if len(result.Cleaned) != 0 {
		t.Fatalf("cleaned tracked organization content: %v", result.Cleaned)
	}
	assertPathsExist(t, governancePaths)
}

func TestCleanWorkstationTopology_PreservesIndeterminateOrganizationContents(t *testing.T) {
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "golusoris")
	if err := os.MkdirAll(orgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orgDir, ".git"), []byte("not a gitlink\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	governancePaths := writeGovernancePayload(t, orgDir)

	result, err := CleanWorkstationTopologyDetailed(context.Background(), devRoot, false)
	if err != nil {
		t.Fatalf("CleanWorkstationTopology: %v", err)
	}
	if len(result.Cleaned) != 0 || len(result.Blocked) == 0 {
		t.Fatalf("indeterminate organization result = %+v, want blocked-only", result)
	}
	assertPathsExist(t, governancePaths)
}

func TestCleanWorkstationTopology_PreservesLiveOrganizationGitlink(t *testing.T) {
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "golusoris")
	linkedRepo := filepath.Join(t.TempDir(), "linked")
	initTestGit(t, linkedRepo)
	writeGitlink(t, orgDir, "gitdir: "+filepath.Join(linkedRepo, ".git")+"\n")
	initTestGit(t, filepath.Join(orgDir, "goenvoy"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := CleanWorkstationTopology(ctx, devRoot, false); err != nil {
		t.Fatalf("CleanWorkstationTopology: %v", err)
	}
	if _, err := os.Stat(filepath.Join(orgDir, ".git")); err != nil {
		t.Fatalf("live organization gitlink was deleted: %v", err)
	}
}

func TestCleanWorkstationTopology_PreservesLegacyHeadSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("legacy Git HEAD symlinks are a POSIX repository layout")
	}
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "golusoris")
	initTestGit(t, orgDir)
	headPath := filepath.Join(orgDir, ".git", "HEAD")
	if err := os.Remove(headPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("refs/heads/master", headPath); err != nil {
		t.Fatal(err)
	}
	initTestGit(t, filepath.Join(orgDir, "goenvoy"))

	if _, err := CleanWorkstationTopology(context.Background(), devRoot, false); err != nil {
		t.Fatalf("CleanWorkstationTopology: %v", err)
	}
	info, err := os.Lstat(headPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("legacy HEAD symlink was not preserved: info=%v err=%v", info, err)
	}
}

func TestCleanWorkstationTopology_PreservesIndeterminateGitSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating repository symlinks requires optional Windows privileges")
	}
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "golusoris")
	if err := os.MkdirAll(orgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitPath := filepath.Join(orgDir, ".git")
	if err := os.Symlink(filepath.Join(t.TempDir(), "unavailable"), gitPath); err != nil {
		t.Fatal(err)
	}
	initTestGit(t, filepath.Join(orgDir, "goenvoy"))

	if _, err := CleanWorkstationTopology(context.Background(), devRoot, false); err != nil {
		t.Fatalf("CleanWorkstationTopology: %v", err)
	}
	info, err := os.Lstat(gitPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("indeterminate .git symlink was not preserved: info=%v err=%v", info, err)
	}
}

func TestCleanWorkstationTopology_PreservesGovernanceNamedLegacyRepository(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("legacy Git HEAD symlinks are a POSIX repository layout")
	}
	devRoot := t.TempDir()
	repo := filepath.Join(devRoot, "golusoris", "docs")
	initTestGit(t, repo)
	headPath := filepath.Join(repo, ".git", "HEAD")
	if err := os.Remove(headPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("refs/heads/master", headPath); err != nil {
		t.Fatal(err)
	}

	if _, err := CleanWorkstationTopology(context.Background(), devRoot, false); err != nil {
		t.Fatalf("CleanWorkstationTopology: %v", err)
	}
	info, err := os.Lstat(headPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("governance-named repository was not preserved: info=%v err=%v", info, err)
	}
}

func TestCleanWorkstationTopology_PreservesGovernanceNamedIndeterminateMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating repository symlinks requires optional Windows privileges")
	}
	devRoot := t.TempDir()
	repo := filepath.Join(devRoot, "golusoris", "docs")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitPath := filepath.Join(repo, ".git")
	if err := os.Symlink(filepath.Join(t.TempDir(), "unavailable"), gitPath); err != nil {
		t.Fatal(err)
	}

	if _, err := CleanWorkstationTopology(context.Background(), devRoot, false); err != nil {
		t.Fatalf("CleanWorkstationTopology: %v", err)
	}
	info, err := os.Lstat(gitPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("indeterminate governance metadata was not preserved: info=%v err=%v", info, err)
	}
}

func TestCleanWorkstationTopology_PreservesGovernanceNamedUnreadableMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs do not implement POSIX chmod permission denial")
	}
	devRoot := t.TempDir()
	repo := filepath.Join(devRoot, "golusoris", "docs")
	initTestGit(t, repo)
	gitPath := filepath.Join(repo, ".git")
	if err := os.Chmod(gitPath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(gitPath, 0o755); err != nil && !os.IsNotExist(err) {
			t.Errorf("restore git metadata permissions: %v", err)
		}
	})
	if _, err := os.Lstat(filepath.Join(gitPath, "HEAD")); err == nil {
		t.Skip("current user can inspect chmod-000 directories")
	}

	if _, err := CleanWorkstationTopology(context.Background(), devRoot, false); err != nil {
		t.Fatalf("CleanWorkstationTopology: %v", err)
	}
	if _, err := os.Lstat(gitPath); err != nil {
		t.Fatalf("unreadable governance metadata was not preserved: %v", err)
	}
	if err := verifyDeletionSafety(devRoot, repo); err == nil {
		t.Fatal("unreadable governance metadata passed the final deletion boundary")
	}
}

func TestAuditWorkstationTopology_UnreadableGitMetadataIsNotSafeToDelete(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs do not implement POSIX chmod permission denial")
	}
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "golusoris")
	initTestGit(t, orgDir)
	initTestGit(t, filepath.Join(orgDir, "goenvoy"))
	gitPath := filepath.Join(orgDir, ".git")
	if err := os.Chmod(gitPath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(gitPath, 0o755); err != nil {
			t.Errorf("restore git metadata permissions: %v", err)
		}
	})
	if _, err := os.Lstat(filepath.Join(gitPath, "HEAD")); err == nil {
		t.Skip("current user can inspect chmod-000 directories")
	}

	report, err := AuditWorkstationTopology(context.Background(), devRoot)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	requireUnsafeStray(t, report, gitPath)
	if err := verifyDeletionSafety(devRoot, gitPath); err == nil {
		t.Fatal("expected unreadable git metadata to fail closed")
	}
}

func TestAuditWorkstationTopology_LiveOrganizationRepositoryIsNotSafeToDelete(t *testing.T) {
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "golusoris")
	initTestGit(t, orgDir)
	initTestGit(t, filepath.Join(orgDir, "goenvoy"))

	report, err := AuditWorkstationTopology(context.Background(), devRoot)
	if err != nil {
		t.Fatalf("AuditWorkstationTopology: %v", err)
	}
	gitPath := filepath.Join(orgDir, ".git")
	requireUnsafeStray(t, report, gitPath)
}

func requireUnsafeStray(t *testing.T, report *TopologyReport, path string) {
	t.Helper()
	for _, stray := range report.StrayFiles {
		if stray.Path == path {
			if stray.IsSafeToDelete {
				t.Fatalf("topology finding marked safe to delete: %+v", stray)
			}
			return
		}
	}
	t.Fatalf("expected %s to remain an explicit topology finding", path)
}

func TestAuditWorkstationTopology_Negative_NonExistent(t *testing.T) {
	ctx := context.Background()
	_, err := AuditWorkstationTopology(ctx, "/nonexistent/path/for/test")
	if !errors.Is(err, ErrDevRootNotExist) {
		t.Errorf("expected ErrDevRootNotExist, got %v", err)
	}
}

func TestAuditWorkstationTopology_Negative_NotADir(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "somefile.txt")
	if err := os.WriteFile(tmpFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err := AuditWorkstationTopology(ctx, tmpFile)
	if !errors.Is(err, ErrDevRootNotDir) {
		t.Errorf("expected ErrDevRootNotDir, got %v", err)
	}
}

func TestAuditWorkstationTopology_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := AuditWorkstationTopology(ctx, t.TempDir())
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
}

func TestCleanWorkstationTopology_Boundary_EmptyDevRoot(t *testing.T) {
	devRoot := t.TempDir()
	ctx := context.Background()
	cleaned, err := CleanWorkstationTopology(ctx, devRoot, false)
	if err != nil {
		t.Fatalf("unexpected error on empty dev root: %v", err)
	}
	if len(cleaned) != 0 {
		t.Errorf("expected 0 cleaned items on empty dev root, got %d", len(cleaned))
	}
}

func TestCleanWorkstationTopology_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := CleanWorkstationTopology(ctx, t.TempDir(), false)
	if err == nil || result != nil {
		t.Fatalf("cancelled clean result = %+v, err = %v; want nil result and error", result, err)
	}
}

func TestCleanWorkstationTopologyDetailed_Boundary_EmptyDevRoot(t *testing.T) {
	result, err := CleanWorkstationTopologyDetailed(context.Background(), t.TempDir(), false)
	if err != nil {
		t.Fatalf("detailed clean on empty root: %v", err)
	}
	if len(result.Cleaned) != 0 || len(result.Blocked) != 0 {
		t.Fatalf("detailed empty-root result = %+v, want empty", result)
	}
}

func TestCleanWorkstationTopologyDetailed_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := CleanWorkstationTopologyDetailed(ctx, t.TempDir(), false)
	if err == nil || result != nil {
		t.Fatalf("cancelled detailed result = %+v, err = %v; want nil result and error", result, err)
	}
}

func TestVerifyDeletionSafety_Protections(t *testing.T) {
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "vmafx")
	if err := os.MkdirAll(orgDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 1. Cannot delete dev root
	if err := verifyDeletionSafety(devRoot, devRoot); err == nil {
		t.Error("expected safety check to reject dev root deletion")
	}

	// 2. Cannot delete org container itself
	if err := verifyDeletionSafety(devRoot, orgDir); err == nil {
		t.Error("expected safety check to reject org container deletion")
	}

	// 3. Symlink unlinking is permitted
	symlinkPath := filepath.Join(devRoot, "pelorus")
	if err := os.Symlink(orgDir, symlinkPath); err != nil {
		t.Fatal(err)
	}
	if err := verifyDeletionSafety(devRoot, symlinkPath); err != nil {
		t.Errorf("expected safety check to allow symlink deletion, got %v", err)
	}

	// 4. Cannot delete directory with valid git repo
	childRepo := filepath.Join(orgDir, "vmafx")
	initTestGit(t, childRepo)
	if err := verifyDeletionSafety(devRoot, childRepo); err == nil {
		t.Error("expected safety check to reject directory with valid git repo")
	}

	// 5. Cannot delete the metadata directory of a valid git repository.
	if err := verifyDeletionSafety(devRoot, filepath.Join(childRepo, ".git")); err == nil {
		t.Error("expected safety check to reject valid git metadata directory")
	}
}

func TestVerifyDeletionSafety_GitMetadataTransitions(t *testing.T) {
	devRoot := t.TempDir()
	orgDir := filepath.Join(devRoot, "vmafx")
	if err := os.MkdirAll(orgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Cannot delete malformed or indeterminate git metadata.
	malformedRepo := filepath.Join(orgDir, "malformed")
	writeGitlink(t, malformedRepo, "not a gitlink\n")
	if err := verifyDeletionSafety(devRoot, filepath.Join(malformedRepo, ".git")); err == nil {
		t.Error("expected safety check to reject indeterminate git metadata")
	}

	// A directory whose HEAD is proven absent remains cleanable.
	headlessGit := filepath.Join(orgDir, "headless", ".git")
	if err := os.MkdirAll(headlessGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyDeletionSafety(devRoot, headlessGit); err != nil {
		t.Errorf("expected proven-headless git metadata to remain cleanable: %v", err)
	}
}

func TestVerifyDeletionSafety_ProtectsOrganizationRepositoryContents(t *testing.T) {
	devRoot := t.TempDir()
	liveOrg := filepath.Join(devRoot, "golusoris")
	initTestGit(t, liveOrg)
	liveDocs := filepath.Join(liveOrg, "docs")
	if err := os.MkdirAll(liveDocs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyDeletionSafety(devRoot, liveDocs); err == nil {
		t.Fatal("tracked directory passed the organization repository boundary")
	}

	unknownOrg := filepath.Join(devRoot, "lusoris")
	if err := os.MkdirAll(filepath.Join(unknownOrg, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unknownOrg, ".git"), []byte("invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyDeletionSafety(devRoot, filepath.Join(unknownOrg, "docs")); err == nil {
		t.Fatal("indeterminate organization metadata passed the deletion boundary")
	}
}

func writeGovernancePayload(t *testing.T, orgDir string) []string {
	t.Helper()
	paths := []string{
		filepath.Join(orgDir, "AGENTS.md"),
		filepath.Join(orgDir, "docs", "guide.md"),
		filepath.Join(orgDir, ".github", "workflow.yml"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("tracked\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func assertPathsExist(t *testing.T, paths []string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("organization repository content was not preserved: %s: %v", path, err)
		}
	}
}

func writeGitlink(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeGitlinkTarget(t *testing.T, root, name string, headIsDirectory bool) string {
	t.Helper()
	target := filepath.Join(root, name)
	head := filepath.Join(target, "HEAD")
	if headIsDirectory {
		if err := os.MkdirAll(head, 0o755); err != nil {
			t.Fatal(err)
		}
		return target
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	return target
}

type gitRepoCase struct {
	name string
	path string
	want bool
}

func validGitRepoCases(t *testing.T, root, repo string) []gitRepoCase {
	t.Helper()
	relLink := filepath.Join(root, "worktree")
	writeGitlink(t, relLink, "gitdir: ../repo/.git\n")
	absLink := filepath.Join(root, "abs-worktree")
	writeGitlink(t, absLink, "gitdir: "+filepath.Join(repo, ".git")+"\n")
	exactLimit := filepath.Join(root, "exact-limit")
	exactContents := "gitdir: " + filepath.Join(repo, ".git")
	exactContents += strings.Repeat("\n", maxGitlinkBytes-len(exactContents))
	writeGitlink(t, exactLimit, exactContents)
	overLimit := filepath.Join(root, "over-limit")
	writeGitlink(t, overLimit, exactContents+" ")
	return []gitRepoCase{
		{name: "directory with HEAD", path: repo, want: true},
		{name: "relative gitlink", path: relLink, want: true},
		{name: "absolute gitlink", path: absLink, want: true},
		{name: "gitlink at byte limit", path: exactLimit, want: true},
		{name: "gitlink over byte limit", path: overLimit, want: false},
	}
}

func invalidGitRepoCases(t *testing.T, root, repo string) []gitRepoCase {
	t.Helper()
	emptyLink := filepath.Join(root, "empty-link")
	writeGitlink(t, emptyLink, "")
	oneByteLink := filepath.Join(root, "one-byte-link")
	writeGitlink(t, oneByteLink, "x")
	garbageLink := filepath.Join(root, "garbage-link")
	writeGitlink(t, garbageLink, "not a gitlink at all\n")
	danglingLink := filepath.Join(root, "dangling-link")
	writeGitlink(t, danglingLink, "gitdir: "+filepath.Join(root, "absent-target")+"\n")
	prefixOnly := filepath.Join(root, "prefix-only")
	writeGitlink(t, prefixOnly, "gitdir:\n")
	missingSpace := filepath.Join(root, "missing-space")
	writeGitlink(t, missingSpace, "gitdir:"+filepath.Join(repo, ".git")+"\n")
	leadingSpace := filepath.Join(root, "leading-space")
	writeGitlink(t, leadingSpace, " gitdir: "+filepath.Join(repo, ".git")+"\n")
	embeddedNewline := filepath.Join(root, "embedded-newline")
	writeGitlink(t, embeddedNewline, "gitdir: "+filepath.Join(repo, ".git")+"\njunk\n")
	notDirectoryTarget := filepath.Join(root, "not-directory-target")
	if err := os.WriteFile(notDirectoryTarget, []byte("not a git directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notDirectoryLink := filepath.Join(root, "not-directory-link")
	writeGitlink(t, notDirectoryLink, "gitdir: "+notDirectoryTarget+"\n")

	headlessTarget := writeGitlinkTarget(t, root, "headless-target", false)
	headlessLink := filepath.Join(root, "headless-link")
	writeGitlink(t, headlessLink, "gitdir: "+headlessTarget+"\n")

	directoryHeadTarget := writeGitlinkTarget(t, root, "directory-head-target", true)
	directoryHeadLink := filepath.Join(root, "directory-head-link")
	writeGitlink(t, directoryHeadLink, "gitdir: "+directoryHeadTarget+"\n")

	headless := filepath.Join(root, "headless")
	if err := os.MkdirAll(filepath.Join(headless, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return []gitRepoCase{
		{name: "empty gitlink", path: emptyLink, want: false},
		{name: "one-byte gitlink", path: oneByteLink, want: false},
		{name: "non-gitlink file", path: garbageLink, want: false},
		{name: "dangling gitlink", path: danglingLink, want: false},
		{name: "gitdir prefix without target", path: prefixOnly, want: false},
		{name: "gitdir prefix without required space", path: missingSpace, want: false},
		{name: "leading whitespace before gitdir prefix", path: leadingSpace, want: false},
		{name: "content after first newline belongs to target", path: embeddedNewline, want: false},
		{name: "gitlink target is a file", path: notDirectoryLink, want: false},
		{name: "gitlink target has no HEAD", path: headlessLink, want: false},
		{name: "gitlink target HEAD is a directory", path: directoryHeadLink, want: false},
		{name: "headless .git directory", path: headless, want: false},
		{name: "absent path", path: filepath.Join(root, "absent"), want: false},
		{name: "empty path", path: "", want: false},
		{name: "whitespace path", path: "  ", want: false},
	}
}

func TestHasValidGitRepo(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initTestGit(t, repo)
	cases := append(validGitRepoCases(t, root, repo), invalidGitRepoCases(t, root, repo)...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasValidGitRepo(tc.path); got != tc.want {
				t.Fatalf("HasValidGitRepo(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestHasValidGitRepoEmptyPathIgnoresCwd(t *testing.T) {
	dir := t.TempDir()
	initTestGit(t, dir)
	t.Chdir(dir)
	if HasValidGitRepo("") {
		t.Fatal("empty path accepted because the working directory is a checkout")
	}
}
