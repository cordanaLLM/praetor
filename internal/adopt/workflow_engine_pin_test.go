// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/buildid"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
)

// pinGit executes git commands in root configured with a fixture user identity.
func pinGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	argv := append([]string{"-c", "user.name=Pin fixture", "-c", "user.email=pin@example.invalid"}, args...)
	result, err := util.RunCommandBytes(ctx, root, requireGit(t), 4096, argv...)
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, result.Stderr)
	}
	return strings.TrimSpace(string(result.Stdout))
}

// pinHistory creates a temporary Git repository containing ancestor, head, and diverged commit history.
func pinHistory(t *testing.T) (root, ancestor, head, diverged string) {
	t.Helper()
	root = t.TempDir()
	ancestor, head, diverged = pinHistoryAt(t, root)
	return root, ancestor, head, diverged
}

// pinHistoryAt populates an existing repository with ancestor, head, and diverged engine commits.
func pinHistoryAt(t *testing.T, root string) (ancestor, head, diverged string) {
	t.Helper()
	initTestGit(t, root)
	tree := pinGit(t, root, "mktree")
	ancestor = pinGit(t, root, "commit-tree", tree, "-m", "ancestor")
	head = pinGit(t, root, "commit-tree", tree, "-p", ancestor, "-m", "adopting engine")
	diverged = pinGit(t, root, "commit-tree", tree, "-p", ancestor, "-m", "diverged engine")
	pinGit(t, root, "update-ref", "HEAD", head)
	return ancestor, head, diverged
}

type workflowPinCase struct {
	name, declaration, second, want, refusal string
	dryRun, noSource                         bool
}

// TestAdoptWorkflowEnginePin3D pins positive, negative, and boundary cases for workflow engine pin reconciliation.
func TestAdoptWorkflowEnginePin3D(t *testing.T) {
	source, ancestor, head, diverged := pinHistory(t)
	cases := []workflowPinCase{
		{name: "stale ancestor", declaration: "PRAETOR_COMMIT: " + ancestor, want: head},
		{name: "short ref", declaration: "PRAETOR_REF='" + ancestor[:12] + "' # keep", want: head},
		{name: "diverged", declaration: "PRAETOR_COMMIT: " + diverged, want: diverged, refusal: "not an ancestor"},
		{name: "different pins", declaration: "PRAETOR_COMMIT: " + ancestor, second: head, want: ancestor, refusal: "several different"},
		{name: "no pin", declaration: "OTHER: unchanged", want: "OTHER: unchanged"},
		{name: "same pin", declaration: "PRAETOR_COMMIT: " + head, want: head},
		{name: "same abbreviated pin", declaration: "PRAETOR_REF: " + head[:12], want: head[:12]},
		{name: "tag", declaration: "PRAETOR_REF: v1.2.3", want: "v1.2.3"},
		{name: "branch", declaration: "PRAETOR_REF: main", want: "main"},
		{name: "invalid commit", declaration: "PRAETOR_COMMIT: abcdef1", want: "abcdef1"},
		{name: "dry run", declaration: "PRAETOR_COMMIT: " + ancestor, want: ancestor, dryRun: true},
		{name: "repeated pin", declaration: "PRAETOR_COMMIT: " + ancestor, second: ancestor, want: head},
		{name: "same file repeated pin", declaration: "PRAETOR_COMMIT: " + ancestor + "\r\n  PRAETOR_REF: " + ancestor, want: head},
		{name: "no source", declaration: "PRAETOR_COMMIT: " + ancestor, want: ancestor, noSource: true},
		{name: "diverged without source", declaration: "PRAETOR_COMMIT: " + diverged, want: diverged, noSource: true},
		{name: "different pins without source", declaration: "PRAETOR_COMMIT: " + ancestor, second: head, want: ancestor, refusal: "several different", noSource: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			selected := source
			if tc.noSource {
				selected = ""
			}
			report := checkWorkflowPinCase(t, selected, head, tc)
			if tc.noSource && tc.refusal == "" {
				assertPinNotEvaluatedReport(t, report, ".github/workflows/ci.yml", 2, tc.want)
			}
		})
	}
}

// checkWorkflowPinCase runs adoption against a test fixture case and asserts expected pin mutations or refusals.
func checkWorkflowPinCase(t *testing.T, source, head string, tc workflowPinCase) *AdoptReport {
	t.Helper()
	root := t.TempDir()
	initTestGit(t, root)
	rel := ".github/workflows/ci.yml"
	before := "env:\r\n  " + tc.declaration + "\r\n"
	mustWrite(t, filepath.Join(root, rel), before)
	if tc.second != "" {
		mustWrite(t, filepath.Join(root, ".github/workflows/other.yaml"), "PRAETOR_REF: "+tc.second+"\n")
	}
	s := &adoptSession{repoPath: root, opts: AdoptOptions{LockSourceRoot: source, DryRun: tc.dryRun}, report: &AdoptReport{}}
	err := reportStaleEnginePin(t.Context(), s)
	assertWorkflowPinRefusal(t, tc, err)
	got := mustRead(t, filepath.Join(root, rel))
	if !strings.Contains(got, tc.want) {
		t.Fatalf("pin stayed stale: got %q; want %s", got, tc.want)
	}
	if tc.want != head || tc.declaration == "PRAETOR_COMMIT: "+head {
		if got != before {
			t.Fatal("unchanged/refused pin was rewritten")
		}
		if tc.dryRun {
			assertPinMoveReport(t, s.report, rel, workflowPinsIn(rel, []byte(before))[0].sha, head)
		}
		return s.report
	}
	old := workflowPinsIn(rel, []byte(before))[0].sha
	assertPinMoveReport(t, s.report, rel, old, head)
	if strings.ReplaceAll(before, old, head) != got {
		t.Fatal("pin move changed surrounding bytes")
	}
	if tc.second != "" && !strings.Contains(mustRead(t, filepath.Join(root, ".github/workflows/other.yaml")), head) {
		t.Fatal("repeated pin was not moved everywhere")
	}
	return s.report
}

// TestAdoptWorkflowPinUsesRunningRevision pins that workflow reconciliation aligns with the running engine revision or release tag.
func TestAdoptWorkflowPinUsesRunningRevision(t *testing.T) {
	source, ancestor, head, _ := pinHistory(t)
	tree := pinGit(t, source, "mktree")
	newer := pinGit(t, source, "commit-tree", tree, "-p", head, "-m", "source ahead of running build")
	pinGit(t, source, "update-ref", "HEAD", newer)
	pinGit(t, source, "update-ref", "refs/tags/v1.2.3", head)
	for _, identity := range []buildid.Identity{{Revision: head[:12]}, {Release: "v1.2.3"}} {
		root := t.TempDir()
		initTestGit(t, root)
		rel := ".github/workflows/ci.yml"
		mustWrite(t, filepath.Join(root, rel), "PRAETOR_REF: "+ancestor+"\n")
		s := &adoptSession{repoPath: root, opts: AdoptOptions{LockSourceRoot: source, Engine: identity}, report: &AdoptReport{}}
		if err := reportStaleEnginePin(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		if got := mustRead(t, filepath.Join(root, rel)); !strings.Contains(got, head) || strings.Contains(got, newer) {
			t.Fatalf("pin did not follow the running engine: %s", got)
		}
	}
}

// TestAdoptWorkflowPinHistoryFailures pins refusal when workflow pin commit history cannot be resolved in the source checkout.
func TestAdoptWorkflowPinHistoryFailures(t *testing.T) {
	source, _, _, _ := pinHistory(t)
	root := t.TempDir()
	initTestGit(t, root)
	rel := ".github/workflows/ci.yml"
	before := "PRAETOR_COMMIT: " + strings.Repeat("f", 40) + "\n"
	mustWrite(t, filepath.Join(root, rel), before)
	s := &adoptSession{repoPath: root, opts: AdoptOptions{LockSourceRoot: source}, report: &AdoptReport{}}
	err := reportStaleEnginePin(t.Context(), s)
	if err == nil || !strings.Contains(err.Error(), rel+":1") {
		t.Fatalf("unknown pin history not refused by name: %v", err)
	}
	if got := mustRead(t, filepath.Join(root, rel)); got != before {
		t.Fatal("history failure changed pin")
	}
}

// TestAdoptWorkflowPinWithoutResolvableEngineCommit pins that unresolvable engine commits leave pins unevaluated without failing adoption.
func TestAdoptWorkflowPinWithoutResolvableEngineCommit(t *testing.T) {
	for _, name := range []string{"non-Git catalog", "unborn checkout", "unavailable build revision"} {
		t.Run(name, func(t *testing.T) {
			source := newAdoptLockSource(t)
			engine := buildid.Identity{}
			switch name {
			case "unborn checkout":
				initTestGit(t, source)
			case "unavailable build revision":
				pinHistoryAt(t, source)
				engine.Revision = strings.Repeat("f", 40)
			}
			root := t.TempDir()
			initTestGit(t, root)
			rel := ".github/workflows/ci.yml"
			pin := strings.Repeat("a", 40)
			before := "env:\n  PRAETOR_COMMIT: " + pin + "\n"
			mustWrite(t, filepath.Join(root, rel), before)
			report, err := Adopt(t.Context(), AdoptOptions{Path: root, LockSourceRoot: source, Engine: engine, SkipHookActivation: true})
			if err != nil {
				t.Fatal(err)
			}
			if got := mustRead(t, filepath.Join(root, rel)); got != before {
				t.Fatal("adoption without resolvable engine commit changed workflow")
			}
			assertAdoptedLock(t, root)
			assertPinNotEvaluatedReport(t, report, rel, 2, pin,
				"resolve engine commit", "in "+source+": exit status 128: ", "Git checkout")
		})
	}
}

// TestAdoptWorkflowPinIntegration pins end-to-end workflow pin advancement and refusal of diverged pin ancestry during adoption.
func TestAdoptWorkflowPinIntegration(t *testing.T) {
	source := newAdoptLockSource(t)
	ancestor, head, diverged := pinHistoryAt(t, source)
	for _, pin := range []string{ancestor, diverged} {
		root := t.TempDir()
		initTestGit(t, root)
		rel := ".github/workflows/ci.yml"
		before := "env:\n  PRAETOR_COMMIT: " + pin + "\n"
		mustWrite(t, filepath.Join(root, rel), before)
		report, err := Adopt(t.Context(), AdoptOptions{Path: root, LockSourceRoot: source, SkipHookActivation: true})
		if pin == ancestor {
			if err != nil {
				t.Fatal(err)
			}
			assertPinMoveReport(t, report, rel, ancestor, head)
			if got := mustRead(t, filepath.Join(root, rel)); !strings.Contains(got, head) {
				t.Fatal("adoption did not move the workflow pin")
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "not an ancestor") {
			t.Fatalf("diverged adoption not refused: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, manifestFile)); !os.IsNotExist(err) {
			t.Fatalf("refused adoption wrote manifest: %v", err)
		}
		if got := mustRead(t, filepath.Join(root, rel)); got != before {
			t.Fatal("refused adoption changed workflow")
		}
	}
}

// TestAdoptWorkflowPinWithoutSourceKeepsValidLock pins that omitting lock source leaves existing pins untouched and maintains valid locks.
func TestAdoptWorkflowPinWithoutSourceKeepsValidLock(t *testing.T) {
	root := t.TempDir()
	initTestGit(t, root)
	if _, err := Adopt(t.Context(), AdoptOptions{Path: root, LockSourceRoot: newAdoptLockSource(t), SkipHookActivation: true}); err != nil {
		t.Fatal(err)
	}
	rel := ".github/workflows/ci.yml"
	pin := strings.Repeat("a", 40)
	before := "env:\n  PRAETOR_COMMIT: " + pin + "\n"
	mustWrite(t, filepath.Join(root, rel), before)
	report, err := Adopt(t.Context(), AdoptOptions{Path: root, SkipHookActivation: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(root, rel)); got != before {
		t.Fatal("adoption without source changed workflow")
	}
	assertPinNotEvaluatedReport(t, report, rel, 2, pin)
}

// assertPinNotEvaluatedReport asserts that skipped engine pins generate expected warnings and action details.
func assertPinNotEvaluatedReport(t *testing.T, report *AdoptReport, rel string, line int, pin string, details ...string) {
	t.Helper()
	declaration := fmt.Sprintf("%s:%d PRAETOR_COMMIT=%s", rel, line, pin)
	if !hasWarningContaining(report, declaration) {
		t.Fatalf("pin skip not reported as warning: %v", report.Warnings)
	}
	for _, want := range details {
		if !hasWarningContaining(report, want) {
			t.Errorf("warnings %v lack pin skip detail %q", report.Warnings, want)
		}
	}
	for _, action := range report.ActionDetails {
		if action.Path == rel && action.Action == actionSkip && strings.Contains(action.Details, declaration) &&
			strings.Contains(action.Details, "not evaluated") && strings.Contains(action.Details, "--lock-source-root=<engine checkout>") {
			return
		}
	}
	t.Fatalf("missing named pin skip with source instruction: %+v", report.ActionDetails)
}

// assertWorkflowPinRefusal verifies that invalid or diverged workflow pins return the expected refusal error.
func assertWorkflowPinRefusal(t *testing.T, tc workflowPinCase, err error) {
	t.Helper()
	if tc.refusal == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), tc.refusal) || !strings.Contains(err.Error(), ".github/workflows/ci.yml:2") {
		t.Fatalf("refusal must name pin: %v", err)
	}
	if tc.second != "" && !strings.Contains(err.Error(), "other.yaml:1") {
		t.Fatalf("refusal must name both pins: %v", err)
	}
}

// assertPinMoveReport checks that an action detail records the exact SHA transition for a reconciled pin.
func assertPinMoveReport(t *testing.T, report *AdoptReport, rel, old, head string) {
	t.Helper()
	for _, action := range report.ActionDetails {
		if action.Path == rel && strings.Contains(action.Details, old) && strings.Contains(action.Details, head) {
			return
		}
	}
	t.Fatalf("missing pin move report: %+v", report.ActionDetails)
}

// TestUnevaluatedWorkflowPinsReportEveryDeclaration pins that missing or unresolvable sources record every declared pin as unevaluated.
func TestUnevaluatedWorkflowPinsReportEveryDeclaration(t *testing.T) {
	for _, unavailableSource := range []bool{false, true} {
		t.Run(fmt.Sprintf("unresolvable=%v", unavailableSource), func(t *testing.T) {
			root := t.TempDir()
			pin := strings.Repeat("a", 40)
			files := map[string]string{
				".github/workflows/ci.yml":     "env:\n  PRAETOR_COMMIT: " + pin + "\n  PRAETOR_REF: " + pin + "\n",
				".github/workflows/other.yaml": "PRAETOR_REF: " + pin + "\n",
			}
			for rel, data := range files {
				mustWrite(t, filepath.Join(root, rel), data)
			}
			s := &adoptSession{repoPath: root, report: &AdoptReport{}}
			if unavailableSource {
				s.opts.LockSourceRoot = t.TempDir()
			}
			if err := reconcileWorkflowEnginePin(t.Context(), s); err != nil {
				t.Fatal(err)
			}
			assertUnevaluatedFiles(t, root, files, s.report)
		})
	}
}

// assertUnevaluatedFiles verifies file contents remain unchanged and declarations are recorded as unevaluated.
func assertUnevaluatedFiles(t *testing.T, root string, files map[string]string, report *AdoptReport) {
	t.Helper()
	for rel, before := range files {
		if got := mustRead(t, filepath.Join(root, rel)); got != before {
			t.Fatal("unevaluated pin was rewritten")
		}
		for _, declaration := range workflowPinsIn(rel, []byte(before)) {
			assertUnevaluatedDeclaration(t, report, declaration)
		}
	}
}

// assertUnevaluatedDeclaration verifies that a specific pin declaration is recorded as skipped in the adopt report.
func assertUnevaluatedDeclaration(t *testing.T, report *AdoptReport, pin workflowEnginePin) {
	t.Helper()
	for _, detail := range report.ActionDetails {
		if detail.Path == pin.file && detail.Action == actionSkip && strings.Contains(detail.Details, pin.String()) &&
			strings.Contains(detail.Details, "not evaluated") && strings.Contains(detail.Details, "--lock-source-root=<engine checkout>") {
			return
		}
	}
	t.Errorf("missing unevaluated declaration %s: %+v", pin, report.ActionDetails)
}

// TestWorkflowEnginePinFileBound pins that only workflow files count toward ghworkflow.MaxFiles and one past it is refused.
func TestWorkflowEnginePinFileBound(t *testing.T) {
	source, ancestor, head, _ := pinHistory(t)
	root := t.TempDir()
	initTestGit(t, root)
	// nosemgrep: go_file-permissions_rule-mkdir, go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a fixture directory that only has to exist; its mode is not what this case asserts
	if err := os.MkdirAll(filepath.Join(root, ".github/workflows/00-dir.yml"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, ".github/workflows/00-README.md"), "PRAETOR_COMMIT: "+strings.Repeat("b", 40)+"\n")
	for i := 0; i < ghworkflow.MaxFiles-1; i++ {
		mustWrite(t, filepath.Join(root, fmt.Sprintf(".github/workflows/w%02d.yml", i)), "name: ci\n")
	}
	last := fmt.Sprintf(".github/workflows/w%02d.yaml", ghworkflow.MaxFiles-1)
	mustWrite(t, filepath.Join(root, last), "PRAETOR_COMMIT: "+ancestor+"\n")
	s := &adoptSession{repoPath: root, opts: AdoptOptions{LockSourceRoot: source}, report: &AdoptReport{}}
	if err := reconcileWorkflowEnginePin(t.Context(), s); err != nil {
		t.Fatalf("at workflow bound with non-workflow entries: %v", err)
	}
	if got := mustRead(t, filepath.Join(root, last)); got != "PRAETOR_COMMIT: "+head+"\n" {
		t.Fatalf("64th workflow pin was not moved: %q", got)
	}
	mustWrite(t, filepath.Join(root, last), "PRAETOR_COMMIT: "+ancestor+"\n")
	mustWrite(t, filepath.Join(root, ".github/workflows/w64.yml"), "name: extra\n")
	err := reconcileWorkflowEnginePin(t.Context(), &adoptSession{repoPath: root, opts: AdoptOptions{LockSourceRoot: source}, report: &AdoptReport{}})
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("exceeds %d workflow entries", ghworkflow.MaxFiles)) {
		t.Fatalf("one workflow past bound not refused: %v", err)
	}
	if got := mustRead(t, filepath.Join(root, last)); got != "PRAETOR_COMMIT: "+ancestor+"\n" {
		t.Fatalf("refused bound overflow rewrote pin: %q", got)
	}
}
