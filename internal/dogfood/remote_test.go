package dogfood

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// writeHermeticHost creates a minimal host repository whose AGENTS.md targets are in
// sync so that RunDogfood exercises the full pipeline without touching the real checkout.
func writeHermeticHost(t *testing.T) string {
	t.Helper()
	host := t.TempDir()
	agents := "# Fixture\n\nMinimal AGENTS.md for dogfood tests.\n"
	if err := os.WriteFile(filepath.Join(host, "AGENTS.md"), []byte(agents), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	return host
}

func TestDogfood_Positive_HomeDirInjection(t *testing.T) {
	host := writeHermeticHost(t)
	home := t.TempDir()
	skillDir := filepath.Join(home, ".claude", "skills", "demo-skill")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# demo\n"), 0o600); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reportPath := filepath.Join(t.TempDir(), "report.json")
	rep, err := RunDogfood(ctx, DogfoodOptions{HostRepoPath: host, HomeDir: home, ReportPath: reportPath})
	if err != nil {
		t.Fatalf("RunDogfood: %v", err)
	}
	if rep.TotalSkillsAudited != 1 {
		t.Errorf("TotalSkillsAudited = %d, want 1 (only the injected home is scanned)", rep.TotalSkillsAudited)
	}
	info, err := os.Stat(reportPath)
	if err != nil {
		t.Fatalf("report not written: %v", err)
	}
	if util.ModeIsProtection() && info.Mode().Perm()&0o002 != 0 {
		t.Errorf("report is world-writable: %v", info.Mode())
	}
}

func TestDogfood_Negative_MissingTargetsDirAndBadURL(t *testing.T) {
	host := writeHermeticHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := RunDogfood(ctx, DogfoodOptions{HostRepoPath: host, TargetReposDir: missing, SkipWorkstationSkills: true})
	if err == nil || !strings.Contains(err.Error(), "read targets directory") {
		t.Errorf("missing targets dir: got %v, want wrapped read error", err)
	}

	for _, bad := range []string{"", "   ", "--upload-pack=touch /tmp/pwned", "https://example.com/a;rm -rf /", "https://example.com/$(id)"} {
		if _, vErr := validateRepoURL(bad); !errors.Is(vErr, ErrInvalidRepoURL) {
			t.Errorf("validateRepoURL(%q) = %v, want ErrInvalidRepoURL", bad, vErr)
		}
		if cErr := cloneEphemeralRepo(ctx, bad, t.TempDir()); !errors.Is(cErr, ErrInvalidRepoURL) {
			t.Errorf("cloneEphemeralRepo(%q) = %v, want ErrInvalidRepoURL", bad, cErr)
		}
	}
}

func TestDogfood_Boundary_LocalTargetsAndSkipSkills(t *testing.T) {
	host := writeHermeticHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	targets := t.TempDir()
	// A plain file and a directory without .git are skipped; a git repository counts.
	if err := os.WriteFile(filepath.Join(targets, "README.md"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(targets, "not-a-repo"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	repo := filepath.Join(targets, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if _, err := util.RunGit(ctx, repo, "init", "-q"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}

	rep, err := RunDogfood(ctx, DogfoodOptions{
		HostRepoPath:          host,
		TargetReposDir:        targets,
		MaxScanTargets:        MaxDogfoodTargets + 1, // above the cap: clamped, not rejected
		SkipWorkstationSkills: true,
	})
	if err != nil {
		t.Fatalf("RunDogfood: %v", err)
	}
	if rep.TargetsEvaluated != 1 || len(rep.TargetResults) != 1 || rep.TargetResults[0].RepoName != "repo" {
		t.Errorf("unexpected target results: %+v", rep.TargetResults)
	}
	if rep.TotalSkillsAudited != 0 {
		t.Errorf("skills audited with SkipWorkstationSkills: %d", rep.TotalSkillsAudited)
	}

	// Boundary: a valid-looking URL that stays local (no network) still validates.
	if _, vErr := validateRepoURL(" https://github.com/org/repo.git "); vErr != nil {
		t.Errorf("trimmed URL rejected: %v", vErr)
	}
}
