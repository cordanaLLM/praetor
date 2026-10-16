// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apicompat

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

const (
	gateExitCompatible = 0
	gateExitRejected   = 1
)

const aliasCheckerStubSource = `package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func git(root string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	return string(out), err
}

func main() {
	gomod, _ := os.ReadFile("go.mod")
	if strings.Contains(string(gomod), "apicompat.invalid/canary") {
		os.Exit(1)
	}
	wd, _ := os.Getwd()
	root := strings.TrimPrefix(os.Args[len(os.Args)-1], "--repo-path=")
	rel, err := filepath.Rel(root, wd)
	if err != nil || len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "stub: unexpected invocation", os.Args, err)
		os.Exit(3)
	}
	_ = rel
	if realChecker := os.Getenv("PRAETOR_TEST_REAL_CHECKER"); realChecker != "" {
		cmd := exec.Command(realChecker, os.Args[1], os.Args[2], os.Args[3])
		cmd.Dir = wd
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			os.Exit(1)
		}
		os.Exit(0)
	}
	stubText, err := git(root, "show", os.Args[2]+":apidiff-stub")
	if err == nil && stubText != "" {
		fmt.Print(stubText)
		os.Exit(1)
	}
	fmt.Println("example.com/storage/storage")
	fmt.Println("  Incompatible changes:")
	fmt.Println("  - (*LocalBucket).Get: changed from func(string) (Object, error) to func(string) (Object, error)")
	fmt.Println("  - Bucket: changed from Bucket to example.com/storage/core/storage.Bucket")
	fmt.Println("  - Connect: changed from func(Bucket) Object to func(Bucket) Object")
	fmt.Println("  - Object: changed from Object to example.com/storage/core/storage.Object")
	os.Exit(1)
}
`

type aliasTestHarness struct {
	gate    string
	checker string
	env     []string
}

func newAliasHarness(t *testing.T) aliasTestHarness {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	source, err := Read(GateFile)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	h := aliasTestHarness{
		gate:    testsupport.BuildExecutable(t, bin, "gate", string(source)),
		checker: testsupport.BuildExecutable(t, bin, "checker", aliasCheckerStubSource),
	}
	h.env = append(testsupport.HermeticGitEnv(t), "GITHUB_ACTIONS=")
	return h
}

func (h aliasTestHarness) run(t *testing.T, dir string, env []string, args ...string) gateRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), gateRunTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, h.gate, append([]string{"-checker=" + h.checker}, args...)...)
	command.Dir = dir
	command.Env = append(slices.Clone(h.env), env...)
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	run := gateRun{stdout: stdout.String(), stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		run.status = exit.ExitCode()
	default:
		t.Fatalf("run the gate: %v", err)
	}
	return run
}

func cloneAliasFixture(t *testing.T) string {
	t.Helper()
	bundlePath, err := filepath.Abs(filepath.Join("testdata", "alias.bundle"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "clone", "--quiet", bundlePath, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone bundle: %v\n%s", err, string(out))
	}
	return dir
}

// Positive: package storage moved types to core/storage and kept type Bucket = corestorage.Bucket
// aliases; the gate accepts them as compatible aliases of identical types and exits 0.
func TestGate_Positive_IdenticalTypeAliasAccepted(t *testing.T) {
	t.Parallel()
	h := newAliasHarness(t)
	repo := cloneAliasFixture(t)
	run := h.run(t, repo, nil, "-base=v1.0.0", "-policy=reject")
	if run.status != gateExitCompatible {
		t.Fatalf("status = %d, want %d (stdout: %s, stderr: %s)", run.status, gateExitCompatible, run.stdout, run.stderr)
	}
	if !strings.Contains(run.stdout, "(compatible: alias of identical type)") {
		t.Fatalf("stdout misses compatible alias note: %s", run.stdout)
	}
	if !strings.Contains(run.stdout, "Bucket: changed from Bucket to example.com/storage/core/storage.Bucket (compatible: alias of identical type)") {
		t.Fatalf("stdout misses Bucket alias reclassification: %s", run.stdout)
	}
	if !strings.Contains(run.stdout, "Every compared module is compatible") {
		t.Fatalf("stdout misses success verdict: %s", run.stdout)
	}
}

// Negative: the alias target in core/storage gains a method relative to base; the gate refuses it.
func TestGate_Negative_AliasTargetGainsMethodRejected(t *testing.T) {
	t.Parallel()
	h := newAliasHarness(t)
	repo := cloneAliasFixture(t)
	gained := "package storage\n\ntype Bucket interface {\n\tGet(name string) (Object, error)\n\tPut(name string) error\n}\n\ntype Object struct {\n\tName string\n}\n"
	if err := os.WriteFile(filepath.Join(repo, "core", "storage", "storage.go"), []byte(gained), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo, "commit", "-a", "-m", "target gains method")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, string(out))
	}
	run := h.run(t, repo, nil, "-base=v1.0.0", "-policy=reject")
	if run.status != gateExitRejected {
		t.Fatalf("status = %d, want %d (stdout: %s, stderr: %s)", run.status, gateExitRejected, run.stdout, run.stderr)
	}
	if !strings.Contains(run.stdout, "Incompatible changes:") {
		t.Fatalf("stdout misses Incompatible changes section: %s", run.stdout)
	}
	if !strings.Contains(run.stdout, "Bucket: changed from Bucket to example.com/storage/core/storage.Bucket") {
		t.Fatalf("stdout should keep Bucket as incompatible: %s", run.stdout)
	}
}

// Negative: the alias target in core/storage loses a method relative to base; the gate refuses it.
func TestGate_Negative_AliasTargetLosesMethodRejected(t *testing.T) {
	t.Parallel()
	h := newAliasHarness(t)
	repo := cloneAliasFixture(t)
	lost := "package storage\n\ntype Bucket interface{}\n\ntype Object struct {\n\tName string\n}\n"
	if err := os.WriteFile(filepath.Join(repo, "core", "storage", "storage.go"), []byte(lost), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo, "commit", "-a", "-m", "target loses method")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, string(out))
	}
	run := h.run(t, repo, nil, "-base=v1.0.0", "-policy=reject")
	if run.status != gateExitRejected {
		t.Fatalf("status = %d, want %d (stdout: %s, stderr: %s)", run.status, gateExitRejected, run.stdout, run.stderr)
	}
	if !strings.Contains(run.stdout, "Incompatible changes:") {
		t.Fatalf("stdout misses Incompatible changes section: %s", run.stdout)
	}
}

// Negative: a function's parameter type really changes; the gate refuses it.
func TestGate_Negative_FunctionParameterReallyChangedRejected(t *testing.T) {
	t.Parallel()
	h := newAliasHarness(t)
	repo := cloneAliasFixture(t)
	stub := "example.com/storage/storage\n  Incompatible changes:\n  - Connect: changed from func(Bucket, int) Object to func(Bucket, string) Object\n"
	if err := os.WriteFile(filepath.Join(repo, "apidiff-stub"), []byte(stub), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo, "add", "apidiff-stub")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, string(out))
	}
	cmd = exec.Command("git", "-C", repo, "commit", "-m", "apidiff-stub")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, string(out))
	}
	run := h.run(t, repo, nil, "-base=v1.0.0", "-policy=reject")
	if run.status != gateExitRejected {
		t.Fatalf("status = %d, want %d (stdout: %s, stderr: %s)", run.status, gateExitRejected, run.stdout, run.stderr)
	}
	if !strings.Contains(run.stdout, "Incompatible changes:") {
		t.Fatalf("stdout misses Incompatible changes section: %s", run.stdout)
	}
	if !strings.Contains(run.stdout, "Connect: changed from func(Bucket, int) Object to func(Bucket, string) Object") {
		t.Fatalf("stdout should keep Connect as incompatible: %s", run.stdout)
	}
}

// Negative: an exported name was removed; the gate refuses it.
func TestGate_Negative_RemovedExportedNameRejected(t *testing.T) {
	t.Parallel()
	h := newAliasHarness(t)
	repo := cloneAliasFixture(t)
	stub := "example.com/storage/storage\n  Incompatible changes:\n  - Connect: removed\n"
	if err := os.WriteFile(filepath.Join(repo, "apidiff-stub"), []byte(stub), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo, "add", "apidiff-stub")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, string(out))
	}
	cmd = exec.Command("git", "-C", repo, "commit", "-m", "apidiff-stub")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, string(out))
	}
	run := h.run(t, repo, nil, "-base=v1.0.0", "-policy=reject")
	if run.status != gateExitRejected {
		t.Fatalf("status = %d, want %d (stdout: %s, stderr: %s)", run.status, gateExitRejected, run.stdout, run.stderr)
	}
	if !strings.Contains(run.stdout, "Incompatible changes:") {
		t.Fatalf("stdout misses Incompatible changes section: %s", run.stdout)
	}
	if !strings.Contains(run.stdout, "- Connect: removed") {
		t.Fatalf("stdout should keep Connect: removed: %s", run.stdout)
	}
}

// Positive: integration test with real go-apidiff binary if available.
func TestGate_Positive_RealCheckerIntegration(t *testing.T) {
	realChecker := "/tmp/go-apidiff-cached"
	if _, err := os.Stat(realChecker); err != nil {
		t.Skip("cached go-apidiff not available for integration test")
	}
	h := newAliasHarness(t)
	repo := cloneAliasFixture(t)
	env := []string{"PRAETOR_TEST_REAL_CHECKER=" + realChecker}
	run := h.run(t, repo, env, "-base=v1.0.0", "-policy=reject")
	if run.status != gateExitCompatible {
		t.Fatalf("status = %d, want %d (stdout: %s, stderr: %s)", run.status, gateExitCompatible, run.stdout, run.stderr)
	}
	if !strings.Contains(run.stdout, "(compatible: alias of identical type)") {
		t.Fatalf("stdout misses compatible alias note: %s", run.stdout)
	}
	if !strings.Contains(run.stdout, "Every compared module is compatible") {
		t.Fatalf("stdout misses success verdict: %s", run.stdout)
	}
}
