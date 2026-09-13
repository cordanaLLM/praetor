package harvester

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAuditSkillsReportsOptionalAndConfiguredRoots(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo-skills")
	mustWriteFile(t, filepath.Join(root, "valid", "SKILL.md"), "skill")
	report, err := AuditSkills(t.Context(), base, root)
	if err != nil || !report.Complete || report.TotalSkills != 1 {
		t.Fatalf("valid configured root was not complete: report=%+v err=%v", report, err)
	}
	var optional, configured bool
	for _, status := range report.RootStatuses {
		optional = optional || status.Status == "not_applicable"
		configured = configured || status.Origin == OriginRepoLocal && status.Status == "scanned"
	}
	if !optional || !configured {
		t.Fatalf("missing root status distinction: %+v", report.RootStatuses)
	}
}

func TestAuditSkillsRejectsInvalidConfiguredRootAndNilContext(t *testing.T) {
	base := t.TempDir()
	var absentContext context.Context
	if _, err := AuditSkills(absentContext, base, ""); err == nil {
		t.Fatal("nil context accepted")
	}
	missing := filepath.Join(base, "missing")
	report, err := AuditSkills(context.Background(), base, missing)
	if err == nil || report == nil || report.Complete {
		t.Fatalf("missing configured root was reported complete: report=%+v err=%v", report, err)
	}
	if testSkillRootStatus(t, report, missing).Status != "failed" {
		t.Fatalf("missing configured root status: %+v", report.RootStatuses)
	}
	fileRoot := filepath.Join(base, "skills-file")
	if err := os.WriteFile(fileRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AuditSkills(context.Background(), base, fileRoot); err == nil {
		t.Fatal("regular file configured root accepted")
	}
}

func TestAuditSkillsReportsExactDirectoryBound(t *testing.T) {
	for _, count := range []int{MaxSkillsScan, MaxSkillsScan + 1} {
		t.Run(fmt.Sprintf("entries-%d", count), func(t *testing.T) {
			base, root := t.TempDir(), filepath.Join(t.TempDir(), "skills")
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < count; i++ {
				if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("skill-%04d", i)), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			report, err := AuditSkills(t.Context(), base, root)
			if count == MaxSkillsScan && (err != nil || !report.Complete) {
				t.Fatalf("exact bound rejected: report=%+v err=%v", report, err)
			}
			if count > MaxSkillsScan && (err == nil || report.Complete) {
				t.Fatalf("over-bound scan passed: report=%+v err=%v", report, err)
			}
		})
	}
}

func TestAuditSkillsRejectsLinkedSkillAndDedupeIncomplete(t *testing.T) {
	base, root, outside := t.TempDir(), filepath.Join(t.TempDir(), "skills"), filepath.Join(t.TempDir(), "outside")
	mustWriteFile(t, filepath.Join(outside, "SKILL.md"), "outside")
	if err := os.MkdirAll(filepath.Join(root, "linked"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	report, err := AuditSkills(t.Context(), base, root)
	if err == nil || report.Complete {
		t.Fatalf("linked skill accepted: report=%+v err=%v", report, err)
	}
	if _, err := DeduplicateSkills(t.Context(), report, true); err == nil {
		t.Fatal("dedupe accepted incomplete audit")
	}
	if _, err := os.Stat(filepath.Join(outside, "SKILL.md")); errors.Is(err, os.ErrNotExist) {
		t.Fatal("linked target disappeared")
	}
}

func TestAuditSkillsRejectsLinkedConfiguredRoot(t *testing.T) {
	base, target := t.TempDir(), t.TempDir()
	linked := filepath.Join(base, "repo-skills")
	if err := os.Symlink(target, linked); err != nil {
		t.Fatal(err)
	}
	report, err := AuditSkills(t.Context(), base, linked)
	if err == nil || report.Complete || testSkillRootStatus(t, report, linked).Status != "failed" {
		t.Fatalf("linked configured root accepted: report=%+v err=%v", report, err)
	}
}

func TestAuditSkillsRetainsConfiguredStatusOnDuplicateRoot(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".gemini")
	root := filepath.Join(base, "skills")
	mustWriteFile(t, filepath.Join(root, "one", "SKILL.md"), "one")
	report, err := AuditSkills(t.Context(), base, root)
	if err != nil || !report.Complete {
		t.Fatalf("duplicate configured root failed: report=%+v err=%v", report, err)
	}
	for _, status := range report.RootStatuses {
		if status.Path == root && !status.Configured {
			t.Fatalf("duplicate root lost configured authority: %+v", report.RootStatuses)
		}
	}
}

func testSkillRootStatus(t *testing.T, report *SkillAuditReport, path string) SkillRootStatus {
	t.Helper()
	for _, status := range report.RootStatuses {
		if status.Path == path {
			return status
		}
	}
	t.Fatalf("missing status for %s", path)
	return SkillRootStatus{}
}

func TestAuditSkillsConfiguredAliasMissingAndBackupOverflow(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".gemini")
	missing := filepath.Join(base, "skills")
	report, err := AuditSkills(t.Context(), base, missing)
	if err == nil || report.Complete || !testSkillRootStatus(t, report, missing).Configured {
		t.Fatalf("configured alias downgraded: %+v %v", report, err)
	}
	root := t.TempDir()
	for i := 0; i < MaxSkillsScan+1; i++ {
		mustWriteFile(t, filepath.Join(root, fmt.Sprintf("GEMINI.md.bak-%03d", i)), "backup")
	}
	report, err = AuditSkills(t.Context(), root, "")
	if err == nil || report.Complete || testSkillRootStatus(t, report, root).Status != "truncated" {
		t.Fatalf("backup overflow accepted: %+v %v", report, err)
	}
}
