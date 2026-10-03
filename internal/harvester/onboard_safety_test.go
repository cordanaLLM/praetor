package harvester

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// hangingProgram outlives any deadline a test sets, so only cancellation ends it.
const hangingProgram = `package main

import "time"

func main() { time.Sleep(30 * time.Second) }
`

func TestOnboardRepositoryCancellationDuringIdentity(t *testing.T) {
	bin, repo := t.TempDir(), t.TempDir()
	testsupport.BuildExecutable(t, bin, "git", hangingProgram)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := OnboardRepository(ctx, repo, false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost deadline error: %v", err)
	}
	entries, err := os.ReadDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("onboarding wrote after cancellation: %v", entries)
	}
}

func TestOnboardRepositoryConfinesGeneratedOutputs(t *testing.T) {
	for _, rel := range []string{"CLAUDE.md", ".vscode"} {
		t.Run(rel, func(t *testing.T) {
			repo, outside := t.TempDir(), t.TempDir()
			target := filepath.Join(outside, "protected")
			if rel == ".vscode" {
				target = outside
			} else {
				mustWriteFile(t, target, "keep")
			}
			if err := os.Symlink(target, filepath.Join(repo, rel)); err != nil {
				t.Fatal(err)
			}
			_, err := OnboardRepository(context.Background(), repo, false)
			if err == nil {
				t.Fatal("outside output symlink accepted")
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			if rel == ".vscode" && len(entries) != 0 {
				t.Fatalf("wrote outside repo: %v", entries)
			}
			if rel == "CLAUDE.md" {
				data, err := os.ReadFile(target)
				if err != nil || string(data) != "keep" {
					t.Fatalf("outside content changed: %q %v", data, err)
				}
			}
		})
	}
}

// onboardFixtureArchetype is the caller-pinned profile source the fixture lock hashes.
const onboardFixtureArchetype = "id: framework\nname: caller-pinned fixture archetype\n"

// verifiedOnboardFixture supplies a source-less consumer lock whose pins are declared
// by the caller. Production onboarding must preserve and validate those caller pins.
func verifiedOnboardFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	mustWriteFile(t, filepath.Join(repo, ".standards.yaml"), "version: 1\nprofiles: [framework]\n")
	testsupport.WritePinnedLock(t, repo, "framework", onboardFixtureArchetype)
	return repo
}

func writeOnboardCatalog(t *testing.T, repo, body string) {
	t.Helper()
	dir := filepath.Join(repo, ".config", "archetypes")
	mustMkdirAll(t, dir)
	mustWriteFile(t, filepath.Join(dir, "framework.yaml"), body)
}

func TestOnboardRepositoryVerifiedCallerLock(t *testing.T) {
	repo := verifiedOnboardFixture(t)
	writeOnboardCatalog(t, repo, onboardFixtureArchetype)
	before, err := os.ReadFile(filepath.Join(repo, ".standards.lock"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := OnboardRepository(context.Background(), repo, false)
	if err != nil || !plan.LockVerified || plan.LockStatus != config.LockStatusVerified {
		t.Fatalf("valid caller lock rejected: %+v %v", plan, err)
	}
	after, err := os.ReadFile(filepath.Join(repo, ".standards.lock"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("caller pins changed: %v", err)
	}
}

// A lock whose catalog is not materialized is valid but unverified: onboarding does not
// fail on it, and it does not claim the content digests were checked.
func TestOnboardRepositoryReportsSourceLessLockUnverifiable(t *testing.T) {
	repo := verifiedOnboardFixture(t)
	plan, err := OnboardRepository(context.Background(), repo, false)
	if err != nil {
		t.Fatalf("source-less caller lock must not make onboarding incomplete: %v", err)
	}
	if plan.LockVerified || plan.LockStatus != config.LockStatusUnverifiable {
		t.Fatalf("source-less lock reported as verified: %+v", plan)
	}
}

func TestOnboardRepositoryRejectsRenamedCatalogArchetype(t *testing.T) {
	repo := verifiedOnboardFixture(t)
	writeOnboardCatalog(t, repo, "id: renamed\nname: caller-pinned fixture archetype\n")
	plan, err := OnboardRepository(context.Background(), repo, false)
	if !errors.Is(err, ErrOnboardingIncomplete) || !errors.Is(err, config.ErrLockSourceMissing) {
		t.Fatalf("renamed archetype id skipped digest verification: %v", err)
	}
	if plan == nil || plan.LockVerified || plan.LockStatus != "" {
		t.Fatalf("missing truthful partial plan: %+v", plan)
	}
}

func TestOnboardRepositoryReportsInvalidExistingLock(t *testing.T) {
	repo := verifiedOnboardFixture(t)
	path := filepath.Join(repo, ".standards.lock")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	invalid := strings.Replace(string(data), "v1.2.3", "latest", 1)
	mustWriteFile(t, path, invalid)
	plan, err := OnboardRepository(context.Background(), repo, false)
	if !errors.Is(err, ErrOnboardingIncomplete) || !errors.Is(err, config.ErrLockVersionInvalid) {
		t.Fatalf("lost lock failure: %v", err)
	}
	if plan == nil || plan.LockVerified {
		t.Fatalf("missing truthful partial plan: %+v", plan)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != invalid {
		t.Fatalf("invalid caller lock was replaced: %v", err)
	}
}

// TestWriteOnboardFileLeavesRepositoryRootMode pins the confined onboarding write: a
// top-level output used to run MkdirSecure on the repository root itself, narrowing its
// mode to 0700; the root is the confinement boundary and keeps its mode, while a nested
// output directory is created with the tracked-directory mode.
func TestWriteOnboardFileLeavesRepositoryRootMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not available on Windows")
	}
	repo := t.TempDir()
	if err := os.Chmod(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := writeOnboardFile(ctx, repo, ".editorconfig", []byte("root = true\n")); err != nil {
		t.Fatalf("top-level output: %v", err)
	}
	if err := writeOnboardFile(ctx, repo, filepath.Join(".vscode", "settings.json"), []byte("{}\n")); err != nil {
		t.Fatalf("nested output: %v", err)
	}
	info, err := os.Stat(repo)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("%s mode = %v: the repository root must keep 0755", repo, info.Mode())
	}
	// BUG-839: onboarding scaffolds files the repository commits, so the nested directory and
	// both outputs carry the tracked-file modes rather than owner-only ones.
	testsupport.RequireCreatedMode(t, filepath.Join(repo, ".vscode"), 0o755)
	testsupport.RequireCreatedMode(t, filepath.Join(repo, ".vscode", "settings.json"), 0o644)
	testsupport.RequireCreatedMode(t, filepath.Join(repo, ".editorconfig"), 0o644)
	if data, err := os.ReadFile(filepath.Join(repo, ".editorconfig")); err != nil || string(data) != "root = true\n" {
		t.Errorf("top-level output = (%q, %v)", data, err)
	}
}
