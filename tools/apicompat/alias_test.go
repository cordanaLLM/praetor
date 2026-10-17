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
	stubPath := filepath.ToSlash(rel + "/apidiff-stub")
	if rel == "." {
		stubPath = "apidiff-stub"
	}
	stubText, err := git(root, "show", os.Args[2]+":"+stubPath)
	if err == nil && strings.TrimSpace(stubText) == "compatible" {
		os.Exit(0)
	}
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

func crossModuleAliasFixture(t *testing.T, h aliasTestHarness) (*fixture, string, string) {
	t.Helper()
	f := newFixture(t, gateHarness{env: h.env})
	baseFiles := map[string]string{
		"go.mod":                  "module example.com/storage\n\ngo 1.21\n\nrequire example.com/storage/core v0.0.0\nreplace example.com/storage/core => ./core\n",
		"storage/storage.go":      "package storage\n\ntype Bucket struct { Name string }\n",
		"core/go.mod":             "module example.com/storage/core\n\ngo 1.21\n",
		"core/storage/storage.go": "package storage\n\ntype Bucket struct { Name string }\n",
		"core/apidiff-stub":       "compatible\n",
		"apidiff-stub":            "compatible\n",
		"split/go.mod":            "module example.com/storage/split\n\ngo 1.21\n\nrequire (\n\texample.com/storage v0.0.0\n\texample.com/storage/core v0.0.0\n)\nreplace example.com/storage => ..\nreplace example.com/storage/core => ../core\n",
		"split/img/img.go":        "package img\n\nimport \"example.com/storage/storage\"\n\ntype Source struct{}\nfunc SourceFromBucket(bucket storage.Bucket) Source { return Source{} }\n",
		"split/apidiff-stub":      "example.com/storage/split/img\n  Incompatible changes:\n  - SourceFromBucket: changed from func(example.com/storage/storage.Bucket) Source to func(example.com/storage/storage.Bucket) Source\n",
		"plain/go.mod":            "module example.com/storage/plain\n\ngo 1.21\n",
		"plain/plain/plain.go":    "package plain\n\nfunc Value() int { return 1 }\n",
		"plain/apidiff-stub":      "compatible\n",
	}
	base := f.commit("fixture", baseFiles)
	f.git("tag", "v1.0.0")
	headFiles := map[string]string{
		"storage/storage.go": "package storage\n\nimport corestorage \"example.com/storage/core/storage\"\n\ntype Bucket = corestorage.Bucket\n",
	}
	head := f.commit("alias root type", headFiles)
	return f, base, head
}

// Positive: a root module alias reclassifies an identical signature change in a split module.
func TestGate_Positive_CrossModuleIdenticalAliasAccepted(t *testing.T) {
	t.Parallel()
	h := newAliasHarness(t)
	f, base, head := crossModuleAliasFixture(t, h)
	run := h.run(t, f.dir, nil, "-base="+base, "-policy=reject")
	if run.status != gateExitCompatible {
		t.Fatalf("status = %d, want %d (stdout: %s, stderr: %s)", run.status, gateExitCompatible, run.stdout, run.stderr)
	}
	if !strings.Contains(run.stdout, "SourceFromBucket: changed from func(example.com/storage/storage.Bucket) Source to func(example.com/storage/storage.Bucket) Source (compatible: alias of identical type)") {
		t.Fatalf("stdout misses split-module alias reclassification: %s", run.stdout)
	}
	if !strings.Contains(run.stdout, head[:12]) {
		t.Fatalf("stdout misses compared head %s: %s", head[:12], run.stdout)
	}
}

// Negative: cross-module alias filtering keeps a real parameter change incompatible.
func TestGate_Negative_CrossModuleAliasKeepsRealChangeRejected(t *testing.T) {
	t.Parallel()
	h := newAliasHarness(t)
	f, base, _ := crossModuleAliasFixture(t, h)
	stub := "example.com/storage/split/img\n  Incompatible changes:\n" +
		"  - SourceFromBucket: changed from func(example.com/storage/storage.Bucket) Source to func(example.com/storage/storage.Bucket) Source\n" +
		"  - SourceFromName: changed from func(int) Source to func(string) Source\n"
	f.commit("plant parameter change", map[string]string{"split/apidiff-stub": stub})
	run := h.run(t, f.dir, nil, "-base="+base, "-policy=reject")
	if run.status != gateExitRejected {
		t.Fatalf("status = %d, want %d (stdout: %s, stderr: %s)", run.status, gateExitRejected, run.stdout, run.stderr)
	}
	if !strings.Contains(run.stdout, "SourceFromBucket: changed from func(example.com/storage/storage.Bucket) Source to func(example.com/storage/storage.Bucket) Source (compatible: alias of identical type)") {
		t.Fatalf("stdout misses compatible alias reclassification: %s", run.stdout)
	}
	if !strings.Contains(run.stdout, "- SourceFromName: changed from func(int) Source to func(string) Source") {
		t.Fatalf("stdout lost the real parameter change: %s", run.stdout)
	}
}

// Boundary: a target with no aliased type keeps the checker's incompatible output unchanged.
func TestGate_Boundary_CrossModuleAliasesDoNotChangeUnrelatedTarget(t *testing.T) {
	t.Parallel()
	h := newAliasHarness(t)
	f, base, _ := crossModuleAliasFixture(t, h)
	stub := "example.com/storage/plain/plain\n  Incompatible changes:\n" +
		"  - Value: changed from func() int to func() string\n"
	f.commit("plant unrelated change", map[string]string{"plain/apidiff-stub": stub})
	run := h.run(t, f.dir, nil, "-base="+base, "-policy=reject")
	if run.status != gateExitRejected {
		t.Fatalf("status = %d, want %d (stdout: %s, stderr: %s)", run.status, gateExitRejected, run.stdout, run.stderr)
	}
	if !strings.Contains(run.stdout, stub) || strings.Contains(run.stdout, "Value: changed from func() int to func() string (compatible:") {
		t.Fatalf("stdout changed unrelated target output: %s", run.stdout)
	}
}

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
	cmd.Env = h.env // hermetic: the push gate has no global git identity
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
	cmd.Env = h.env // hermetic: the push gate has no global git identity
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
	cmd.Env = h.env // hermetic: the push gate has no global git identity
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, string(out))
	}
	cmd = exec.Command("git", "-C", repo, "commit", "-m", "apidiff-stub")
	cmd.Env = h.env // hermetic: the push gate has no global git identity
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
	cmd.Env = h.env // hermetic: the push gate has no global git identity
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, string(out))
	}
	cmd = exec.Command("git", "-C", repo, "commit", "-m", "apidiff-stub")
	cmd.Env = h.env // hermetic: the push gate has no global git identity
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
