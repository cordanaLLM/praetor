// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/worktree"
)

// cargoAuditMissingReason is how the security stage reports a Cargo.lock it could not audit.
const cargoAuditMissingReason = "cargo audit not run: cargo-audit is not installed (cargo install cargo-audit --locked)"

// cargoTestsPassed is the test stage's Cargo outcome when both commands pass.
const cargoTestsPassed = "cargo test --workspace --locked and cargo clippy --workspace --all-targets -- -D warnings passed"

// seedCargoWorkspace gives dir the standards lockfiles and a Cargo workspace; withLock adds its
// Cargo.lock, which is what the Cargo parts of the stages key on.
func seedCargoWorkspace(t *testing.T, dir string, withLock bool) string {
	t.Helper()
	writeLockfiles(t, dir)
	writeFile(t, filepath.Join(dir, "Cargo.toml"), "[workspace]\nmembers = []\nresolver = \"3\"\n")
	if withLock {
		writeFile(t, filepath.Join(dir, CargoLockFile), "version = 4\n")
	}
	return dir
}

// toolchainStages are the three stages that run a language's own commands, in pipeline order.
func toolchainStages() []stage {
	return []stage{{stagePrefetch, runPrefetchStage}, {stageSecurity, runSecurityStage}, {stageTests, runTestStage}}
}

// commandLines renders recorded invocations as "name args", dropping the race stage's toolchain
// probe (go env), which is not a command the stage runs against the repository.
func commandLines(recorded []recordedCommand) []string {
	lines := make([]string, 0, len(recorded))
	for i := 0; i < len(recorded); i++ {
		line := strings.TrimSpace(recorded[i].name + " " + strings.Join(recorded[i].args, " "))
		if !strings.HasPrefix(line, "go env ") {
			lines = append(lines, line)
		}
	}
	return lines
}

// lookPathWithout resolves every binary the way newTestConfig does, except missing, which are
// reported absent.
func lookPathWithout(missing ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(missing, name) {
			return "", exec.ErrNotFound
		}
		return "/usr/bin/" + name, nil
	}
}

// runToolchainStages runs the three toolchain stages through executeStage and returns each
// recorded verdict, stopping at the first failure as the pipeline does.
func runToolchainStages(t *testing.T, cfg *stageConfig) ([]StageResult, error) {
	t.Helper()
	t.Setenv(TestStageTimeoutEnv, "")
	stages := toolchainStages()
	for i := 0; i < len(stages); i++ {
		if err := executeStage(t.Context(), stages[i], cfg); err != nil {
			return cfg.rep.Stages, err
		}
	}
	return cfg.rep.Stages, nil
}

// wantStages requires each recorded stage to carry the verdict and reason given for its name.
func wantStages(t *testing.T, got []StageResult, want map[string]StageResult) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("recorded %d stages, want %d: %+v", len(got), len(want), got)
	}
	for _, s := range got {
		if w := want[s.Name]; s.Status != w.Status || s.Message != w.Message {
			t.Errorf("stage %s = %s %q, want %s %q", s.Name, s.Status, s.Message, w.Status, w.Message)
		}
	}
}

// Positive: a Cargo repository has its lockfile fetched and audited in place and its suite and
// clippy run in the isolated worktree, and each stage names cargo as what ran.
func TestToolchainStages_Positive_CargoRepositoryRunsCargo(t *testing.T) {
	t.Setenv(cargoTargetDirEnv, "")
	repo := seedCargoWorkspace(t, newHermeticGitRepo(t), true)
	cfg, recorded := newTestConfig(t, repo, false)
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("a passing Cargo toolchain failed a stage: %v", err)
	}
	wantStages(t, got, map[string]StageResult{
		stagePrefetch: {Status: StagePassed, Message: "cargo: cargo fetch --locked passed"},
		stageSecurity: {Status: StagePassed, Message: "cargo: cargo audit passed"},
		stageTests:    {Status: StagePassed, Message: "cargo: " + cargoTestsPassed},
	})
	target := "--target-dir " + persistentTargetDir(t, repo)
	want := []string{"cargo fetch --locked", "cargo audit", "cargo test " + target + " --workspace --locked",
		"cargo clippy " + target + " --workspace --all-targets -- -D warnings"}
	if lines := commandLines(*recorded); !slices.Equal(lines, want) {
		t.Fatalf("commands = %q, want %q", lines, want)
	}
	for i, c := range *recorded {
		inWorktree := strings.Contains(c.dir, "worktrees")
		if (i >= 2) != inWorktree || (i < 2 && c.dir != repo) {
			t.Errorf("%s %v ran in %s; fetch and audit belong in the repository, tests in the worktree", c.name, c.args, c.dir)
		}
	}
	if !slices.Equal(cfg.verified, []string{languageCargo}) {
		t.Errorf("verified languages = %q, want [cargo]", cfg.verified)
	}
}

// Positive: a Go repository without a Cargo.lock runs exactly the Go commands and records exactly
// the verdicts and reasons it recorded before Cargo support, so its signed output is unchanged.
func TestToolchainStages_Positive_GoPathUnchanged(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedGoModule(t, repo)
	writeLockfiles(t, repo)
	writeFile(t, filepath.Join(repo, GosecConfigFile), "{\"global\":{}}\n")
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = listingRunner(recorded, repo)
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("the Go path failed a stage: %v", err)
	}
	wantStages(t, got, map[string]StageResult{
		stagePrefetch: {Status: StagePassed}, stageSecurity: {Status: StagePassed}, stageTests: {Status: StagePassed},
	})
	want := []string{"go mod verify", "go mod download", "govulncheck -scan symbol -format json ./...", "go list -f {{.Dir}} ./...",
		"gosec -conf " + GosecConfigFile + " .", "go test -race -timeout " + EnvRunBudget(repo, false).StageBound.String() + " ./..."}
	if lines := commandLines(*recorded); !slices.Equal(lines, want) {
		t.Fatalf("commands = %q, want %q", lines, want)
	}
	if !slices.Equal(cfg.verified, []string{languageGo}) {
		t.Errorf("verified languages = %q, want [go]", cfg.verified)
	}
}

// listingRunner records like fakeRunner and answers `go list` with dir, the one package the
// gosec invocation is then confined to.
func listingRunner(recorded *[]recordedCommand, dir string) commandRunner {
	base := fakeRunner(recorded, "", nil)
	return func(ctx context.Context, cmdDir, name string, args ...string) (string, error) {
		out, err := base(ctx, cmdDir, name, args...)
		if name == "go" && len(args) > 0 && args[0] == "list" {
			return dir + "\n", nil
		}
		return out, err
	}
}

// Positive and boundary: a repository holding both languages names each one's outcome, and a
// stage that ran for Go but could not audit the Cargo.lock is skipped, never passed.
func TestToolchainStages_MixedRepositoryNamesEachLanguage(t *testing.T) {
	repo := seedCargoWorkspace(t, newHermeticGitRepo(t), true)
	seedGoModule(t, repo)
	writeFile(t, filepath.Join(repo, GosecConfigFile), "{\"global\":{}}\n")
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = listingRunner(recorded, repo)
	cfg.lookPath = lookPathWithout("cargo-audit")
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("a mixed repository failed a stage: %v", err)
	}
	wantStages(t, got, map[string]StageResult{
		stagePrefetch: {Status: StagePassed, Message: "go: passed; cargo: cargo fetch --locked passed"},
		stageSecurity: {Status: StageSkipped, Message: "go: passed; cargo: " + cargoAuditMissingReason},
		stageTests:    {Status: StagePassed, Message: "go: passed; cargo: " + cargoTestsPassed},
	})
	if !slices.Equal(cfg.verified, []string{languageGo, languageCargo}) {
		t.Errorf("verified languages = %q, want [go cargo]", cfg.verified)
	}
}

// Boundary: a missing cargo-audit reports the audit as not run with its install command, runs
// nothing, and verifies nothing.
func TestRunSecurityStage_Boundary_MissingCargoAuditIsNotAPass(t *testing.T) {
	cfg, recorded := newTestConfig(t, seedCargoWorkspace(t, t.TempDir(), true), false)
	cfg.lookPath = lookPathWithout("cargo-audit")
	_, err := runSecurityStage(t.Context(), cfg)
	if reason := wantSkip(t, err, StageSkipped); reason != "cargo: "+cargoAuditMissingReason {
		t.Errorf("reason = %q", reason)
	}
	if len(*recorded) != 0 || len(cfg.verified) != 0 {
		t.Errorf("an unaudited Cargo.lock ran %+v and verified %q", *recorded, cfg.verified)
	}
}

// Negative: an audit finding, and a clippy warning denied as an error, fail their stages and
// carry cargo's output; clippy runs only after the suite passed.
func TestToolchainStages_Negative_CargoFailuresFailTheStage(t *testing.T) {
	cases := map[string]struct {
		fail, want string
		run        func(context.Context, *stageConfig) (string, error)
	}{
		"audit finding": {fail: "audit", want: "cargo: cargo audit failed: exit status 1: RUSTSEC-0000-0000", run: runSecurityStage},
		"clippy denied": {fail: "clippy", want: "cargo: cargo clippy --workspace --all-targets -- -D warnings failed in",
			run: runTestStage},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, recorded := newTestConfig(t, seedCargoWorkspace(t, newHermeticGitRepo(t), true), false)
			base := fakeRunner(recorded, "", nil)
			cfg.run = func(ctx context.Context, dir, cmd string, args ...string) (string, error) {
				if out, err := base(ctx, dir, cmd, args...); len(args) == 0 || args[0] != tc.fail {
					return out, err
				}
				return "RUSTSEC-0000-0000", errors.New("exit status 1")
			}
			_, err := tc.run(t.Context(), cfg)
			if !isFailure(err) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a failure containing %q, got %v", tc.want, err)
			}
			last := (*recorded)[len(*recorded)-1]
			if last.name != "cargo" || len(last.args) == 0 || last.args[0] != tc.fail {
				t.Errorf("the failing command must be the last one run, got %q", commandLines(*recorded))
			}
		})
	}
}

// persistentTargetDir returns the target directory the test stage's cargo commands build into for
// repo, and requires it to be the shared one under the git common directory.
func persistentTargetDir(t *testing.T, repo string) string {
	t.Helper()
	args, note := cargoTargetDir(t.Context(), repo)
	if len(args) != 2 || args[0] != "--target-dir" || note != "" {
		t.Fatalf("cargoTargetDir(%s) = %q, %q; want a persistent --target-dir", repo, args, note)
	}
	if !strings.HasSuffix(filepath.ToSlash(args[1]), "/.git/"+cargoTargetDirRel) {
		t.Fatalf("target directory %s is not <git common dir>/%s", args[1], cargoTargetDirRel)
	}
	return args[1]
}

// Positive: the test stage's cargo commands build into one directory under the git common
// directory, which every linked worktree of the clone resolves to as well, so the isolated test
// worktree of the next run finds the dependencies already built.
func TestCargoTargetDir_Positive_SharedByEveryWorktreeOfTheClone(t *testing.T) {
	t.Setenv(cargoTargetDirEnv, "")
	repo := newHermeticGitRepo(t)
	dir := persistentTargetDir(t, repo)
	wt, err := worktree.NewManager(repo).Create(t.Context(), "linked", "HEAD")
	if err != nil {
		t.Skipf("git worktree add unavailable here: %v", err)
	}
	if got := persistentTargetDir(t, wt.Path); got != dir {
		t.Errorf("a linked worktree builds into %s, the main checkout into %s; want one directory", got, dir)
	}
	if rel, relErr := filepath.Rel(wt.Path, dir); relErr == nil && !strings.HasPrefix(rel, "..") {
		t.Errorf("target directory %s lies inside the worktree %s the stage removes", dir, wt.Path)
	}
}

// Boundary: an absolute CARGO_TARGET_DIR is the operator's persistent directory and cargo uses it
// unflagged; a relative one would resolve inside the removed worktree, so the gate's own is used.
func TestCargoTargetDir_Boundary_OperatorTargetDir(t *testing.T) {
	repo := newHermeticGitRepo(t)
	t.Setenv(cargoTargetDirEnv, t.TempDir())
	if args, note := cargoTargetDir(t.Context(), repo); args != nil || note != "" {
		t.Errorf("an absolute %s must be left to cargo, got %q %q", cargoTargetDirEnv, args, note)
	}
	t.Setenv(cargoTargetDirEnv, "target")
	persistentTargetDir(t, repo)
}

// Negative: where no git common directory resolves, the commands run without a shared directory
// and the stage reason says the run built every dependency, rather than failing the stage.
func TestCargoTargetDir_Negative_NoCheckoutBuildsCold(t *testing.T) {
	t.Setenv(cargoTargetDirEnv, "")
	args, note := cargoTargetDir(t.Context(), t.TempDir())
	if args != nil || !strings.Contains(note, "no persistent Cargo target directory") ||
		!strings.Contains(note, "compiled every dependency") {
		t.Fatalf("cargoTargetDir(no checkout) = %q, %q; want no flag and the cold-build note", args, note)
	}

	// Through the stage: a crate in a subdirectory of the checkout names the cold build.
	crate := filepath.Join(newHermeticGitRepo(t), "crate")
	if err := os.Mkdir(crate, 0o750); err != nil {
		t.Fatalf("mkdir crate: %v", err)
	}
	seedCargoWorkspace(t, crate, true)
	cfg, recorded := newTestConfig(t, crate, false)
	t.Setenv(TestStageTimeoutEnv, "")
	msg, err := runCargoTests(t.Context(), cfg)
	if err != nil || !strings.HasPrefix(msg, cargoTestsPassed+"; no persistent Cargo target directory") {
		t.Fatalf("runCargoTests = %q, %v; want the pass with the cold-build note", msg, err)
	}
	if lines := commandLines(*recorded); slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "--target-dir") }) {
		t.Errorf("a cold build passed a target directory: %q", lines)
	}
}

// Boundary: without cargo on PATH every Cargo part is reported as not run, nothing starts, and
// the receipt stage refuses a run in which no language was verified.
func TestToolchainStages_Boundary_CargoAbsentFromPath(t *testing.T) {
	repo := seedCargoWorkspace(t, t.TempDir(), true)
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.lookPath = lookPathWithout("cargo", "cargo-audit")
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("an absent cargo must be reported, not fail the run: %v", err)
	}
	absent := StageResult{Status: StageSkipped, Message: "cargo: not run: cargo is not on PATH (" + cargoInstallHint + ")"}
	wantStages(t, got, map[string]StageResult{stagePrefetch: absent, stageSecurity: absent, stageTests: absent})
	if len(*recorded) != 0 {
		t.Errorf("commands ran without cargo: %+v", *recorded)
	}
	refused(t, cfg, "Cargo.lock is present, but no Cargo stage ran here")
}

// Boundary: a Cargo.toml without its Cargo.lock leaves each stage not applicable with the reason
// it had before Cargo support, and the refusal names the missing lockfile.
func TestToolchainStages_Boundary_NoCargoLockIsNotApplicable(t *testing.T) {
	cfg, recorded := newTestConfig(t, seedCargoWorkspace(t, t.TempDir(), false), false)
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("a repository without Cargo.lock failed a stage: %v", err)
	}
	wantStages(t, got, map[string]StageResult{
		stagePrefetch: {Status: StageNotApplicable, Message: "lockfiles verified; no go.mod: module prefetch skipped"},
		stageSecurity: {Status: StageNotApplicable, Message: "no go.mod: Go security scanners skipped"},
		stageTests:    {Status: StageNotApplicable, Message: "no go.mod: Go race-detector tests skipped"},
	})
	if len(*recorded) != 0 {
		t.Errorf("commands ran without a Cargo.lock: %+v", *recorded)
	}
	refused(t, cfg, "unsupported languages at the repository root: cargo without a committed Cargo.lock (Cargo.toml)")
}

// refused runs the receipt stage over a clean tree and requires the nothing-verified refusal
// naming want, with no receipt written.
func refused(t *testing.T, cfg *stageConfig, want string) {
	t.Helper()
	cfg.rep.WorktreeClean = true
	_, err := runReceiptStage(t.Context(), cfg)
	if !errors.Is(err, ErrNothingVerified) || !strings.Contains(err.Error(), want) {
		t.Fatalf("want ErrNothingVerified naming %q, got %v", want, err)
	}
	if _, statErr := os.Stat(filepath.Join(cfg.repoDir, ReceiptFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a refused run wrote a receipt: %v", statErr)
	}
}

// Boundary: where every toolchain stage is not applicable the receipt is refused, naming the
// languages found at the root, or saying none was recognised. The refusal needs no signing key.
func TestRunReceiptStage_Boundary_NothingVerifiedIsRefused(t *testing.T) {
	t.Setenv("PRAETOR_RECEIPT_KEY", "")
	cases := map[string]struct {
		markers []string
		want    string
	}{
		"node and python": {markers: []string{"package.json", "pyproject.toml", "requirements.txt"},
			want: "unsupported languages at the repository root: node (package.json), python (pyproject.toml, requirements.txt)"},
		"no marker": {want: "no language marker the gate recognises is at the repository root"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for _, marker := range tc.markers {
				writeFile(t, filepath.Join(dir, marker), "{}\n")
			}
			cfg, _ := newTestConfig(t, dir, false)
			refused(t, cfg, tc.want)
			refused(t, cfg, "Prefetch & Lockfiles, Security & SCA Scan and Race-Detector Tests each ran nothing")
		})
	}
}

// Boundary: a dry run of a repository holding both languages starts no command at all and skips
// each Cargo part, as it skips the Go ones.
func TestExecuteStages_DryRunWithCargoLockInvokesNoCommand(t *testing.T) {
	repo := goLibraryRepo(t, nil)
	writeFile(t, filepath.Join(repo, CargoLockFile), "version = 4\n")
	cfg, recorded := newTestConfig(t, repo, true)
	if err := executeStages(t.Context(), cfg); err != nil {
		t.Fatalf("dry run rejected the repository: %v (%+v)", err, cfg.rep.Stages)
	}
	if len(*recorded) != 0 {
		t.Fatalf("a dry run invoked commands: %+v", *recorded)
	}
	for _, s := range cfg.rep.Stages {
		if s.Name == stagePrefetch && (s.Status != StageSkipped || !strings.Contains(s.Message, "cargo: dry run: cargo fetch --locked not run")) {
			t.Errorf("prefetch = %s %q", s.Status, s.Message)
		}
	}
}

// combineParts: the stage passes only when every language passed; a partial run is skipped, and
// only a stage no language applied to is not applicable.
func TestCombineParts_3D(t *testing.T) {
	pass := languagePart{language: languageGo}
	skip := languagePart{language: languageCargo, err: skipped("not run")}
	na := languagePart{language: languageGo, err: notApplicable("no go.mod")}
	cases := []struct {
		parts  []languagePart
		status StageStatus
		reason string
	}{
		{[]languagePart{pass, {language: languageCargo, msg: "cargo audit passed"}}, StagePassed, "go: passed; cargo: cargo audit passed"},
		{[]languagePart{pass, skip}, StageSkipped, "go: passed; cargo: not run"},
		{[]languagePart{na, skip}, StageSkipped, "go: no go.mod; cargo: not run"},
		{[]languagePart{na}, StageNotApplicable, "go: no go.mod"},
	}
	for i, tc := range cases {
		msg, err := combineParts(tc.parts...)
		reason := msg
		if tc.status != StagePassed {
			reason = wantSkip(t, err, tc.status)
		}
		if partStatus(err) != tc.status || reason != tc.reason {
			t.Errorf("case %d: got %s %q, want %s %q", i, partStatus(err), reason, tc.status, tc.reason)
		}
	}
}

// CargoClippyArgs is the test stage's clippy run. Positive: it denies warnings on the whole
// workspace, as the stage outcome states. Negative: a caller that edits the copy leaves the
// stage's own command unchanged. Boundary: -D warnings follows the -- separator, so a hook that
// joins the arguments passes it to clippy, not to cargo.
func TestCargoClippyArgs_3D(t *testing.T) {
	args := CargoClippyArgs()
	if got := "cargo " + strings.Join(args, " "); !strings.Contains(cargoTestsPassed, got) {
		t.Fatalf("%q is not the clippy run the stage reports", got)
	}
	args[len(args)-1] = "edited"
	if !slices.Equal(cargoTestCommands[1], CargoClippyArgs()) || slices.Contains(cargoTestCommands[1], "edited") {
		t.Fatalf("editing the returned arguments changed the stage command: %v", cargoTestCommands[1])
	}
	separator := slices.Index(CargoClippyArgs(), "--")
	if separator < 0 || !slices.Equal(CargoClippyArgs()[separator+1:], []string{"-D", "warnings"}) {
		t.Fatalf("-D warnings must follow the -- separator: %v", CargoClippyArgs())
	}
}
