package gating

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/lockdown"
)

// recordedCommand captures one invocation made through the injected command runner.
type recordedCommand struct {
	dir  string
	name string
	args []string
}

// fakeRunner returns a command runner that records invocations and replays outcomes.
func fakeRunner(recorded *[]recordedCommand, out string, err error) commandRunner {
	return func(_ context.Context, dir, name string, args ...string) (string, error) {
		*recorded = append(*recorded, recordedCommand{dir: dir, name: name, args: args})
		// The race stage asks the toolchain whether the detector can build before it
		// runs anything. These cases are about the stage, not about the host they run
		// on, so the probe is answered as a capable toolchain would answer it; the
		// probe's own behaviour is covered in race_availability_test.go. Without this
		// the stage would skip and every assertion below would pass vacuously.
		if name == "go" && len(args) == 2 && args[0] == "env" {
			switch args[1] {
			case "CGO_ENABLED":
				return "1\n", nil
			case "CC":
				return "gcc\n", nil
			}
		}
		return out, err
	}
}

// testInvocations returns only the race-detector runs, filtering out the toolchain
// capability probe the stage performs first. The probe is part of the stage, not a
// test run, and counting it would make these cases assert the wrong thing.
func testInvocations(recorded []recordedCommand) []recordedCommand {
	runs := make([]recordedCommand, 0, len(recorded))
	for i := 0; i < len(recorded); i++ {
		if len(recorded[i].args) > 0 && recorded[i].args[0] == "test" {
			runs = append(runs, recorded[i])
		}
	}
	return runs
}

// newTestConfig builds a stage configuration whose seams never touch the real toolchain.
func newTestConfig(t *testing.T, repoDir string, dryRun bool) (*stageConfig, *[]recordedCommand) {
	t.Helper()
	recorded := &[]recordedCommand{}
	rep := &PipelineReport{Stages: make([]StageResult, 0, maxStages)}
	return &stageConfig{
		repoDir:     repoDir,
		dryRun:      dryRun,
		run:         fakeRunner(recorded, "", nil),
		lookPath:    func(name string) (string, error) { return "/usr/bin/" + name, nil },
		rep:         rep,
		boundStage:  withStageBound,
		inspectTree: cleanTree(rep),
	}, recorded
}

// cleanTree answers every tree inspection as a clean tree at rep's recorded commit, so a stage
// fixture need not be a repository. The real inspection is covered in tree_test.go.
func cleanTree(rep *PipelineReport) func(context.Context, string) treeState {
	return func(context.Context, string) treeState { return treeState{commit: rep.CommitSHA} }
}

// newGoModuleDir writes a minimal dependency-free Go module.
func newGoModuleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return dir
}

// newHermeticGitRepo creates an isolated git repository with a single commit, using a
// sandboxed git configuration so no developer configuration is read or written.
func newHermeticGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, "no-such-gitconfig"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, "no-such-gitconfig"),
		"GIT_AUTHOR_NAME=praetor-test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=praetor-test", "GIT_COMMITTER_EMAIL=test@example.invalid",
	)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "README.md"},
		{"commit", "-q", "-m", "fixture"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Skipf("git %v failed in sandbox: %v (%s)", args, err, string(out))
		}
	}
	return dir
}

func TestPrefetchDependencies_Positive_And_Negative(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	goDir := newGoModuleDir(t)

	rep, err := PrefetchDependencies(ctx, goDir)
	if err != nil {
		t.Fatalf("expected prefetch to succeed on a dependency-free module, got: %v", err)
	}
	if !rep.VerifiedDependencies || rep.Skipped {
		t.Errorf("expected VerifiedDependencies=true and Skipped=false, got %+v", rep)
	}

	// Negative: Cancelled Context
	cancCtx, cancelNow := context.WithCancel(ctx)
	cancelNow()
	if _, err := PrefetchDependencies(cancCtx, goDir); err == nil {
		t.Error("expected error for cancelled context, got nil")
	}

	// Boundary: Nil Context
	if _, err := PrefetchDependencies(nil, goDir); err == nil { //nolint:staticcheck // exercising the documented nil-context contract
		t.Error("expected error for nil context, got nil")
	}
}

func TestPrefetchDependencies_Boundary_NonGoRepository(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A Rust or Python repository adopted by praetor has no go.mod. The stage must skip,
	// not reject the repository.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname=\"x\"\n"), 0o600); err != nil {
		t.Fatalf("seed Cargo.toml: %v", err)
	}

	rep, err := PrefetchDependencies(ctx, dir)
	if err != nil {
		t.Fatalf("expected a non-Go repository to be skipped, got: %v", err)
	}
	if !rep.Skipped || rep.VerifiedDependencies {
		t.Errorf("expected Skipped=true and VerifiedDependencies=false, got %+v", rep)
	}
}

func TestVerifyLockfiles_3D(t *testing.T) {
	// Positive: both files present and non-empty.
	ok := t.TempDir()
	writeFile(t, filepath.Join(ok, ".standards.yaml"), "version: 1\n")
	writeFile(t, filepath.Join(ok, ".standards.lock"), "version: 1\n")
	if err := VerifyLockfiles(ok); err != nil {
		t.Fatalf("expected lockfiles to verify, got: %v", err)
	}

	// Negative: nothing at all, then manifest only.
	tmpDir := t.TempDir()
	if err := VerifyLockfiles(tmpDir); err == nil {
		t.Errorf("expected error for missing lockfiles in empty dir, got nil")
	}
	writeFile(t, filepath.Join(tmpDir, ".standards.yaml"), "version: 1\n")
	if err := VerifyLockfiles(tmpDir); err == nil {
		t.Errorf("expected error when .standards.lock is missing, got nil")
	}

	// Boundary: zero-byte files exist but carry no content.
	empty := t.TempDir()
	writeFile(t, filepath.Join(empty, ".standards.yaml"), "")
	writeFile(t, filepath.Join(empty, ".standards.lock"), "")
	err := VerifyLockfiles(empty)
	if !errors.Is(err, ErrLockfileEmpty) {
		t.Errorf("expected ErrLockfileEmpty for a zero-byte manifest, got %v", err)
	}

	// Boundary: a repository path containing glob metacharacters must be stat'ed
	// literally, never matched as a pattern.
	globRoot := filepath.Join(t.TempDir(), "proj[v2]")
	if err := os.MkdirAll(globRoot, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(globRoot, ".standards.yaml"), "version: 1\n")
	writeFile(t, filepath.Join(globRoot, ".standards.lock"), "version: 1\n")
	if err := VerifyLockfiles(globRoot); err != nil {
		t.Errorf("expected a path with glob metacharacters to verify, got: %v", err)
	}

	// Boundary: a directory in place of the manifest is not a valid manifest.
	dirAsFile := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dirAsFile, ".standards.yaml"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := VerifyLockfiles(dirAsFile); err == nil {
		t.Error("expected an error when the manifest path is a directory")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// wantSkip asserts that a stage reported a skip with the given verdict and returns its reason.
func wantSkip(t *testing.T, err error, want StageStatus) string {
	t.Helper()
	skip, ok := errors.AsType[*stageSkip](err)
	if !ok {
		t.Fatalf("expected a %s verdict, got error %v", want, err)
	}
	if skip.status != want {
		t.Fatalf("expected a %s verdict, got %s (%s)", want, skip.status, skip.reason)
	}
	return skip.reason
}

func TestExecuteStage_3D(t *testing.T) {
	cfg, _ := newTestConfig(t, t.TempDir(), false)
	ctx := context.Background()

	// Positive: a passing stage records its message.
	pass := stage{"Pass", func(context.Context, *stageConfig) (string, error) { return "0 infractions", nil }}
	if err := executeStage(ctx, pass, cfg); err != nil {
		t.Fatalf("passing stage returned %v", err)
	}
	if got := cfg.rep.Stages[0]; got.Status != StagePassed || got.Message != "0 infractions" {
		t.Errorf("unexpected stage result: %+v", got)
	}

	// Negative: a failing stage records the error and propagates it.
	boom := errors.New("boom")
	fail := stage{"Fail", func(context.Context, *stageConfig) (string, error) { return "", boom }}
	if err := executeStage(ctx, fail, cfg); !errors.Is(err, boom) {
		t.Fatalf("expected the stage error to propagate, got %v", err)
	}
	if got := cfg.rep.Stages[1]; got.Status != StageFailed || !got.Failed() || got.Message != "boom" {
		t.Errorf("unexpected failing stage result: %+v", got)
	}

	// Boundary: an empty message on success leaves Message empty.
	quiet := stage{"Quiet", func(context.Context, *stageConfig) (string, error) { return "", nil }}
	if err := executeStage(ctx, quiet, cfg); err != nil {
		t.Fatalf("quiet stage returned %v", err)
	}
	if got := cfg.rep.Stages[2]; got.Status != StagePassed || got.Message != "" {
		t.Errorf("unexpected quiet stage result: %+v", got)
	}
}

// A stage that ran nothing is recorded as skipped or not applicable, never as passed, and it
// does not stop the pipeline. Before StageStatus both of these recorded Passed=true.
func TestExecuteStage_SkipVerdicts(t *testing.T) {
	cfg, _ := newTestConfig(t, t.TempDir(), false)
	ctx := context.Background()

	cases := []struct {
		fn     func(context.Context, *stageConfig) (string, error)
		status StageStatus
		reason string
	}{
		{func(context.Context, *stageConfig) (string, error) { return "", skipped("dry run: nothing ran") }, StageSkipped, "dry run: nothing ran"},
		{func(context.Context, *stageConfig) (string, error) { return "", notApplicable("no go.mod") }, StageNotApplicable, "no go.mod"},
	}
	for i, tc := range cases {
		if err := executeStage(ctx, stage{"Skip", tc.fn}, cfg); err != nil {
			t.Fatalf("case %d: a skip must not stop the pipeline, got %v", i, err)
		}
		got := cfg.rep.Stages[i]
		if got.Status != tc.status || got.Message != tc.reason || got.Failed() {
			t.Errorf("case %d: got %+v, want status %s and reason %q", i, got, tc.status, tc.reason)
		}
	}

	// Boundary: the verdict reaches the JSON report as a status string, and no passed field
	// survives to be read as a pass.
	raw, err := json.Marshal(cfg.rep.Stages[0])
	if err != nil {
		t.Fatalf("marshal stage: %v", err)
	}
	if !strings.Contains(string(raw), `"status":"skipped"`) || strings.Contains(string(raw), `"passed"`) {
		t.Errorf("stage JSON must carry the status and no passed bool: %s", raw)
	}
}

func TestRunSecurityStage_3D(t *testing.T) {
	ctx := context.Background()

	// Positive: both scanners present, gosec invoked with the pinned configuration and
	// no exclusions.
	goDir := newGoModuleDir(t)
	writeFile(t, filepath.Join(goDir, GosecConfigFile), "{\"global\":{}}\n")
	cfg, recorded := newTestConfig(t, goDir, false)
	cfg.run = func(ctx context.Context, dir, name string, args ...string) (string, error) {
		*recorded = append(*recorded, recordedCommand{dir: dir, name: name, args: args})
		if name == "go" {
			return goDir + "\n", nil
		}
		return "", nil
	}
	if _, err := runSecurityStage(ctx, cfg); err != nil {
		t.Fatalf("security stage failed: %v", err)
	}
	if len(*recorded) != 3 {
		t.Fatalf("expected package listing, govulncheck and gosec to run, recorded %+v", *recorded)
	}
	gosecArgs := strings.Join((*recorded)[2].args, " ")
	if (*recorded)[2].name != "gosec" || gosecArgs != "-conf "+GosecConfigFile+" ." {
		t.Errorf("unexpected gosec invocation: %s %s", (*recorded)[2].name, gosecArgs)
	}
	if strings.Contains(gosecArgs, "-exclude") {
		t.Errorf("gosec must run with zero exclusions, got: %s", gosecArgs)
	}

	// Negative: a missing scanner fails the stage instead of passing with nothing run.
	missing, _ := newTestConfig(t, goDir, false)
	missing.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if _, err := runSecurityStage(ctx, missing); !errors.Is(err, ErrMissingScanner) {
		t.Errorf("expected ErrMissingScanner, got %v", err)
	}

	// Negative: a scanner that reports findings fails the stage.
	failing, _ := newTestConfig(t, goDir, false)
	failRecorded := &[]recordedCommand{}
	failing.run = fakeRunner(failRecorded, "G304 findings", errors.New("exit status 1"))
	if _, err := runSecurityStage(ctx, failing); err == nil {
		t.Error("expected the stage to fail when a scanner reports findings")
	}

	// Negative: the pinned gosec configuration is mandatory.
	noConf := newGoModuleDir(t)
	noConfCfg, _ := newTestConfig(t, noConf, false)
	if _, err := runSecurityStage(ctx, noConfCfg); err == nil || !strings.Contains(err.Error(), GosecConfigFile) {
		t.Errorf("expected a missing-%s error, got %v", GosecConfigFile, err)
	}

	// Boundary: a repository without go.mod reports the Go scanners not applicable.
	nonGo, nonGoRecorded := newTestConfig(t, t.TempDir(), false)
	_, err := runSecurityStage(ctx, nonGo)
	if reason := wantSkip(t, err, StageNotApplicable); !strings.Contains(reason, "no go.mod") || len(*nonGoRecorded) != 0 {
		t.Errorf("expected a no-go.mod reason and no commands, got %q / %+v", reason, *nonGoRecorded)
	}

	// Boundary: a dry run lists no packages and starts neither scanner, even where both are
	// installed and the configuration is present.
	dry, dryRecorded := newTestConfig(t, goDir, true)
	_, err = runSecurityStage(ctx, dry)
	if reason := wantSkip(t, err, StageSkipped); !strings.Contains(reason, "dry run") || len(*dryRecorded) != 0 {
		t.Errorf("a dry run must run no scanner, got %q / %+v", reason, *dryRecorded)
	}
}

// writeLockfiles gives dir the non-empty manifest and lockfile the prefetch stage requires.
func writeLockfiles(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, ".standards.yaml"), "version: 1\n")
	writeFile(t, filepath.Join(dir, ".standards.lock"), "version: 1\n")
}

// The prefetch stage runs go mod verify and go mod download through the stage runner, and a
// dry run runs neither: download writes the module cache and can reach the network.
func TestRunPrefetchStage_3D(t *testing.T) {
	ctx := context.Background()
	goDir := newGoModuleDir(t)
	writeLockfiles(t, goDir)

	// Positive: a real run verifies and downloads, in that order, in the repository.
	cfg, recorded := newTestConfig(t, goDir, false)
	if _, err := runPrefetchStage(ctx, cfg); err != nil {
		t.Fatalf("prefetch stage failed: %v", err)
	}
	var got []string
	for _, c := range *recorded {
		got = append(got, c.name+" "+strings.Join(c.args, " "))
	}
	if strings.Join(got, "; ") != "go mod verify; go mod download" {
		t.Errorf("unexpected prefetch commands: %v", got)
	}

	// Negative: a dry run verifies the lockfiles and runs no go command.
	dry, dryRecorded := newTestConfig(t, goDir, true)
	_, err := runPrefetchStage(ctx, dry)
	if reason := wantSkip(t, err, StageSkipped); !strings.Contains(reason, "lockfiles verified") || len(*dryRecorded) != 0 {
		t.Errorf("a dry run must run no go command, got %q / %+v", reason, *dryRecorded)
	}

	// Negative: a dry run still fails on a missing lockfile; the read-only check still runs.
	noLock, _ := newTestConfig(t, newGoModuleDir(t), true)
	if _, err := runPrefetchStage(ctx, noLock); err == nil || errors.As(err, new(*stageSkip)) {
		t.Errorf("a dry run must still reject missing lockfiles, got %v", err)
	}

	// Boundary: without a go.mod the stage is not applicable, dry run or not.
	nonGoDir := t.TempDir()
	writeLockfiles(t, nonGoDir)
	for _, dryRun := range []bool{false, true} {
		nonGo, nonGoRecorded := newTestConfig(t, nonGoDir, dryRun)
		_, err := runPrefetchStage(ctx, nonGo)
		if reason := wantSkip(t, err, StageNotApplicable); !strings.Contains(reason, "no go.mod") || len(*nonGoRecorded) != 0 {
			t.Errorf("dry run %v: got %q / %+v", dryRun, reason, *nonGoRecorded)
		}
	}
}

// A whole dry run changes nothing: it records the mutating and network stages as skipped,
// runs the read-only ones for real, and invokes no command at all.
func TestExecuteStages_DryRunInvokesNoCommand(t *testing.T) {
	repo := goLibraryRepo(t, nil)
	cfg, recorded := newTestConfig(t, repo, true)

	if err := executeStages(context.Background(), cfg); err != nil {
		t.Fatalf("dry run rejected a conforming repository: %v (%+v)", err, cfg.rep.Stages)
	}
	if len(*recorded) != 0 {
		t.Fatalf("a dry run invoked commands: %+v", *recorded)
	}
	want := map[string]StageStatus{
		"Prefetch & Lockfiles":   StageSkipped,
		"HISS Invariant Scan":    StagePassed,
		"Security & SCA Scan":    StageSkipped,
		"Flavor Conformance":     StagePassed,
		"Race-Detector Tests":    StageSkipped,
		"Ed25519 Exit-0 Receipt": StageSkipped,
	}
	if len(cfg.rep.Stages) != len(want) {
		t.Fatalf("expected %d stages, got %+v", len(want), cfg.rep.Stages)
	}
	for _, s := range cfg.rep.Stages {
		if s.Status != want[s.Name] {
			t.Errorf("stage %s: status %s, want %s", s.Name, s.Status, want[s.Name])
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ReceiptFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a dry run wrote a receipt: %v", err)
	}
}

func TestRunHissStage_3D(t *testing.T) {
	ctx := context.Background()

	// A fixture with one legacy infraction the scanner reports.
	repoDir := t.TempDir()
	writeFile(t, filepath.Join(repoDir, "legacy.go"),
		"package legacy\n\nfunc boom() {\n\tpanic(\"legacy debt\")\n}\n")

	scan, err := hiss.Scan(ctx, repoDir, hiss.ScanOptions{Cap: 1000, MaxFuncLOC: 60})
	if err != nil {
		t.Fatalf("hiss scan: %v", err)
	}
	if scan.TotalInfractions == 0 {
		t.Skip("fixture produced no infractions; scanner rules changed")
	}

	// Negative: no baseline recorded, so the legacy debt is a new violation, and the rejection
	// names it as [rule] file:line rather than only counting it.
	cfg, _ := newTestConfig(t, repoDir, false)
	_, err = runHissStage(ctx, cfg)
	if err == nil {
		t.Fatal("expected unbaselined infractions to fail the stage")
	}
	first := scan.Violations[0]
	named := fmt.Sprintf("[%s] %s:%d", first.RuleID, first.FilePath, first.LineNumber)
	if !strings.Contains(err.Error(), named) || !strings.Contains(err.Error(), "(new)") {
		t.Errorf("the rejection must name %q, got %q", named, err)
	}

	// Positive: with the debt recorded in .standards-baseline.json the ratchet passes,
	// which is what lets an adopted brownfield repository push at all.
	infractions := hiss.ConvertToBaseline(scan.Violations)
	for i := range infractions {
		infractions[i].Fingerprint = fmt.Sprintf("%s:%d:%s",
			infractions[i].FilePath, infractions[i].LineNumber, infractions[i].RuleID)
	}
	base := &baseline.Baseline{Version: 1, Infractions: infractions}
	if err := baseline.SaveBaseline(filepath.Join(repoDir, ".standards-baseline.json"), base); err != nil {
		t.Fatalf("save baseline: %v", err)
	}
	msg, err := runHissStage(ctx, cfg)
	if err != nil {
		t.Fatalf("expected baselined debt to pass the ratchet, got %v", err)
	}
	if !strings.Contains(msg, "baselined limit") {
		t.Errorf("expected a ratchet summary message, got %q", msg)
	}

	// Boundary: a clean repository with an empty baseline passes.
	clean, _ := newTestConfig(t, t.TempDir(), false)
	if _, err := runHissStage(ctx, clean); err != nil {
		t.Errorf("expected a clean tree to pass, got %v", err)
	}
}

func TestRunTestStage_3D(t *testing.T) {
	ctx := context.Background()

	// Boundary: a dry run executes nothing and says so.
	dry, dryRecorded := newTestConfig(t, t.TempDir(), true)
	_, err := dry.runTestStageForTest(ctx)
	if reason := wantSkip(t, err, StageSkipped); !strings.Contains(reason, "dry run") || len(*dryRecorded) != 0 {
		t.Errorf("dry run executed work: %q / %+v", reason, *dryRecorded)
	}

	// Negative: a directory that is not a git repository cannot yield an isolated
	// worktree, and the stage must fail rather than test the live tree. It carries a
	// go.mod so the stage reaches worktree creation instead of skipping as non-Go.
	notGitDir := t.TempDir()
	seedGoModule(t, notGitDir)
	notGit, notGitRecorded := newTestConfig(t, notGitDir, false)
	if _, err := notGit.runTestStageForTest(ctx); err == nil {
		t.Error("expected worktree creation failure to fail the stage")
	}
	if runs := testInvocations(*notGitRecorded); len(runs) != 0 {
		t.Errorf("no test command may run without an isolated worktree, got %+v", runs)
	}

	// Positive: the race detector runs inside the worktree, not in the repository.
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, recorded := newTestConfig(t, repoDir, false)
	if _, err := runTestStage(ctx, cfg); err != nil {
		t.Fatalf("test stage failed: %v", err)
	}
	runs := testInvocations(*recorded)
	if len(runs) != 1 {
		t.Fatalf("expected exactly one test invocation, got %+v", runs)
	}
	invocation := runs[0]
	wantArgs := "test -race -timeout " + EnvRunBudget(repoDir).StageBound.String() + " ./..."
	if invocation.name != "go" || strings.Join(invocation.args, " ") != wantArgs {
		t.Errorf("expected 'go %s', got %s %v", wantArgs, invocation.name, invocation.args)
	}
	if invocation.dir == repoDir || !strings.Contains(invocation.dir, "worktrees") {
		t.Errorf("tests must run in the isolated worktree, ran in %s", invocation.dir)
	}

	// Negative: a failing test run fails the stage and names the worktree.
	failDir := newHermeticGitRepo(t)
	seedGoModule(t, failDir)
	failing, _ := newTestConfig(t, failDir, false)
	failRecorded := &[]recordedCommand{}
	failing.run = fakeRunner(failRecorded, "FAIL example.test", errors.New("exit status 1"))
	if _, err := runTestStage(ctx, failing); err == nil {
		t.Error("expected a failing test run to fail the stage")
	}
}

// runTestStageForTest is a readability shim so the table above reads as stage calls.
func (c *stageConfig) runTestStageForTest(ctx context.Context) (string, error) {
	return runTestStage(ctx, c)
}

func TestRunReceiptStage_Boundary_DryRunMintsNothing(t *testing.T) {
	repoDir := t.TempDir()
	cfg, _ := newTestConfig(t, repoDir, true)
	cfg.rep.DryRun = true

	_, err := runReceiptStage(context.Background(), cfg)
	if reason := wantSkip(t, err, StageSkipped); !strings.Contains(reason, "no Exit-0 receipt") {
		t.Errorf("expected an explicit dry-run note, got %q", reason)
	}
	if cfg.rep.ReceiptSignature != "" || cfg.rep.ReceiptPath != "" {
		t.Errorf("dry run produced receipt metadata: %+v", cfg.rep)
	}
	if _, err := os.Stat(filepath.Join(repoDir, ReceiptFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dry run wrote a receipt file: %v", err)
	}
}

func TestRunReceiptStage_Positive_SignsRealStageOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// APPDATA and LOCALAPPDATA are set alongside the POSIX pair because
	// os.UserConfigDir reads APPDATA on Windows. Without them this sandbox held on
	// POSIX only, and a keygen case wrote to the real per-user key file -- silently
	// destroying a developer's signing key on every test run (HISS-21).
	t.Setenv("APPDATA", home)
	t.Setenv("LOCALAPPDATA", home)

	pub, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	t.Setenv(lockdown.SigningKeyEnv, hex.EncodeToString(priv.Seed()))

	repoDir := t.TempDir()
	cfg, _ := newTestConfig(t, repoDir, false)
	cfg.rep.Repository = "acme/widget"
	cfg.rep.CommitSHA = "0123456789abcdef0123456789abcdef01234567"
	cfg.rep.WorktreeClean = true
	cfg.rep.Stages = append(cfg.rep.Stages,
		StageResult{Name: "Prefetch & Lockfiles", Status: StagePassed},
		StageResult{Name: "HISS Invariant Scan", Status: StagePassed, Message: "0 infractions within the 0 baselined limit"},
	)
	// The Go prefetch recorded above ran, which is what lets the receipt stage sign at all.
	cfg.verified = []string{languageGo}

	msg, err := runReceiptStage(context.Background(), cfg)
	if err != nil {
		t.Fatalf("receipt stage failed: %v", err)
	}
	if !strings.Contains(msg, ReceiptFileName) {
		t.Errorf("expected the receipt path in the stage message, got %q", msg)
	}

	rf, err := lockdown.LoadReceiptFile(filepath.Join(repoDir, ReceiptFileName))
	if err != nil {
		t.Fatalf("LoadReceiptFile: %v", err)
	}
	// The receipt must verify against the pinned key and the real concatenated output,
	// which is what makes it evidence rather than decoration, and it must pass the gate
	// output version check `gate verify` and `forge validate-pr` apply.
	if err := lockdown.VerifyPinnedReceiptFile(rf, pub); err != nil {
		t.Fatalf("written receipt does not verify: %v", err)
	}
	if rf.GateOutput != string(cfg.rep.StageOutput()) {
		t.Errorf("receipt gate output does not match the report:\n%q", rf.GateOutput)
	}
	if !strings.Contains(rf.GateOutput, "HISS Invariant Scan") {
		t.Errorf("stage results are missing from the signed output:\n%s", rf.GateOutput)
	}
	if rf.Repository != "acme/widget" || rf.CommitSHA != cfg.rep.CommitSHA {
		t.Errorf("receipt identity = %s@%s, want acme/widget@%s", rf.Repository, rf.CommitSHA, cfg.rep.CommitSHA)
	}

	// Any later edit to the recorded stage results invalidates the signature, including
	// relabelling a stage that passed as one that was skipped.
	cfg.rep.Stages[0].Status = StageSkipped
	if err := lockdown.VerifyPinnedReceipt(&rf.ExecutionReceipt, pub, cfg.rep.StageOutput()); err == nil {
		t.Error("expected a mutated stage list to invalidate the receipt")
	}
}

func TestRunReceiptStage_Negative_NoSigningKey(t *testing.T) {
	// Sandbox the key lookup so the developer's own key is never used. XDG_CONFIG_HOME
	// and HOME cover POSIX only: os.UserConfigDir reads APPDATA on Windows, so without
	// it this case reached the real per-user key and passed for the wrong reason --
	// the key was refused by the mode check rather than absent. Once that check stopped
	// refusing every Windows key, the omission surfaced as a failure here (HISS-21).
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", home)
	t.Setenv("LOCALAPPDATA", home)
	t.Setenv("PRAETOR_RECEIPT_KEY", "")

	repoDir := t.TempDir()
	cfg, _ := newTestConfig(t, repoDir, false)
	// A clean tree and a language that was verified, so the refusal below is the missing key's
	// and neither the tree's nor the empty verification's.
	cfg.rep.WorktreeClean = true
	cfg.verified = []string{languageGo}
	_, err := runReceiptStage(context.Background(), cfg)
	if err == nil || errors.Is(err, ErrUncleanTree) || errors.Is(err, ErrNothingVerified) {
		t.Fatalf("expected the receipt stage to fail closed on the missing signing key, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, ReceiptFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a keyless run wrote a receipt: %v", err)
	}
}

func TestStageOutput_3D(t *testing.T) {
	rep := &PipelineReport{
		Repository:    "acme/widget",
		CommitSHA:     "deadbeef",
		WorktreeClean: true,
		Stages: []StageResult{
			{Name: "Prefetch & Lockfiles", Status: StagePassed},
			{Name: "HISS Invariant Scan", Status: StagePassed, Message: "0 infractions\nwithin limit"},
		},
	}

	// Positive: deterministic and complete.
	first := string(rep.StageOutput())
	if first != string(rep.StageOutput()) {
		t.Error("StageOutput is not deterministic")
	}
	for _, want := range []string{"repository\tacme/widget", "commit_sha\tdeadbeef", "worktree_clean\ttrue", "dry_run\tfalse"} {
		if !strings.Contains(first, want) {
			t.Errorf("stage output missing %q:\n%s", want, first)
		}
	}
	// Multi-line stage messages are flattened so the signed payload stays line-oriented.
	if strings.Contains(first, "0 infractions\nwithin limit") {
		t.Errorf("stage messages must be flattened:\n%s", first)
	}

	if !strings.Contains(first, "stage\tPrefetch & Lockfiles\tpassed\t") {
		t.Errorf("stage lines must carry the verdict:\n%s", first)
	}

	// Negative: a stage that did not run changes the signed payload and says so. With a
	// passed bool, a skipped stage and a passed one rendered the same line.
	rep.Stages[0].Status = StageSkipped
	skippedOutput := string(rep.StageOutput())
	if skippedOutput == first || !strings.Contains(skippedOutput, "stage\tPrefetch & Lockfiles\tskipped\t") {
		t.Errorf("a skipped stage must be signed as skipped:\n%s", skippedOutput)
	}
	rep.Stages[0].Status = StageNotApplicable
	if string(rep.StageOutput()) == skippedOutput {
		t.Error("not applicable and skipped must sign differently")
	}

	// Boundary: an empty report still renders a complete header, under the v2 version.
	empty := (&PipelineReport{}).StageOutput()
	if !strings.HasPrefix(string(empty), lockdown.GateOutputVersion+"\n") || lockdown.GateOutputVersion != "praetor-gate-output/v2" {
		t.Errorf("unexpected empty-report output: %q", string(empty))
	}
}

func TestRunGatedPipeline_Negative_And_Boundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Negative: a directory without lockfiles is rejected at the first stage and no
	// later stage is attempted.
	tmpDir := t.TempDir()
	negRep, err := RunGatedPipeline(ctx, tmpDir, true)
	if err != nil {
		t.Fatalf("expected pipeline to return a report, not err: %v", err)
	}
	if negRep.Status != StatusRejected {
		t.Errorf("expected StatusRejected for empty dir, got %s", negRep.Status)
	}
	if len(negRep.Stages) != 1 || !negRep.Stages[0].Failed() {
		t.Errorf("expected rejection at stage 1, got %+v", negRep.Stages)
	}
	if negRep.ReceiptSignature != "" {
		t.Error("a rejected pipeline must not carry a receipt signature")
	}
	if negRep.Repository == "." || negRep.Repository == ".." {
		t.Errorf("repository identity must not be a bare path element, got %q", negRep.Repository)
	}

	// Boundary: Nil Context
	if _, nilErr := RunGatedPipeline(nil, tmpDir, true); nilErr == nil { //nolint:staticcheck // exercising the documented nil-context contract
		t.Error("expected error for nil context, got nil")
	}

	// Boundary: an already cancelled context is refused before any stage runs.
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, cancelErr := RunGatedPipeline(cancelled, tmpDir, true); cancelErr == nil {
		t.Error("expected error for a cancelled context, got nil")
	}
}

// A pnpm/TypeScript repository adopted by praetor has no go.mod. The race-detector
// stage must skip it, exactly as the prefetch and security stages do, rather than
// failing the whole pipeline with "directory prefix . does not contain main module".
func TestRunTestStage_Boundary_NonGoRepository(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{\"name\":\"x\"}\n"), 0o600); err != nil {
		t.Fatalf("seed package.json: %v", err)
	}

	ran := false
	cfg := &stageConfig{
		repoDir: dir,
		run: func(context.Context, string, string, ...string) (string, error) {
			ran = true
			return "", nil
		},
	}

	_, err := runTestStage(ctx, cfg)
	if reason := wantSkip(t, err, StageNotApplicable); reason != "no go.mod: Go race-detector tests skipped" {
		t.Errorf("unexpected skip message: %q", reason)
	}
	if ran {
		t.Error("go test was executed in a repository with no go.mod")
	}
}

// seedGoModule writes a minimal go.mod so a fixture is recognised as a Go repository.
// The prefetch, security and test stages all skip where there is none, so a fixture
// exercising the Go path has to declare a module.
func seedGoModule(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gating.test\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
}

func TestExecuteStage_SkipUnderExpiredCtx(t *testing.T) {
	cfg, _ := newTestConfig(t, t.TempDir(), false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fn := func(context.Context, *stageConfig) (string, error) {
		return "", skipped("dry run: nothing ran")
	}

	if err := executeStage(ctx, stage{"Skip", fn}, cfg); err == nil || !strings.Contains(err.Error(), "fired first") {
		t.Fatalf("a skip under expired ctx must fail the stage as a cut, got %v", err)
	}
	got := cfg.rep.Stages[0]
	if got.Status != StageFailed || !strings.Contains(got.Message, "fired first") {
		t.Errorf("got %+v, want status %s and cut message", got, StageFailed)
	}
}

func TestRunReceiptStage_CancelledContext(t *testing.T) {
	cfg, _ := newTestConfig(t, t.TempDir(), false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runReceiptStage(ctx, cfg)
	if err == nil || !strings.Contains(err.Error(), "context cancelled before receipt could be minted") {
		t.Errorf("expected cancellation error, got %v", err)
	}
}
