package dogfood

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

	// Option shapes, shell metacharacters and, since the transport allow-list, every
	// non-network transport git would otherwise resolve: a local path, file:// and ext::.
	for _, bad := range []string{
		"", "   ", "--upload-pack=touch /tmp/pwned", "https://example.com/a;rm -rf /", "https://example.com/$(id)",
		"/srv/private-repo", "file:///etc", "ext::sh", "git://example.com/repo", "HTTP://example.com/repo",
	} {
		if _, vErr := validateRepoURL(bad); !errors.Is(vErr, ErrInvalidRepoURL) {
			t.Errorf("validateRepoURL(%q) = %v, want ErrInvalidRepoURL", bad, vErr)
		}
		if cErr := cloneEphemeralRepo(ctx, bad, t.TempDir(), filepath.Join(t.TempDir(), "clone")); !errors.Is(cErr, ErrInvalidRepoURL) {
			t.Errorf("cloneEphemeralRepo(%q) = %v, want ErrInvalidRepoURL", bad, cErr)
		}
	}
}

// writeCommittedFixture creates a one-commit repository that a clone can resolve without a
// network, and returns its path.
func writeCommittedFixture(t *testing.T, ctx context.Context) string {
	t.Helper()
	fixture := t.TempDir()
	if out, err := util.RunGit(ctx, fixture, "init", "-q"); err != nil {
		t.Skipf("git unavailable: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(fixture, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	if out, err := util.RunGit(ctx, fixture, "add", "a.txt"); err != nil {
		t.Fatalf("add: %v: %s", err, out)
	}
	if out, err := util.RunGit(ctx, fixture, "-c", "user.name=T", "-c", "user.email=t@example.invalid",
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", "fixture"); err != nil {
		t.Fatalf("commit: %v: %s", err, out)
	}
	return fixture
}

// TestCloneEphemeralRepo_Negative_LocalTransportAndTemplateHooks is the regression for the
// ephemeral clone running under the operator's configuration.
//
// Measured before the fix: a file:// URL cloned, the GIT_TEMPLATE_DIR post-checkout hook
// was installed into the sandbox, and it executed during the clone of untrusted content.
func TestCloneEphemeralRepo_Negative_LocalTransportAndTemplateHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook fixture is a POSIX shell script; the transport allow-list is covered platform-neutrally above")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fixture := writeCommittedFixture(t, ctx)
	aux := t.TempDir()
	marker := filepath.Join(aux, "template-hook-ran")
	hooks := filepath.Join(aux, "template", "hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatalf("mkdir template hooks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte("#!/bin/sh\n: > "+marker+"\n"), 0o700); err != nil {
		t.Fatalf("write template hook: %v", err)
	}
	t.Setenv("GIT_TEMPLATE_DIR", filepath.Join(aux, "template"))

	target := filepath.Join(t.TempDir(), "clone")
	if err := cloneEphemeralRepo(ctx, "file://"+fixture, t.TempDir(), target); !errors.Is(err, ErrInvalidRepoURL) {
		t.Errorf("file:// clone = %v, want ErrInvalidRepoURL", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the operator's template post-checkout hook ran during an ephemeral clone")
	}
	if _, err := os.Stat(filepath.Join(target, ".git", "hooks", "post-checkout")); err == nil {
		t.Error("the operator's template hook was installed into the ephemeral sandbox")
	}
}

// TestCloneEphemeralRepo_Positive_HTTPSCloneIsIsolated exercises the accepted transport end
// to end against a local HTTPS-shaped remote. The clone must fail because nothing answers,
// not because validation refused it, and it must leave no template hook behind.
func TestCloneEphemeralRepo_Positive_HTTPSCloneIsIsolated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook fixture is a POSIX shell script")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	aux := t.TempDir()
	marker := filepath.Join(aux, "template-hook-ran")
	hooks := filepath.Join(aux, "template", "hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatalf("mkdir template hooks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte("#!/bin/sh\n: > "+marker+"\n"), 0o700); err != nil {
		t.Fatalf("write template hook: %v", err)
	}
	t.Setenv("GIT_TEMPLATE_DIR", filepath.Join(aux, "template"))

	scratch := t.TempDir()
	target := filepath.Join(t.TempDir(), "clone")
	err := cloneEphemeralRepo(ctx, "https://127.0.0.1:1/praetor/absent.git", scratch, target)
	if err == nil {
		t.Fatal("a clone from a closed port must fail")
	}
	if errors.Is(err, ErrInvalidRepoURL) {
		t.Errorf("an https URL must reach git, got %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("the operator's template hook ran for an https clone")
	}
	for _, scratchChild := range []string{"home", "tmp"} {
		if _, statErr := os.Stat(filepath.Join(scratch, scratchChild)); statErr != nil {
			t.Errorf("the clone did not run with its own %s: %v", scratchChild, statErr)
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

	// Boundary: a valid-looking URL that stays local (no network) still validates, and so
	// does the second allowed transport; the scheme check is the only new gate.
	for _, good := range []string{" https://github.com/org/repo.git ", "ssh://git@github.com/org/repo.git"} {
		if _, vErr := validateRepoURL(good); vErr != nil {
			t.Errorf("validateRepoURL(%q) rejected an allowed transport: %v", good, vErr)
		}
	}
}
