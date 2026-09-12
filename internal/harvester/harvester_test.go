package harvester

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mustMkdirAll creates dir or fails the test.
func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

// mustWriteFile writes body to path, creating the parent directory.
func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// mustAge back-dates a path so that it counts as stale.
func mustAge(t *testing.T, path string, age time.Duration) {
	t.Helper()
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// =========================================================================
// Positive 3D Tests
// =========================================================================

func TestScanLocalWorkstation_Positive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	repoA := filepath.Join(tmpDir, "repo-a")
	mustMkdirAll(t, filepath.Join(repoA, ".git"))
	mustWriteFile(t, filepath.Join(repoA, "AGENTS.md"), "# Rules")

	repoB := filepath.Join(tmpDir, "repo-b")
	mustMkdirAll(t, filepath.Join(repoB, ".git"))

	staleWt := filepath.Join(tmpDir, "k8s-worktrees", "task-123")
	mustMkdirAll(t, staleWt)
	mustAge(t, staleWt, DefaultStaleWorktreeAge+time.Hour)

	freshWt := filepath.Join(tmpDir, "k8s-worktrees", "task-live")
	mustMkdirAll(t, freshWt)

	rep, err := ScanLocalWorkstation(ctx, tmpDir)
	if err != nil {
		t.Fatalf("ScanLocalWorkstation failed: %v", err)
	}

	if rep.DevReposCount != 2 {
		t.Fatalf("expected 2 dev repos, got: %d", rep.DevReposCount)
	}
	if len(rep.MissingRulesRepos) != 1 || rep.MissingRulesRepos[0] != "repo-b" {
		t.Fatalf("expected repo-b in missing rules, got: %v", rep.MissingRulesRepos)
	}
	if len(rep.StaleWorktrees) != 1 {
		t.Fatalf("expected only the back-dated worktree to be stale, got: %v", rep.StaleWorktrees)
	}
	if rep.StaleWorktrees[0] != filepath.Join("k8s-worktrees", "task-123") {
		t.Fatalf("unexpected stale worktree: %v", rep.StaleWorktrees)
	}
}

func TestScanLocalWorkstation_Boundary_FreshWorktreeIsNotStale(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	mustMkdirAll(t, filepath.Join(tmpDir, "praetor-worktrees", "just-created"))

	rep, err := ScanLocalWorkstation(ctx, tmpDir)
	if err != nil {
		t.Fatalf("ScanLocalWorkstation failed: %v", err)
	}
	if len(rep.StaleWorktrees) != 0 {
		t.Fatalf("a worktree created seconds ago must not be reported stale, got: %v", rep.StaleWorktrees)
	}
}

func TestScanLocalWorkstation_NestedOrgLayout(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	repoC := filepath.Join(tmpDir, "vmafx", "vmafx")
	mustMkdirAll(t, filepath.Join(repoC, ".git"))
	mustWriteFile(t, filepath.Join(repoC, "AGENTS.md"), "# Rules")

	repoD := filepath.Join(tmpDir, "vmafx", "pelorus")
	mustMkdirAll(t, filepath.Join(repoD, ".git"))

	rep, err := ScanLocalWorkstation(ctx, tmpDir)
	if err != nil {
		t.Fatalf("ScanLocalWorkstation failed: %v", err)
	}

	if rep.DevReposCount != 2 {
		t.Fatalf("expected 2 dev repos in nested layout, got: %d", rep.DevReposCount)
	}
	expectedMissing := filepath.Join("vmafx", "pelorus")
	if len(rep.MissingRulesRepos) != 1 || rep.MissingRulesRepos[0] != expectedMissing {
		t.Fatalf("expected %s in missing rules, got: %v", expectedMissing, rep.MissingRulesRepos)
	}
}

func TestAuditSkills_Positive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	mustWriteFile(t, filepath.Join(tmpDir, "skills", "skill-a", "SKILL.md"), "skill a")
	mustWriteFile(t, filepath.Join(tmpDir, "config", "skills", "skill-a", "SKILL.md"), "skill a")
	mustWriteFile(t, filepath.Join(tmpDir, "GEMINI.md.bak-12345"), "backup")

	rep, err := AuditSkills(ctx, tmpDir, "")
	if err != nil {
		t.Fatalf("AuditSkills failed: %v", err)
	}

	if rep.TotalSkills != 2 {
		t.Fatalf("expected 2 total skills, got: %d", rep.TotalSkills)
	}
	if rep.UniqueSkills != 1 {
		t.Fatalf("expected 1 unique skill, got: %d", rep.UniqueSkills)
	}
	if len(rep.Duplicates["skill-a"]) != 2 {
		t.Fatalf("expected duplicate for skill-a, got: %v", rep.Duplicates)
	}
	if len(rep.StaleBackups) != 1 {
		t.Fatalf("expected 1 stale backup, got: %v", rep.StaleBackups)
	}
}

// TestAuditSkills_GeminiBaseDirIsNotDoubleCounted covers the CLI default path, where
// baseDir is ~/.gemini itself: the "direct-*" roots are then the very same directories as
// the "gemini-*" roots and must be scanned once, not twice.
func TestAuditSkills_GeminiBaseDirIsNotDoubleCounted(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	geminiDir := filepath.Join(home, ".gemini")
	mustWriteFile(t, filepath.Join(geminiDir, "skills", "solo", "SKILL.md"), "solo")

	rep, err := AuditSkills(ctx, geminiDir, "")
	if err != nil {
		t.Fatalf("AuditSkills failed: %v", err)
	}
	if rep.TotalSkills != 1 {
		t.Fatalf("expected 1 skill manifest, got: %d (%v)", rep.TotalSkills, rep.Duplicates)
	}
	if len(rep.Duplicates) != 0 {
		t.Fatalf("a single skill must not be a duplicate of itself, got: %v", rep.Duplicates)
	}
}

func TestExtractMemoryInsights_Positive(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	logDir := filepath.Join(root, "convo-1", ".system_generated", "logs")
	mustWriteFile(t, filepath.Join(logDir, "transcript.jsonl"), "{\"text\":\"nothing here\"}\n{\"text\":\"HISS-02 bound\"}\n")

	insights, err := ExtractMemoryInsights(ctx, root)
	if err != nil {
		t.Fatalf("ExtractMemoryInsights failed: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got: %d", len(insights))
	}
	if insights[0].Source != "convo-1" || insights[0].Category != "governance-invariant" {
		t.Fatalf("unexpected insight: %+v", insights[0])
	}
}

// TestExtractMemoryInsights_Boundary_HugeFirstLine pins the bufio.Scanner buffer fix: a
// transcript whose first line exceeds the 64 KiB default must not abort the scan.
func TestExtractMemoryInsights_Boundary_HugeFirstLine(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	logDir := filepath.Join(root, "convo-big", ".system_generated", "logs")

	huge := make([]byte, 70000)
	for i := range huge {
		huge[i] = 'x'
	}
	body := string(huge) + "\n{\"text\":\"HISS-04 complexity\"}\n"
	mustWriteFile(t, filepath.Join(logDir, "transcript.jsonl"), body)

	insights, err := ExtractMemoryInsights(ctx, root)
	if err != nil {
		t.Fatalf("ExtractMemoryInsights failed: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected the insight behind the oversized line, got: %d", len(insights))
	}
}

func TestExtractMemoryInsights_Boundary_EmptyAndMissingRoots(t *testing.T) {
	ctx := context.Background()

	insights, err := ExtractMemoryInsights(ctx, "")
	if err != nil {
		t.Fatalf("empty root must not error: %v", err)
	}
	if len(insights) != 0 {
		t.Fatalf("expected 0 insights for an empty root, got: %d", len(insights))
	}

	insights, err = ExtractMemoryInsights(ctx, filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("absent root must not error: %v", err)
	}
	if len(insights) != 0 {
		t.Fatalf("expected 0 insights for an absent root, got: %d", len(insights))
	}
}

func TestExtractMemoryInsights_Negative_NotADirectory(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "brain.txt")
	mustWriteFile(t, file, "not a directory")

	if _, err := ExtractMemoryInsights(ctx, file); err == nil {
		t.Fatal("expected an error when the brain path is not a directory")
	}
}

func TestExtractMemoryInsights_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := ExtractMemoryInsights(ctx, t.TempDir()); err == nil {
		t.Fatal("expected an error on a cancelled context")
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

func TestScanLocalWorkstation_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ScanLocalWorkstation(ctx, t.TempDir())
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestScanLocalWorkstation_Negative_InvalidDir(t *testing.T) {
	ctx := context.Background()
	_, err := ScanLocalWorkstation(ctx, filepath.Join(t.TempDir(), "nonexistent"))
	if err == nil {
		t.Fatal("expected error on nonexistent directory")
	}
}

func TestAuditSkills_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := AuditSkills(ctx, t.TempDir(), "")
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestAuditSkills_Boundary_AbsentBaseDir(t *testing.T) {
	ctx := context.Background()
	rep, err := AuditSkills(ctx, filepath.Join(t.TempDir(), "absent"), "")
	if err != nil {
		t.Fatalf("an absent base dir must yield an empty report, got: %v", err)
	}
	if rep.TotalSkills != 0 || rep.UniqueSkills != 0 || len(rep.Duplicates) != 0 {
		t.Fatalf("expected an empty report, got: %+v", rep)
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

func TestScanLocalWorkstation_Boundary_EmptyDir(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	rep, err := ScanLocalWorkstation(ctx, tmpDir)
	if err != nil {
		t.Fatalf("ScanLocalWorkstation on empty dir failed: %v", err)
	}
	if rep.DevReposCount != 0 {
		t.Fatalf("expected 0 repos in empty dir, got: %d", rep.DevReposCount)
	}
	if len(rep.StaleWorktrees) != 0 {
		t.Fatalf("expected 0 stale worktrees, got: %d", len(rep.StaleWorktrees))
	}
}

func TestDeduplicateSkills_PositiveAndDryRun(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	geminiSkills := filepath.Join(tmpDir, ".gemini", "skills", "my-skill")
	configSkills := filepath.Join(tmpDir, ".gemini", "config", "skills", "my-skill")
	mustWriteFile(t, filepath.Join(geminiSkills, "SKILL.md"), "data")
	mustWriteFile(t, filepath.Join(configSkills, "SKILL.md"), "data")

	rep, err := AuditSkills(ctx, filepath.Join(tmpDir, ".gemini"), "")
	if err != nil {
		t.Fatal(err)
	}

	dRep, err := DeduplicateSkills(ctx, rep, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dRep.PrunedSkills) != 1 {
		t.Fatalf("expected 1 pruned skill in dry-run, got: %d", len(dRep.PrunedSkills))
	}
	if _, err := os.Stat(geminiSkills); os.IsNotExist(err) {
		t.Fatal("dry-run should not delete files")
	}

	dRepLive, err := DeduplicateSkills(ctx, rep, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(dRepLive.PrunedSkills) != 1 {
		t.Fatalf("expected 1 pruned skill in live, got: %d", len(dRepLive.PrunedSkills))
	}
	if _, err := os.Stat(geminiSkills); !os.IsNotExist(err) {
		t.Fatal("live deduplication should remove redundant skill folder")
	}
	if _, err := os.Stat(configSkills); os.IsNotExist(err) {
		t.Fatal("canonical config skill folder must be preserved")
	}
}

// TestDeduplicateSkills_Negative_NeverTouchesOtherAgentRoots pins the blast-radius fix:
// only ~/.gemini/skills is a prune candidate. A same-named skill belonging to Claude,
// Codex, Copilot, the universal root or a repository working tree must survive.
func TestDeduplicateSkills_Negative_NeverTouchesOtherAgentRoots(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	repoSkills := filepath.Join(t.TempDir(), "checkout", ".agents", "skills")

	roots := map[string]string{
		"claude":       filepath.Join(home, ".claude", "skills", "commit"),
		"codex":        filepath.Join(home, ".codex", "skills", "commit"),
		"copilot":      filepath.Join(home, ".copilot", "skills", "commit"),
		"universal":    filepath.Join(home, ".agents", "skills", "commit"),
		"repo":         filepath.Join(repoSkills, "commit"),
		"geminiConfig": filepath.Join(home, ".gemini", "config", "skills", "commit"),
	}
	for _, dir := range roots {
		mustWriteFile(t, filepath.Join(dir, "SKILL.md"), "commit skill")
	}

	rep, err := AuditSkills(ctx, filepath.Join(home, ".gemini"), repoSkills)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Duplicates["commit"]) != len(roots) {
		t.Fatalf("expected %d copies of commit, got: %v", len(roots), rep.Duplicates["commit"])
	}

	dRep, err := DeduplicateSkills(ctx, rep, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(dRep.PrunedSkills) != 0 {
		t.Fatalf("no gemini-root copy exists, nothing may be pruned, got: %v", dRep.PrunedSkills)
	}
	for label, dir := range roots {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s skill directory was destroyed: %v", label, err)
		}
	}
}

func TestDeduplicateSkills_Negative_CancelledContextAndNilReport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DeduplicateSkills(ctx, &SkillAuditReport{}, true); err == nil {
		t.Fatal("expected an error on a cancelled context")
	}
	if _, err := DeduplicateSkills(context.Background(), nil, true); err == nil {
		t.Fatal("expected an error for a nil audit report")
	}
}

func TestDeduplicateSkills_Boundary_NoDuplicates(t *testing.T) {
	dRep, err := DeduplicateSkills(context.Background(), &SkillAuditReport{
		Duplicates: map[string][]SkillLocation{},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if dRep.ReclaimedEntries != 0 || len(dRep.PrunedSkills) != 0 {
		t.Fatalf("expected nothing pruned, got: %+v", dRep)
	}
}

func TestPurgeBackups_Positive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	backupFile := filepath.Join(tmpDir, "GEMINI.md.bak-999")
	mustWriteFile(t, backupFile, "backup")

	purged, err := PurgeBackups(ctx, tmpDir, []string{"GEMINI.md.bak-999"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(purged) != 1 {
		t.Fatalf("expected 1 purged file in dry-run, got: %d", len(purged))
	}
	if _, err := os.Stat(backupFile); os.IsNotExist(err) {
		t.Fatal("dry-run should not delete backup file")
	}

	_, err = PurgeBackups(ctx, tmpDir, []string{"GEMINI.md.bak-999"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(backupFile); !os.IsNotExist(err) {
		t.Fatal("live purge should delete backup file")
	}
}

func TestPurgeBackups_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PurgeBackups(ctx, t.TempDir(), []string{"GEMINI.md.bak-1"}, true); err == nil {
		t.Fatal("expected an error on a cancelled context")
	}
}

func TestPurgeBackups_Boundary_EmptyInput(t *testing.T) {
	purged, err := PurgeBackups(context.Background(), t.TempDir(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(purged) != 0 {
		t.Fatalf("expected nothing purged, got: %v", purged)
	}
}

func TestOnboardRepository_Positive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "sample-repo")
	mustMkdirAll(t, repoPath)
	mustWriteFile(t, filepath.Join(repoPath, "go.mod"), "module sample\n")

	plan, err := OnboardRepository(ctx, repoPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Archetype != "framework" {
		t.Fatalf("expected framework archetype, got: %s", plan.Archetype)
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".standards.yaml")); !os.IsNotExist(err) {
		t.Fatal("dry-run should not create .standards.yaml")
	}

	planLive, err := OnboardRepository(ctx, repoPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(planLive.Actions) == 0 {
		t.Fatal("expected actions in live onboarding plan")
	}
	for _, name := range []string{".standards.yaml", ".standards-baseline.json", ".standards.lock", "AGENTS.md", "CLAUDE.md"} {
		if _, err := os.Stat(filepath.Join(repoPath, name)); err != nil {
			t.Fatalf("live onboarding should create %s: %v", name, err)
		}
	}

	manifest, err := os.ReadFile(filepath.Join(repoPath, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), "visibility: public") {
		t.Fatalf("onboarding must not declare an unknown repository public:\n%s", manifest)
	}
	if strings.Contains(string(manifest), "owner: cordanaLLM") {
		t.Fatalf("onboarding must not invent an owner:\n%s", manifest)
	}

	agents, err := os.ReadFile(filepath.Join(repoPath, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(agents), "make verify-all") {
		t.Fatalf("scaffolded AGENTS.md must not reference a make target onboarding never writes:\n%s", agents)
	}
}

func TestOnboardRepository_Negative_NotADirectory(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "notes.txt")
	mustWriteFile(t, file, "just a file")

	_, err := OnboardRepository(ctx, file, true)
	if err == nil {
		t.Fatal("expected an error when the repo path is a file")
	}
	if !errors.Is(err, ErrNotADirectory) {
		t.Fatalf("expected ErrNotADirectory, got: %v", err)
	}
	if strings.Contains(err.Error(), "%!w") {
		t.Fatalf("error message must not render a nil wrapped error: %v", err)
	}
}

func TestOnboardRepository_Negative_MissingPath(t *testing.T) {
	_, err := OnboardRepository(context.Background(), filepath.Join(t.TempDir(), "absent"), true)
	if err == nil {
		t.Fatal("expected an error for an absent repository path")
	}
}

func TestOnboardRepository_Boundary_EmptyRepo(t *testing.T) {
	repoPath := t.TempDir()
	plan, err := OnboardRepository(context.Background(), repoPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Archetype != "template-seed" {
		t.Fatalf("expected template-seed for an empty repository, got: %s", plan.Archetype)
	}
}
