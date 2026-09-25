package bump

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

func TestDryRunDoesNotCertifyOrCreateState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent-repository")
	result, err := RunCanary(t.Context(), CanaryOptions{RepoPath: root, DryRun: true,
		Candidate: UpgradeCandidate{Package: "example.org/dependency", TargetVersion: "v1.2.3", ManifestType: "go.mod"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.CanaryCertified || result.WorktreePath != "" || result.StagedPatchPath != "" {
		t.Fatalf("dry-run fabricated execution evidence: %+v", result)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("dry-run created repository state: %v", err)
	}
}

func TestCanaryOutputBoundaryCannotCertifyTruncation(t *testing.T) {
	for _, size := range []int{65536, 65537} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			dir, candidate := canaryFixture(t)
			// Prints exactly size bytes, as printf '%<size>s' x did.
			source := fmt.Sprintf("package main\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\nfunc main() { fmt.Print(strings.Repeat(\" \", %d) + \"x\") }\n", size-1)
			executable := testsupport.BuildExecutable(t, t.TempDir(), "test-command", source)
			result, err := RunCanary(t.Context(), CanaryOptions{RepoPath: dir, Candidate: candidate, TestCmd: executable})
			if size == 65536 {
				if err != nil || !result.Success || len(result.ExecutionLog) != size {
					t.Fatalf("exact output boundary rejected: %+v, %v", result, err)
				}
			} else if !errors.Is(err, ErrCanaryFailed) || result.Success || result.Status != CanaryFailed {
				t.Fatalf("overflow misreported as success: %+v, %v", result, err)
			} else if !strings.Contains(err.Error(), "command output exceeds") {
				// Any failure satisfied the lines above, including refusing the executable before
				// it ran -- which is how this case passed on Windows while the boundary went untested.
				t.Fatalf("overflow case failed for another reason: %v", err)
			}
			if result.CanaryCertified {
				t.Fatal("command output incorrectly certified")
			}
		})
	}
}

func TestCanaryPropagatesManifestUpdateFailure(t *testing.T) {
	dir, candidate := canaryFixture(t)
	candidate.ManifestType = "unsupported"
	result, err := RunCanary(t.Context(), CanaryOptions{RepoPath: dir, Candidate: candidate})
	if !errors.Is(err, ErrCanaryFailed) || result.Success || result.CanaryCertified || result.Status != CanaryFailed {
		t.Fatalf("update failure hidden: %+v, %v", result, err)
	}
}

func canaryFixture(t *testing.T) (string, UpgradeCandidate) {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Canary Fixture"}, {"config", "user.email", "fixture@example.test"}} {
		if _, err := util.RunGit(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		"package.json": `{"dependencies":{"fixture-dep":"^1.0.0"}}` + "\n",
		"README.md":    "before\n", ".gitignore": ".standards/\n.workingdir/\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := util.RunGit(t.Context(), dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(t.Context(), dir, "commit", "-q", "-s", "-m", "test: initialize canary fixture"); err != nil {
		t.Fatal(err)
	}
	return dir, UpgradeCandidate{Package: "fixture-dep", CurrentVersion: "1.0.0", TargetVersion: "2.0.0", ManifestType: "package.json"}
}

func TestCanaryCommandPassingIsNotCertification(t *testing.T) {
	dir, candidate := canaryFixture(t)
	result, err := RunCanary(t.Context(), CanaryOptions{RepoPath: dir, Candidate: candidate, TestCmd: "git status --porcelain"})
	if err != nil || !result.Success || result.Status != CanaryPassed || result.CanaryCertified {
		t.Fatalf("configured command result conflated with certification: %+v, %v", result, err)
	}
	if !strings.Contains(result.ExecutionLog, "package.json") {
		t.Fatalf("test did not observe updated worktree: %+v", result)
	}
	if _, err := os.Stat(result.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("temporary worktree not cleaned: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || !strings.Contains(string(body), "1.0.0") {
		t.Fatalf("canary changed source manifest: %s, %v", body, err)
	}
}

func TestCanaryFailureRetainsDiagnosticsNeverPatch(t *testing.T) {
	dir, candidate := canaryFixture(t)
	result, err := RunCanary(t.Context(), CanaryOptions{RepoPath: dir, Candidate: candidate, TestCmd: "git -c color.ui=always diff --exit-code"})
	if !errors.Is(err, ErrCanaryFailed) || result.Success || result.CanaryCertified || result.Status != CanaryFailed {
		t.Fatalf("failed test misreported: %+v, %v", result, err)
	}
	if result.StagedPatchPath != "" || filepath.Ext(result.DiagnosticPath) != ".sarif" {
		t.Fatalf("failure diagnostics missing or disguised as patch: %+v, %v", result, err)
	}
	if _, err := os.Stat(result.DiagnosticPath); err != nil {
		t.Fatalf("claimed diagnostic file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".standards", "patches")); !os.IsNotExist(err) {
		t.Fatalf("failure fabricated patch directory: %v", err)
	}
}

func TestCanaryCancellationNeverCreatesWorktree(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := RunCanary(ctx, CanaryOptions{RepoPath: dir})
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %+v, %v", result, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled canary wrote files: %v, %v", entries, err)
	}
}

// Positive and boundary: the default test command follows the manifest the
// candidate updates; a manifest with no known runner has no default rather
// than inheriting Go's.
func TestDefaultCanaryTestCommandFollowsTheManifest(t *testing.T) {
	for manifest, want := range map[string]string{"go.mod": "go test ./...", "package.json": "pnpm test"} {
		if got, err := defaultCanaryTestCommand(manifest); err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", manifest, got, err, want)
		}
	}
	for _, manifest := range []string{"", "Cargo.toml"} {
		if got, err := defaultCanaryTestCommand(manifest); err == nil {
			t.Errorf("manifest %q defaulted to %q", manifest, got)
		}
	}
}

// Positive: a Node candidate with no configured command is tested by pnpm. It
// used to run `go test -v ./...`, which fails in a repository without a go.mod,
// so every Node candidate in a bump train was reported as breakage.
func TestCanaryDefaultsNodeCandidatesToThePackageManager(t *testing.T) {
	dir, candidate := canaryFixture(t)
	bin := t.TempDir()
	testsupport.BuildExecutable(t, bin, "pnpm", "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\t\"strings\"\n)\n\nfunc main() { fmt.Print(\"fake-pnpm \" + strings.Join(os.Args[1:], \" \")) }\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := RunCanary(t.Context(), CanaryOptions{RepoPath: dir, Candidate: candidate})
	if err != nil || result.Status != CanaryPassed {
		t.Fatalf("node candidate not tested by pnpm: %+v, %v", result, err)
	}
	if !strings.Contains(result.ExecutionLog, "fake-pnpm test") {
		t.Fatalf("default command was not `pnpm test`: %q", result.ExecutionLog)
	}
}

// Negative: a test command killed by the caller's deadline or cancellation is
// reported as cancelled, not as candidate breakage, and no breakage
// diagnostics are written for it.
func TestCanaryInterruptedTestIsCancelledNotBreakage(t *testing.T) {
	dir, candidate := canaryFixture(t)
	sleeper := testsupport.BuildExecutable(t, t.TempDir(), "sleeper", "package main\n\nimport (\n\t\"os\"\n\t\"time\"\n)\n\nfunc main() {\n\tif os.WriteFile(os.Args[1], []byte(\"started\"), 0o600) != nil {\n\t\tos.Exit(2)\n\t}\n\ttime.Sleep(time.Minute)\n}\n")
	marker := filepath.Join(t.TempDir(), "started")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		defer cancel()
		for range 1200 {
			if _, err := os.Stat(marker); err == nil || ctx.Err() != nil {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	}()

	result, err := RunCanary(ctx, CanaryOptions{RepoPath: dir, Candidate: candidate, TestCmd: sleeper + " " + marker})
	if !errors.Is(err, ErrCanaryCancelled) || !errors.Is(err, context.Canceled) || errors.Is(err, ErrCanaryFailed) {
		t.Fatalf("interrupted canary misreported: %v", err)
	}
	if result.Status != CanaryCancelled || result.Success || result.DistilledErrors != "" || result.DiagnosticPath != "" {
		t.Fatalf("interrupted canary blamed on the candidate: %+v", result)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".workingdir", "evidence", "canary")); !os.IsNotExist(statErr) {
		t.Fatalf("breakage diagnostics written for an interrupted canary: %v", statErr)
	}
	if _, statErr := os.Stat(result.WorktreePath); !os.IsNotExist(statErr) {
		t.Fatalf("interrupted canary left its worktree: %v", statErr)
	}
}

// Boundary: only the caller's context and a deadline mark a step interrupted.
// Output overflow cancels the runner's own context, which surfaces as
// context.Canceled under a live caller context; that stays a failure.
func TestInterruptedDistinguishesDeadlinesFromOverflow(t *testing.T) {
	live := t.Context()
	if !interrupted(live, fmt.Errorf("run: %w", context.DeadlineExceeded)) {
		t.Error("runner fallback deadline not treated as an interruption")
	}
	if interrupted(live, errors.Join(errors.New("command output exceeds 65536 bytes per stream"), context.Canceled)) {
		t.Error("output overflow treated as an interruption")
	}
	done, cancel := context.WithCancel(t.Context())
	cancel()
	if !interrupted(done, errors.New("signal: killed")) {
		t.Error("caller cancellation not treated as an interruption")
	}
}

// Negative: a worktree the canary cannot remove is returned as an error even
// when the test passed; it used to be only a line in the execution log, so the
// caller saw success while a worktree and a branch leaked.
func TestCanaryReportsWorktreeCleanupFailure(t *testing.T) {
	dir, candidate := canaryFixture(t)
	// A locked worktree survives `git worktree remove --force`, so the test
	// command locks its own worktree to make cleanup fail.
	result, err := RunCanary(t.Context(), CanaryOptions{RepoPath: dir, Candidate: candidate, TestCmd: "git worktree lock ."})
	if result == nil || !result.Success || result.Status != CanaryPassed {
		t.Fatalf("test outcome lost: %+v, %v", result, err)
	}
	if err == nil || !strings.Contains(err.Error(), "remove canary worktree") || errors.Is(err, ErrCanaryFailed) {
		t.Fatalf("cleanup failure not returned: %v", err)
	}
	if _, statErr := os.Stat(result.WorktreePath); statErr != nil {
		t.Fatalf("fixture did not leak the worktree it reports: %v", statErr)
	}
}

// Boundary: task IDs stay unique across many canaries for one package, and a
// package name at the sanitizer's length cap still yields an ID the worktree
// manager accepts.
func TestCanaryTaskIDsAreUniqueAndValid(t *testing.T) {
	valid := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	seen := make(map[string]bool, 1000)
	pkg := "github.com/" + strings.Repeat("x", 80)
	for range 1000 {
		id := canaryTaskID(pkg)
		if seen[id] {
			t.Fatalf("task ID repeated: %s", id)
		}
		seen[id] = true
		if len(id) > worktree.MaxTaskIDLength || !valid.MatchString(id) {
			t.Fatalf("task ID rejected by the worktree manager's rule: %q", id)
		}
	}
}
