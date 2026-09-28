// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// fakeCargoSource stands in for cargo. It appends "<working directory>\t<arguments>" to
// $FAKE_CARGO_LOG, and fails with cargo's exit status 101 when its arguments start with
// $FAKE_CARGO_FAIL. Built from Go source, it runs the same way on every platform (HISS-21).
const fakeCargoSource = `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	args := strings.Join(os.Args[1:], " ")
	wd, err := os.Getwd()
	if err != nil {
		os.Exit(3)
	}
	log, err := os.OpenFile(os.Getenv("FAKE_CARGO_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(3)
	}
	fmt.Fprintf(log, "%s\t%s\n", wd, args)
	if log.Close() != nil {
		os.Exit(3)
	}
	if fail := os.Getenv("FAKE_CARGO_FAIL"); fail != "" && strings.HasPrefix(args, fail) {
		fmt.Println("error: denied by the fixture")
		os.Exit(101)
	}
}
`

// fakeCargoOnPath builds the fake cargo and an empty cargo-audit into a directory put first on
// PATH, and returns the log the fake appends each invocation to.
func fakeCargoOnPath(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	testsupport.BuildExecutable(t, bin, "cargo", fakeCargoSource)
	testsupport.BuildExecutable(t, bin, "cargo-audit", "package main\n\nfunc main() {}\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := filepath.Join(t.TempDir(), "cargo.log")
	t.Setenv("FAKE_CARGO_LOG", log)
	t.Setenv("FAKE_CARGO_FAIL", "")
	t.Setenv(TestStageTimeoutEnv, "")
	return log
}

// sandboxReceiptKey points the signing-key lookup at a fresh key and away from the developer's
// own, on every platform's per-user configuration variables, and returns its public half.
func sandboxReceiptKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	home := t.TempDir()
	for _, name := range []string{"XDG_CONFIG_HOME", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(name, home)
	}
	pub, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	t.Setenv(lockdown.SigningKeyEnv, hex.EncodeToString(priv.Seed()))
	return pub
}

// cargoGateConfig commits a Cargo workspace to a hermetic repository and returns a production
// stage configuration for it: the real command runner and PATH lookup, with the tree reported
// clean at HEAD.
func cargoGateConfig(t *testing.T) *stageConfig {
	t.Helper()
	repo := newHermeticGitRepo(t)
	commitFile(t, repo, "Cargo.toml", "[workspace]\nmembers = []\nresolver = \"3\"\n")
	commitFile(t, repo, CargoLockFile, "version = 4\n")
	writeLockfiles(t, repo)
	rep := &PipelineReport{Repository: "example/crate", CommitSHA: headOf(t, repo), WorktreeClean: true,
		Stages: make([]StageResult, 0, maxStages)}
	cfg := newStageConfig(repo, false, rep)
	cfg.inspectTree = cleanTree(rep)
	return cfg
}

// runThroughReceipt runs the toolchain stages and then the receipt stage, stopping at the first
// failure exactly as executeStages does.
func runThroughReceipt(t *testing.T, cfg *stageConfig) error {
	t.Helper()
	stages := append(toolchainStages(), stage{stageReceipt, runReceiptStage})
	for i := 0; i < len(stages); i++ {
		if err := executeStage(t.Context(), stages[i], cfg); err != nil {
			return err
		}
	}
	return nil
}

// cargoInvocations reads the fake's log as "argv" lines, with whether each ran in a worktree.
func cargoInvocations(t *testing.T, log string) (argv []string, inWorktree []bool) {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read the fake cargo log: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		dir, args, _ := strings.Cut(line, "\t")
		argv = append(argv, args)
		inWorktree = append(inWorktree, strings.Contains(filepath.ToSlash(dir), "/worktrees/"))
	}
	return argv, inWorktree
}

// Positive: with cargo and cargo-audit on PATH, a Cargo repository runs all four commands through
// the production runner and receives a receipt that verifies and records each Cargo outcome.
func TestCargoGate_Positive_FakeCargoOnPathMintsReceipt(t *testing.T) {
	pub := sandboxReceiptKey(t)
	log := fakeCargoOnPath(t)
	cfg := cargoGateConfig(t)
	if err := runThroughReceipt(t, cfg); err != nil {
		t.Fatalf("a passing Cargo repository was rejected: %v (%+v)", err, cfg.rep.Stages)
	}
	argv, inWorktree := cargoInvocations(t, log)
	want := []string{"fetch --locked", "audit", "test --workspace --locked", "clippy --workspace --all-targets -- -D warnings"}
	if strings.Join(argv, "|") != strings.Join(want, "|") {
		t.Fatalf("cargo ran %q, want %q", argv, want)
	}
	if inWorktree[0] || inWorktree[1] || !inWorktree[2] || !inWorktree[3] {
		t.Errorf("fetch and audit run in the repository, test and clippy in the worktree: %v", inWorktree)
	}
	rf, err := lockdown.LoadReceiptFile(filepath.Join(cfg.repoDir, ReceiptFileName))
	if err != nil {
		t.Fatalf("LoadReceiptFile: %v", err)
	}
	if err := lockdown.VerifyPinnedReceiptFile(rf, pub); err != nil {
		t.Fatalf("the receipt does not verify: %v", err)
	}
	for _, line := range []string{"stage\t" + stagePrefetch + "\tpassed\tcargo: cargo fetch --locked passed",
		"stage\t" + stageSecurity + "\tpassed\tcargo: cargo audit passed",
		"stage\t" + stageTests + "\tpassed\tcargo: " + cargoTestsPassed} {
		if !strings.Contains(rf.GateOutput, line+"\n") {
			t.Errorf("the signed output lacks %q:\n%s", line, rf.GateOutput)
		}
	}
}

// Negative: clippy denying a warning fails the test stage through the production runner, and the
// run stops before the receipt stage, so nothing is signed.
func TestCargoGate_Negative_ClippyFailureMintsNoReceipt(t *testing.T) {
	sandboxReceiptKey(t)
	log := fakeCargoOnPath(t)
	t.Setenv("FAKE_CARGO_FAIL", "clippy")
	cfg := cargoGateConfig(t)
	err := runThroughReceipt(t, cfg)
	if err == nil || !strings.Contains(err.Error(), "cargo clippy --workspace --all-targets -- -D warnings failed") {
		t.Fatalf("want the clippy failure, got %v", err)
	}
	last := cfg.rep.Stages[len(cfg.rep.Stages)-1]
	if last.Name != stageTests || last.Status != StageFailed {
		t.Errorf("the run must stop failed at %s, stopped at %+v", stageTests, last)
	}
	if argv, _ := cargoInvocations(t, log); len(argv) != 4 {
		t.Errorf("cargo ran %q; clippy runs after the suite and nothing after clippy", argv)
	}
	if _, statErr := os.Stat(filepath.Join(cfg.repoDir, ReceiptFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a failed run wrote a receipt: %v", statErr)
	}
}
