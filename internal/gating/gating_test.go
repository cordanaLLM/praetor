package gating

import (
	"context"
	"encoding/hex"
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
		return out, err
	}
}

// newTestConfig builds a stage configuration whose seams never touch the real toolchain.
func newTestConfig(t *testing.T, repoDir string, dryRun bool) (*stageConfig, *[]recordedCommand) {
	t.Helper()
	recorded := &[]recordedCommand{}
	return &stageConfig{
		repoDir:  repoDir,
		dryRun:   dryRun,
		run:      fakeRunner(recorded, "", nil),
		lookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		rep:      &PipelineReport{Stages: make([]StageResult, 0, maxStages)},
	}, recorded
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

func TestExecuteStage_3D(t *testing.T) {
	cfg, _ := newTestConfig(t, t.TempDir(), false)
	ctx := context.Background()

	// Positive: a passing stage records its message.
	pass := stage{"Pass", func(context.Context, *stageConfig) (string, error) { return "skipped: no go.mod", nil }}
	if err := executeStage(ctx, pass, cfg); err != nil {
		t.Fatalf("passing stage returned %v", err)
	}
	if got := cfg.rep.Stages[0]; !got.Passed || got.Message != "skipped: no go.mod" {
		t.Errorf("unexpected stage result: %+v", got)
	}

	// Negative: a failing stage records the error and propagates it.
	boom := errors.New("boom")
	fail := stage{"Fail", func(context.Context, *stageConfig) (string, error) { return "", boom }}
	if err := executeStage(ctx, fail, cfg); !errors.Is(err, boom) {
		t.Fatalf("expected the stage error to propagate, got %v", err)
	}
	if got := cfg.rep.Stages[1]; got.Passed || got.Message != "boom" {
		t.Errorf("unexpected failing stage result: %+v", got)
	}

	// Boundary: an empty message on success leaves Message empty.
	quiet := stage{"Quiet", func(context.Context, *stageConfig) (string, error) { return "", nil }}
	if err := executeStage(ctx, quiet, cfg); err != nil {
		t.Fatalf("quiet stage returned %v", err)
	}
	if got := cfg.rep.Stages[2]; !got.Passed || got.Message != "" {
		t.Errorf("unexpected quiet stage result: %+v", got)
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

	// Boundary: a repository without go.mod skips the Go scanners and says so.
	nonGo, nonGoRecorded := newTestConfig(t, t.TempDir(), false)
	msg, err := runSecurityStage(ctx, nonGo)
	if err != nil {
		t.Fatalf("expected a non-Go repository to skip, got %v", err)
	}
	if !strings.Contains(msg, "skipped") || len(*nonGoRecorded) != 0 {
		t.Errorf("expected a skip message and no commands, got %q / %+v", msg, *nonGoRecorded)
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

	// Negative: no baseline recorded, so the legacy debt is a new violation.
	cfg, _ := newTestConfig(t, repoDir, false)
	if _, err := runHissStage(ctx, cfg); err == nil {
		t.Error("expected unbaselined infractions to fail the stage")
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
	msg, err := dry.runTestStageForTest(ctx)
	if err != nil {
		t.Fatalf("dry run returned %v", err)
	}
	if !strings.Contains(msg, "dry run") || len(*dryRecorded) != 0 {
		t.Errorf("dry run executed work: %q / %+v", msg, *dryRecorded)
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
	if len(*notGitRecorded) != 0 {
		t.Errorf("no test command may run without an isolated worktree, got %+v", *notGitRecorded)
	}

	// Positive: the race detector runs inside the worktree, not in the repository.
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, recorded := newTestConfig(t, repoDir, false)
	if _, err := runTestStage(ctx, cfg); err != nil {
		t.Fatalf("test stage failed: %v", err)
	}
	if len(*recorded) != 1 {
		t.Fatalf("expected exactly one test invocation, got %+v", *recorded)
	}
	invocation := (*recorded)[0]
	if invocation.name != "go" || strings.Join(invocation.args, " ") != "test -race ./..." {
		t.Errorf("expected 'go test -race ./...', got %s %v", invocation.name, invocation.args)
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

	msg, err := runReceiptStage(context.Background(), cfg)
	if err != nil {
		t.Fatalf("dry-run receipt stage returned %v", err)
	}
	if !strings.Contains(msg, "no Exit-0 receipt") {
		t.Errorf("expected an explicit dry-run note, got %q", msg)
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
		StageResult{Name: "Prefetch & Lockfiles", Passed: true},
		StageResult{Name: "HISS Invariant Scan", Passed: true, Message: "0 infractions within the 0 baselined limit"},
	)

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
	// which is what makes it evidence rather than decoration.
	if err := lockdown.VerifyPinnedReceipt(&rf.ExecutionReceipt, pub, []byte(rf.GateOutput)); err != nil {
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

	// Any later edit to the recorded stage results invalidates the signature.
	cfg.rep.Stages[0].Passed = false
	if err := lockdown.VerifyPinnedReceipt(&rf.ExecutionReceipt, pub, cfg.rep.StageOutput()); err == nil {
		t.Error("expected a mutated stage list to invalidate the receipt")
	}
}

func TestRunReceiptStage_Negative_NoSigningKey(t *testing.T) {
	// Sandbox the key lookup so the developer's own key is never used.
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("PRAETOR_RECEIPT_KEY", "")

	repoDir := t.TempDir()
	cfg, _ := newTestConfig(t, repoDir, false)
	if _, err := runReceiptStage(context.Background(), cfg); err == nil {
		t.Fatal("expected the receipt stage to fail closed without a signing key")
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
			{Name: "Prefetch & Lockfiles", Passed: true},
			{Name: "HISS Invariant Scan", Passed: true, Message: "0 infractions\nwithin limit"},
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

	// Negative: a changed stage outcome changes the signed payload.
	rep.Stages[0].Passed = false
	if string(rep.StageOutput()) == first {
		t.Error("stage output did not change when a stage outcome changed")
	}

	// Boundary: an empty report still renders a complete header.
	empty := (&PipelineReport{}).StageOutput()
	if !strings.HasPrefix(string(empty), "praetor-gate-output/v1\n") {
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
	if len(negRep.Stages) != 1 || negRep.Stages[0].Passed {
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

	msg, err := runTestStage(ctx, cfg)
	if err != nil {
		t.Fatalf("expected a non-Go repository to be skipped, got: %v", err)
	}
	if msg != "no go.mod: Go race-detector tests skipped" {
		t.Errorf("unexpected skip message: %q", msg)
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
