package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

func TestMergeAgentsPreflightsProjectionBeforeWrite(t *testing.T) {
	repo := newTestRepo(t, "harness-budget")
	path := filepath.Join(repo, agentsFile)
	initial := "# Repository rules\n" + strings.Repeat("retain this rule\n", 260)
	if err := os.WriteFile(path, []byte(initial), filePerm); err != nil {
		t.Fatal(err)
	}
	vendorPaths := []string{"CLAUDE.md", ".cursor/rules/hiss-invariants.mdc", ".github/copilot-instructions.md", ".windsurfrules", ".gemini/GEMINI.md", ".codex/rules.md"}
	for _, vendor := range vendorPaths {
		mustWrite(t, filepath.Join(repo, vendor), "incumbent vendor context\n")
	}
	before := mustRead(t, path)
	vendorBefore := make(map[string]string, len(vendorPaths))
	for _, vendor := range vendorPaths {
		vendorBefore[vendor] = mustRead(t, filepath.Join(repo, vendor))
	}
	s := &adoptSession{repoPath: repo, repoName: "fixture", arch: "framework", report: &AdoptReport{}, verification: &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}}, opts: AdoptOptions{Force: false}}
	if err := reconcileAgentHarness(context.Background(), s); err == nil {
		t.Fatal("oversized merged context accepted")
	}
	if got := mustRead(t, path); got != before {
		t.Fatal("oversized preflight changed canonical AGENTS.md")
	}
	for _, vendor := range vendorPaths {
		if got := mustRead(t, filepath.Join(repo, vendor)); got != vendorBefore[vendor] {
			t.Fatalf("oversized preflight changed vendor %s", vendor)
		}
	}
}

func TestForceHarnessPreflightPreservesRecognizedTail(t *testing.T) {
	repo := newTestRepo(t, "harness-force-budget")
	path := filepath.Join(repo, agentsFile)
	harness, err := buildAgentHarness("fixture", "framework", &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	initial := harness + harnessSeparator + "\n" + strings.Repeat("retain this rule\n", 260)
	if err := os.WriteFile(path, []byte(initial), filePerm); err != nil {
		t.Fatal(err)
	}
	s := &adoptSession{repoPath: repo, repoName: "fixture", arch: "framework", report: &AdoptReport{}, verification: &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}}, opts: AdoptOptions{Force: true}}
	if err := reconcileAgentHarness(context.Background(), s); err == nil {
		t.Fatal("oversized force replacement accepted")
	}
	if got := mustRead(t, path); got != initial {
		t.Fatal("force preflight changed canonical AGENTS.md")
	}
}

func TestMergeAgentsProjectionAcceptsShortTail(t *testing.T) {
	repo := newTestRepo(t, "harness-short-tail")
	path := filepath.Join(repo, agentsFile)
	initial := "# Repository rules\nKeep project instructions.\n"
	if err := os.WriteFile(path, []byte(initial), filePerm); err != nil {
		t.Fatal(err)
	}
	s := &adoptSession{repoPath: repo, repoName: "fixture", arch: "framework", report: &AdoptReport{}, opts: AdoptOptions{Force: false}}
	harness, err := buildAgentHarness(s.repoName, s.arch, &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := mergeExistingAgentsContent(s, path, initial, harness)
	if err != nil || !strings.Contains(merged, "Keep project instructions.") || mustRead(t, path) != merged {
		t.Fatalf("short context merge failed: %v", err)
	}
}

func TestValidateHarnessProjectionExactBudget(t *testing.T) {
	harness, err := buildAgentHarness("fixture", "framework", &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	var exact string
	base := strings.TrimSuffix(harness, "\n") + "\n"
	initial, compileErr := compiler.NewTranspiler().CompileContent(base)
	if compileErr != nil {
		t.Fatal(compileErr)
	}
	maxLines := 0
	for _, file := range initial.Files {
		if file.LineCount > maxLines {
			maxLines = file.LineCount
		}
	}
	for n := compiler.MaxLineBudget - maxLines - 2; n <= compiler.MaxLineBudget-maxLines+2; n++ {
		if n < 0 {
			continue
		}
		candidate := base + strings.Repeat("x\n", n)
		result, compileErr := compiler.NewTranspiler().CompileContent(candidate)
		if compileErr != nil {
			continue
		}
		maxLines := 0
		for _, file := range result.Files {
			if file.LineCount > maxLines {
				maxLines = file.LineCount
			}
		}
		if maxLines == compiler.MaxLineBudget {
			exact = candidate
			break
		}
	}
	if exact == "" {
		t.Fatal("failed to construct exact line-budget fixture")
	}
	if err := validateHarnessProjection(exact); err != nil {
		t.Fatalf("exact projection budget rejected: %v", err)
	}
	if err := validateHarnessProjection(exact + "overflow\n"); err == nil {
		t.Fatal("projection over budget accepted")
	}
}

func TestResolveAgentsContentPreflightHonorsDryRun(t *testing.T) {
	repo := newTestRepo(t, "harness-dry-run-budget")
	path := filepath.Join(repo, agentsFile)
	initial := "# Repository rules\n" + strings.Repeat("retain this rule\n", 260)
	if err := os.WriteFile(path, []byte(initial), filePerm); err != nil {
		t.Fatal(err)
	}
	s := &adoptSession{repoPath: repo, repoName: "fixture", arch: "framework", report: &AdoptReport{}, verification: &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}}, opts: AdoptOptions{DryRun: true}}
	if _, err := resolveAgentsContent(s); err == nil {
		t.Fatal("dry-run oversized context accepted")
	}
	if got := mustRead(t, path); got != initial {
		t.Fatal("dry-run preflight changed canonical AGENTS.md")
	}
}
