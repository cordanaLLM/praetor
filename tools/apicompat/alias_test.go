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
	"github.com/cordanaLLM/praetor/internal/util"
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

func nestedModuleAliasFixture(t *testing.T, h aliasTestHarness) (*fixture, string, string) {
	t.Helper()
	f := newFixture(t, gateHarness{env: h.env})
	baseFiles := map[string]string{
		"go.mod":           "module example.com/repo\n\ngo 1.21\n\nrequire (\n\texample.com/dep v0.0.0\n\texample.com/repo/sub v0.0.0\n)\nreplace example.com/dep => ./dep\nreplace example.com/repo/sub => ./sub\n",
		"p/p.go":           "package p\n\nimport (\n\t\"example.com/repo/sub/a\"\n)\n\ntype Config struct{}\ntype W struct{}\ntype S func() error\n\nfunc New(Config, S, a.T) (*W, error) { return nil, nil }\n",
		"sub/go.mod":       "module example.com/repo/sub\n\ngo 1.21\n\nrequire example.com/dep v0.0.0\nreplace example.com/dep => ../dep\n",
		"sub/a/a.go":       "package a\n\nimport \"example.com/dep/dep\"\n\ntype T = dep.T\n",
		"sub/apidiff-stub": "compatible\n",
		"dep/go.mod":       "module example.com/dep\n\ngo 1.21\n",
		"dep/dep/dep.go":   "package dep\n\ntype T struct{}\n",
		"dep/apidiff-stub": "compatible\n",
		"apidiff-stub":     "compatible\n",
	}
	base := f.commit("fixture", baseFiles)
	f.git("tag", "v1.0.0")
	headFiles := map[string]string{
		"p/ptypes/types.go": "package ptypes\n\ntype S func() error\n",
		"p/p.go":            "package p\n\nimport (\n\t\"example.com/repo/p/ptypes\"\n\t\"example.com/repo/sub/a\"\n)\n\ntype Config struct{}\ntype W struct{}\ntype S = ptypes.S\n\nfunc New(Config, S, a.T) (*W, error) { return nil, nil }\n",
		"apidiff-stub":      "example.com/repo/p\n  Incompatible changes:\n  - New: changed from func(Config, S, a.T) (*W, error) to func(Config, S, a.T) (*W, error)\n  - S: changed from S to example.com/repo/p/ptypes.S\n",
	}
	head := f.commit("alias root type S", headFiles)
	return f, base, head
}

// Positive: repo with root module example.com/repo and nested module example.com/repo/sub;
// sub/a/a.go has type T = dep.T with dep a package of a third module; root package p has
// type S func() error and func New(Config, S, a.T) (*W, error); commit moves S into p/ptypes
// and leaves type S = ptypes.S in p. Gate with -policy=reject must exit compatible and print
// the alias-of-identical-type reclassification for New.
func TestGate_Positive_NestedModuleAliasesDoNotPolluteRoot(t *testing.T) {
	t.Parallel()
	h := newAliasHarness(t)
	f, base, head := nestedModuleAliasFixture(t, h)
	run := h.run(t, f.dir, nil, "-base="+base, "-policy=reject")
	if run.status != gateExitCompatible {
		t.Fatalf("status = %d, want %d (stdout: %s, stderr: %s)", run.status, gateExitCompatible, run.stdout, run.stderr)
	}
	if !strings.Contains(run.stdout, "New: changed from func(Config, S, a.T) (*W, error) to func(Config, S, a.T) (*W, error) (compatible: alias of identical type)") {
		t.Fatalf("stdout misses New alias reclassification: %s", run.stdout)
	}
	if !strings.Contains(run.stdout, head[:12]) {
		t.Fatalf("stdout misses compared head %s: %s", head[:12], run.stdout)
	}
}

// Negative: same fixture, but the commit really changes New's parameter (S -> func() int): still rejected.
func TestGate_Negative_RootModuleRealParameterChangeWithNestedModule(t *testing.T) {
	t.Parallel()
	h := newAliasHarness(t)
	f, base, _ := nestedModuleAliasFixture(t, h)
	stub := "example.com/repo/p\n  Incompatible changes:\n" +
		"  - New: changed from func(Config, S, a.T) (*W, error) to func(Config, func() int, a.T) (*W, error)\n" +
		"  - S: changed from S to example.com/repo/p/ptypes.S\n"
	f.commit("plant parameter change", map[string]string{"apidiff-stub": stub})
	run := h.run(t, f.dir, nil, "-base="+base, "-policy=reject")
	if run.status != gateExitRejected {
		t.Fatalf("status = %d, want %d (stdout: %s, stderr: %s)", run.status, gateExitRejected, run.stdout, run.stderr)
	}
	if !strings.Contains(run.stdout, "S: changed from S to example.com/repo/p/ptypes.S (compatible: alias of identical type)") {
		t.Fatalf("stdout misses compatible alias reclassification for S: %s", run.stdout)
	}
	if !strings.Contains(run.stdout, "- New: changed from func(Config, S, a.T) (*W, error) to func(Config, func() int, a.T) (*W, error)") {
		t.Fatalf("stdout lost the real parameter change for New: %s", run.stdout)
	}
}

func nestedDeeperModuleAliasFixture(t *testing.T, h aliasTestHarness) (*fixture, string, string) {
	t.Helper()
	f := newFixture(t, gateHarness{env: h.env})
	baseFiles := map[string]string{
		"go.mod":                "module example.com/repo\n\ngo 1.21\n\nrequire (\n\texample.com/dep v0.0.0\n\texample.com/repo/sub v0.0.0\n)\nreplace example.com/dep => ./dep\nreplace example.com/repo/sub => ./sub\n",
		"apidiff-stub":          "compatible\n",
		"dep/go.mod":            "module example.com/dep\n\ngo 1.21\n",
		"dep/dep/dep.go":        "package dep\n\ntype T struct{}\n",
		"dep/apidiff-stub":      "compatible\n",
		"sub/go.mod":            "module example.com/repo/sub\n\ngo 1.21\n\nrequire (\n\texample.com/dep v0.0.0\n\texample.com/repo/sub/deep v0.0.0\n)\nreplace example.com/dep => ../dep\nreplace example.com/repo/sub/deep => ./deep\n",
		"sub/subpkg/subpkg.go":  "package subpkg\n\nimport \"example.com/repo/sub/deep/deep\"\n\ntype X func() error\n\nfunc Do(X, deep.D) error { return nil }\n",
		"sub/apidiff-stub":      "compatible\n",
		"sub/deep/go.mod":       "module example.com/repo/sub/deep\n\ngo 1.21\n\nrequire example.com/dep v0.0.0\nreplace example.com/dep => ../../dep\n",
		"sub/deep/deep/deep.go": "package deep\n\ntype D struct{}\n\nfunc Run(int) error { return nil }\n",
		"sub/deep/apidiff-stub": "compatible\n",
	}
	base := f.commit("fixture", baseFiles)
	f.git("tag", "v1.0.0")
	headFiles := map[string]string{
		"sub/subpkg/subtypes/types.go": "package subtypes\n\ntype X func() error\n",
		"sub/subpkg/subpkg.go":         "package subpkg\n\nimport (\n\t\"example.com/repo/sub/deep/deep\"\n\t\"example.com/repo/sub/subpkg/subtypes\"\n)\n\ntype X = subtypes.X\n\nfunc Do(X, deep.D) error { return nil }\n",
		"sub/apidiff-stub":             "example.com/repo/sub/subpkg\n  Incompatible changes:\n  - Do: changed from func(X, deep.D) error to func(X, deep.D) error\n  - X: changed from X to example.com/repo/sub/subpkg/subtypes.X\n",
		"sub/deep/deep/deep.go":        "package deep\n\nimport \"example.com/dep/dep\"\n\ntype D = dep.T\n\nfunc Run(string) error { return nil }\n",
		"sub/deep/apidiff-stub":        "example.com/repo/sub/deep/deep\n  Incompatible changes:\n  - Run: changed from func(int) error to func(string) error\n",
	}
	head := f.commit("alias sub type and plant real change in sub/deep", headFiles)
	return f, base, head
}

// Boundary: target is the nested module sub, which holds a deeper module sub/deep with its own
// alias; an alias-identical change inside sub is accepted, a real change inside sub/deep is
// reported for sub/deep only.
func TestGate_Boundary_NestedModuleAliasWithDeeperModule(t *testing.T) {
	t.Parallel()
	h := newAliasHarness(t)
	f, base, _ := nestedDeeperModuleAliasFixture(t, h)
	run := h.run(t, f.dir, nil, "-base="+base, "-policy=reject")
	if run.status != gateExitRejected {
		t.Fatalf("status = %d, want %d (stdout: %s, stderr: %s)", run.status, gateExitRejected, run.stdout, run.stderr)
	}
	if !strings.Contains(run.stdout, "Do: changed from func(X, deep.D) error to func(X, deep.D) error (compatible: alias of identical type)") {
		t.Fatalf("stdout misses Do alias reclassification in sub: %s", run.stdout)
	}
	if !strings.Contains(run.stdout, "X: changed from X to example.com/repo/sub/subpkg/subtypes.X (compatible: alias of identical type)") {
		t.Fatalf("stdout misses X alias reclassification in sub: %s", run.stdout)
	}
	if !strings.Contains(run.stdout, "- Run: changed from func(int) error to func(string) error") {
		t.Fatalf("stdout misses real change in sub/deep: %s", run.stdout)
	}
	if strings.Contains(run.stdout, "Run: changed from func(int) error to func(string) error (compatible:") {
		t.Fatalf("stdout incorrectly reclassified real change in sub/deep: %s", run.stdout)
	}
}

func siblingPrefixAliasFixture(t *testing.T, h aliasTestHarness) (*fixture, string, string) {
	t.Helper()
	f := newFixture(t, gateHarness{env: h.env})
	baseFiles := map[string]string{
		"go.mod":           "module example.com/repo\n\ngo 1.21\n\nrequire (\n\texample.com/dep v0.0.0\n\texample.com/repo/sub v0.0.0\n)\nreplace example.com/dep => ./dep\nreplace example.com/repo/sub => ./sub\n",
		"subx/subx.go":     "package subx\n\nimport (\n\t\"example.com/repo/sub/a\"\n)\n\ntype Config struct{}\ntype W struct{}\ntype S func() error\n\nfunc New(Config, S, a.T) (*W, error) { return nil, nil }\n",
		"sub/go.mod":       "module example.com/repo/sub\n\ngo 1.21\n\nrequire example.com/dep v0.0.0\nreplace example.com/dep => ../dep\n",
		"sub/a/a.go":       "package a\n\nimport \"example.com/dep/dep\"\n\ntype T = dep.T\n",
		"sub/apidiff-stub": "compatible\n",
		"dep/go.mod":       "module example.com/dep\n\ngo 1.21\n",
		"dep/dep/dep.go":   "package dep\n\ntype T struct{}\n",
		"dep/apidiff-stub": "compatible\n",
		"apidiff-stub":     "compatible\n",
	}
	base := f.commit("fixture", baseFiles)
	f.git("tag", "v1.0.0")
	headFiles := map[string]string{
		"subx/subtypes/types.go": "package subtypes\n\ntype S func() error\n",
		"subx/subx.go":           "package subx\n\nimport (\n\t\"example.com/repo/subx/subtypes\"\n\t\"example.com/repo/sub/a\"\n)\n\ntype Config struct{}\ntype W struct{}\ntype S = subtypes.S\n\nfunc New(Config, S, a.T) (*W, error) { return nil, nil }\n",
		"apidiff-stub":           "example.com/repo/subx\n  Incompatible changes:\n  - New: changed from func(Config, S, a.T) (*W, error) to func(Config, S, a.T) (*W, error)\n  - S: changed from S to example.com/repo/subx/subtypes.S\n",
	}
	head := f.commit("alias root package subx type S", headFiles)
	return f, base, head
}

// Boundary: root module has package subx (no go.mod) sharing directory prefix with nested
// module sub; sub declares an alias, and root package subx turns a type into an alias of an
// identical type. Gate must not let prefix matching assign subx/ files to module sub, keeping
// the root alias valid and accepted under -policy=reject.
func TestGate_Boundary_SiblingPrefixNotMaskedByNestedModule(t *testing.T) {
	t.Parallel()
	h := newAliasHarness(t)
	f, base, head := siblingPrefixAliasFixture(t, h)
	run := h.run(t, f.dir, nil, "-base="+base, "-policy=reject")
	if run.status != gateExitCompatible {
		t.Fatalf("status = %d, want %d (stdout: %s, stderr: %s)", run.status, gateExitCompatible, run.stdout, run.stderr)
	}
	if !strings.Contains(run.stdout, "New: changed from func(Config, S, a.T) (*W, error) to func(Config, S, a.T) (*W, error) (compatible: alias of identical type)") {
		t.Fatalf("stdout misses New alias reclassification: %s", run.stdout)
	}
	if !strings.Contains(run.stdout, head[:12]) {
		t.Fatalf("stdout misses compared head %s: %s", head[:12], run.stdout)
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
	cmdCtx, err := util.WithCommandEnvironment(ctx, append(slices.Clone(h.env), env...))
	if err != nil {
		t.Fatalf("command environment: %v", err)
	}
	res, err := util.RunCommandBytes(cmdCtx, dir, h.gate, util.MaxCommandOutputBytes, append([]string{"-checker=" + h.checker}, args...)...)
	run := gateRun{stdout: string(res.Stdout), stderr: string(res.Stderr)}
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

func (h aliasTestHarness) git(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmdCtx, err := util.WithCommandEnvironment(t.Context(), h.env)
	if err != nil {
		t.Fatalf("git env: %v", err)
	}
	if out, err := util.RunGit(cmdCtx, "", append([]string{"-C", repo}, args...)...); err != nil {
		action := args[0]
		if action == "add" {
			action = "git add"
		}
		t.Fatalf("%s: %v\n%s", action, err, out)
	}
}

func cloneAliasFixture(t *testing.T) string {
	t.Helper()
	bundlePath, err := filepath.Abs(filepath.Join("testdata", "alias.bundle"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if out, err := util.RunGit(t.Context(), "", "clone", "--quiet", bundlePath, dir); err != nil {
		t.Fatalf("clone bundle: %v\n%s", err, out)
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
	h.git(t, repo, "commit", "-a", "-m", "target gains method")
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
	h.git(t, repo, "commit", "-a", "-m", "target loses method")
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
	h.git(t, repo, "add", "apidiff-stub")
	h.git(t, repo, "commit", "-m", "apidiff-stub")
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
	h.git(t, repo, "add", "apidiff-stub")
	h.git(t, repo, "commit", "-m", "apidiff-stub")
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
