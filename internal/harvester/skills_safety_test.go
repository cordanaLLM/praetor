package harvester

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDeduplicateSkillsPreservesGenericDirectRoots(t *testing.T) {
	home := t.TempDir()
	paths := writeSkillCopies(t, home, "skills", "config/skills")
	report, err := AuditSkills(context.Background(), home, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, dryRun := range []bool{true, false} {
		result, err := DeduplicateSkills(context.Background(), report, dryRun)
		if err != nil || len(result.PrunedSkills) != 0 {
			t.Fatalf("dryRun=%t: generic skill roots became prune candidates: %+v, %v", dryRun, result, err)
		}
		assertSkillCopies(t, paths)
	}
}

func TestDeduplicateSkillsRequiresGeminiConfigOrigin(t *testing.T) {
	home := t.TempDir()
	paths := writeSkillCopies(t, home, ".gemini/skills", "config/skills")
	report, err := AuditSkills(context.Background(), home, "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := DeduplicateSkills(context.Background(), report, false)
	if err != nil || len(result.PrunedSkills) != 0 {
		t.Fatalf("non-Gemini config authorized Gemini deletion: %+v, %v", result, err)
	}
	assertSkillCopies(t, paths)
}

func TestDeduplicateSkillsPrunesOnlyExplicitGeminiShadow(t *testing.T) {
	for _, geminiBase := range []bool{false, true} {
		t.Run(map[bool]string{false: "home", true: "gemini"}[geminiBase], func(t *testing.T) {
			home := t.TempDir()
			paths := writeSkillCopies(t, home, ".gemini/skills", ".gemini/config/skills", "skills", "config/skills")
			base := home
			if geminiBase {
				base = filepath.Join(home, ".gemini")
			}
			report, err := AuditSkills(context.Background(), base, "")
			if err != nil {
				t.Fatal(err)
			}
			result, err := DeduplicateSkills(context.Background(), report, false)
			if err != nil || len(result.PrunedSkills) != 1 || result.PrunedSkills[0] != filepath.Dir(paths[0]) {
				t.Fatalf("wrong prune candidates: %+v, %v", result, err)
			}
			if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
				t.Fatalf("Gemini root copy remains: %v", err)
			}
			assertSkillCopies(t, paths[1:])
		})
	}
}

func writeSkillCopies(t *testing.T, home string, roots ...string) []string {
	t.Helper()
	paths := make([]string, 0, len(roots))
	for _, root := range roots {
		path := filepath.Join(home, filepath.FromSlash(root), "shared", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("preserve "+path), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

func assertSkillCopies(t *testing.T, paths []string) {
	t.Helper()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "preserve "+path {
			t.Errorf("skill copy changed: %s: %q, %v", path, data, err)
		}
	}
}

func TestDeduplicateSkillsRejectsForgedOrigins(t *testing.T) {
	home := t.TempDir()
	paths := writeSkillCopies(t, home, "outside", ".gemini/config/skills")
	report := &SkillAuditReport{Duplicates: map[string][]SkillLocation{
		"shared": {{Path: paths[0], Origin: OriginGeminiRoot}, {Path: paths[1], Origin: OriginGeminiConfig}},
	}}
	result, err := DeduplicateSkills(context.Background(), report, false)
	if err == nil || result == nil || result.ReclaimedEntries != 0 || len(result.Errors) == 0 {
		t.Fatalf("forged origin accepted: %+v, %v", result, err)
	}
	assertSkillCopies(t, paths)
}

func TestDeduplicateSkillsRejectsDifferentHomePair(t *testing.T) {
	root := writeSkillCopies(t, t.TempDir(), ".gemini/skills")
	config := writeSkillCopies(t, t.TempDir(), ".gemini/config/skills")
	report := &SkillAuditReport{Duplicates: map[string][]SkillLocation{
		"shared": {{Path: root[0], Origin: OriginGeminiRoot}, {Path: config[0], Origin: OriginGeminiConfig}},
	}}
	result, err := DeduplicateSkills(context.Background(), report, false)
	if err == nil || result == nil || result.ReclaimedEntries != 0 {
		t.Fatalf("different-home config authorized deletion: %+v, %v", result, err)
	}
	assertSkillCopies(t, append(root, config...))
}

func TestPurgeBackupsPreflightsAllNames(t *testing.T) {
	for _, bad := range []string{"../outside", "/outside", "GEMINI.md", "GEMINI.md.bak-", "nested/GEMINI.md.bak-1"} {
		t.Run(bad, func(t *testing.T) {
			root := t.TempDir()
			gemini := filepath.Join(root, ".gemini")
			good := filepath.Join(gemini, "GEMINI.md.bak-1")
			mustWriteFile(t, good, "backup")
			mustWriteFile(t, filepath.Join(root, "outside"), "outside")
			result, err := PurgeBackups(context.Background(), gemini, []string{filepath.Base(good), bad}, false)
			if err == nil || len(result) != 0 {
				t.Fatalf("invalid batch accepted: %v, %v", result, err)
			}
			if data, err := os.ReadFile(good); err != nil || string(data) != "backup" {
				t.Fatalf("preflight removed valid backup: %q, %v", data, err)
			}
			if data, err := os.ReadFile(filepath.Join(root, "outside")); err != nil || string(data) != "outside" {
				t.Fatalf("preflight changed outside file: %q, %v", data, err)
			}
		})
	}
}

func TestPurgeBackupsRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	mustWriteFile(t, outside, "outside")
	gemini := filepath.Join(root, ".gemini")
	mustMkdirAll(t, gemini)
	if err := os.Symlink(outside, filepath.Join(gemini, "GEMINI.md.bak-1")); err != nil {
		t.Fatal(err)
	}
	result, err := PurgeBackups(context.Background(), gemini, []string{"GEMINI.md.bak-1"}, false)
	if err == nil || len(result) != 0 {
		t.Fatalf("symlink backup accepted: %v, %v", result, err)
	}
}

func TestDeduplicateSkillsPreflightsAllGroups(t *testing.T) {
	home := t.TempDir()
	report := skillRemovalFixture(t, home, "a-valid")
	outside := filepath.Join(home, "outside", "z-invalid", "SKILL.md")
	mustWriteFile(t, outside, "outside")
	report.Duplicates["z-invalid"] = []SkillLocation{{Path: outside, Origin: OriginGeminiRoot}}
	result, err := DeduplicateSkills(context.Background(), report, false)
	if err == nil || result.ReclaimedEntries != 0 || len(result.PrunedSkills) != 0 {
		t.Fatalf("invalid later group did not stop preflight: %+v, %v", result, err)
	}
	if _, err := os.Stat(report.Duplicates["a-valid"][0].Path); err != nil {
		t.Fatalf("valid earlier group was deleted: %v", err)
	}
}

func TestDeletionRejectsSymlinkAncestors(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	mustWriteFile(t, filepath.Join(outside, "shared", "SKILL.md"), "outside")
	paths := writeSkillCopies(t, home, ".gemini/config/skills")
	if err := os.Symlink(outside, filepath.Join(home, ".gemini", "skills")); err != nil {
		t.Fatal(err)
	}
	report := &SkillAuditReport{Duplicates: map[string][]SkillLocation{
		"shared": {{Path: filepath.Join(home, ".gemini", "skills", "shared", "SKILL.md"), Origin: OriginGeminiRoot},
			{Path: paths[0], Origin: OriginGeminiConfig}},
	}}
	if _, err := DeduplicateSkills(context.Background(), report, false); err == nil {
		t.Fatal("symlink skill ancestor accepted")
	}
	mustWriteFile(t, filepath.Join(outside, "GEMINI.md.bak-1"), "outside")
	if _, err := PurgeBackups(context.Background(), filepath.Join(home, ".gemini", "skills"), []string{"GEMINI.md.bak-1"}, false); err == nil {
		t.Fatal("symlink backup root accepted")
	}
	for _, relative := range []string{"shared/SKILL.md", "GEMINI.md.bak-1"} {
		if data, err := os.ReadFile(filepath.Join(outside, relative)); err != nil || string(data) != "outside" {
			t.Errorf("outside path changed: %q, %v", data, err)
		}
	}
}

func TestDeduplicateSkillsCancellationPreservesProgress(t *testing.T) {
	home := t.TempDir()
	report := skillRemovalFixture(t, home, "a-first", "b-second")
	first := filepath.Join(home, ".gemini", "skills", "a-first")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &cancelAfterRemoval{Context: ctx, cancel: cancel, path: first}
	result, err := DeduplicateSkills(observed, report, false)
	if !errors.Is(err, context.Canceled) || result.ReclaimedEntries != 1 || len(result.PrunedSkills) != 1 || len(result.Errors) == 0 {
		t.Fatalf("incorrect cancellation progress: %+v, %v", result, err)
	}
	if _, err := os.Stat(report.Duplicates["b-second"][0].Path); err != nil {
		t.Fatalf("mutation continued after cancellation: %v", err)
	}
}

func TestPurgeBackupsCancellationPreservesProgress(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "GEMINI.md.bak-1")
	mustWriteFile(t, first, "first")
	mustWriteFile(t, filepath.Join(root, "GEMINI.md.bak-2"), "second")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &cancelAfterRemoval{Context: ctx, cancel: cancel, path: first}
	purged, err := PurgeBackups(observed, root, []string{"GEMINI.md.bak-1", "GEMINI.md.bak-2"}, false)
	if !errors.Is(err, context.Canceled) || len(purged) != 1 || purged[0] != first {
		t.Fatalf("incorrect cancellation progress: %v, %v", purged, err)
	}
	if _, err := os.Stat(filepath.Join(root, "GEMINI.md.bak-2")); err != nil {
		t.Fatalf("mutation continued after cancellation: %v", err)
	}
}

func TestDeduplicateSkillsReportsPartialFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("POSIX unprivileged deletion permissions")
	}
	first, second := t.TempDir(), t.TempDir()
	report := skillRemovalFixture(t, first, "a-first")
	report.Duplicates["b-second"] = skillRemovalFixture(t, second, "b-second").Duplicates["b-second"]
	protected := filepath.Join(second, ".gemini", "skills")
	if err := os.Chmod(protected, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(protected, 0700); err != nil {
			t.Errorf("restore fixture directory: %v", err)
		}
	})
	result, err := DeduplicateSkills(context.Background(), report, false)
	if err == nil || result.ReclaimedEntries != 1 || len(result.PrunedSkills) != 1 || len(result.Errors) == 0 {
		t.Fatalf("failed removal counted as reclaimed: %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(protected, "b-second")); err != nil {
		t.Fatalf("failed target directory should remain: %v", err)
	}
}

func TestDeletionInputBoundsAndDryRunCounts(t *testing.T) {
	for _, count := range []int{MaxSkillsScan, MaxSkillsScan + 1} {
		report := &SkillAuditReport{Duplicates: make(map[string][]SkillLocation)}
		backups := make([]string, count)
		for i := 0; i < count; i++ {
			report.Duplicates[fmt.Sprint(i)] = nil
			backups[i] = fmt.Sprintf("GEMINI.md.bak-%d", i)
		}
		_, skillErr := DeduplicateSkills(context.Background(), report, false)
		_, backupErr := PurgeBackups(context.Background(), t.TempDir(), backups, false)
		if (skillErr != nil) != (count > MaxSkillsScan) || (backupErr != nil) != (count > MaxSkillsScan) {
			t.Fatalf("incorrect boundary at %d: %v, %v", count, skillErr, backupErr)
		}
	}
	report := skillRemovalFixture(t, t.TempDir(), "preview")
	result, err := DeduplicateSkills(context.Background(), report, true)
	if err != nil || result.ReclaimedEntries != 0 || len(result.PrunedSkills) != 1 {
		t.Fatalf("dry-run counted actual reclamation: %+v, %v", result, err)
	}
}

func skillRemovalFixture(t *testing.T, home string, names ...string) *SkillAuditReport {
	t.Helper()
	report := &SkillAuditReport{Duplicates: make(map[string][]SkillLocation)}
	for _, name := range names {
		root := filepath.Join(home, ".gemini", "skills", name, "SKILL.md")
		config := filepath.Join(home, ".gemini", "config", "skills", name, "SKILL.md")
		mustWriteFile(t, root, "root skill")
		mustWriteFile(t, config, "config skill")
		report.Duplicates[name] = []SkillLocation{{Path: root, Origin: OriginGeminiRoot}, {Path: config, Origin: OriginGeminiConfig}}
	}
	return report
}

type cancelAfterRemoval struct {
	context.Context
	cancel context.CancelFunc
	path   string
}

func (c *cancelAfterRemoval) Err() error {
	if _, err := os.Stat(c.path); os.IsNotExist(err) {
		c.cancel()
	}
	return c.Context.Err()
}
