package adopt

import (
	"context"
	"fmt"
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
	harness, err := buildAgentHarness(adoptedFacts("", "fixture", "framework", &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}}))
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

// TestKeptHarnessFoldPreflightsProjectionBeforeWrite (#1073): text folded from an authored
// CLAUDE.md into a harness a run without --force keeps is validated before AGENTS.md is written;
// an oversized fold fails the step with AGENTS.md and CLAUDE.md as the run found them.
func TestKeptHarnessFoldPreflightsProjectionBeforeWrite(t *testing.T) {
	repo := newTestRepo(t, "harness-fold-budget")
	plan := &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}}
	harness, err := buildAgentHarness(adoptedFacts("", "fixture", "framework", plan))
	if err != nil {
		t.Fatal(err)
	}
	s := &adoptSession{repoPath: repo, repoName: "fixture", arch: "framework", report: &AdoptReport{}, verification: plan, opts: AdoptOptions{Force: false}}
	// The kept harness carries the register block the run renders, so the run splices nothing and
	// only the folded text can make the harness invalid.
	block, err := s.registerBlock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if harness, _, err = compiler.SpliceRegisterBlock(harness, block); err != nil {
		t.Fatal(err)
	}
	path, claude := filepath.Join(repo, agentsFile), filepath.Join(repo, "CLAUDE.md")
	mustWrite(t, path, harness)
	// 192 lines keep the folded AGENTS.md inside the document budget the splice checks, while its
	// Cursor projection, frontmatter and header added, exceeds the projection budget.
	var authored strings.Builder
	for i := 0; i < 192; i++ {
		fmt.Fprintf(&authored, "authored rule %d\n", i)
	}
	mustWrite(t, claude, authored.String())
	if err := reconcileAgentHarness(context.Background(), s); err == nil || !strings.Contains(err.Error(), "context composition cannot produce valid projections") {
		t.Fatalf("oversized folded context not refused by the projection check: %v", err)
	}
	if mustRead(t, path) != harness || mustRead(t, claude) != authored.String() {
		t.Fatal("a refused fold changed AGENTS.md or CLAUDE.md")
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
	harness, err := buildAgentHarness(adoptedFacts("", s.repoName, s.arch, &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}}))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := mergeExistingAgentsContent(context.Background(), s, path, initial, initial, harness)
	if err != nil || !strings.Contains(merged, "Keep project instructions.") || mustRead(t, path) != merged {
		t.Fatalf("short context merge failed: %v", err)
	}
}

func TestValidateHarnessProjectionExactBudget(t *testing.T) {
	harness, err := buildAgentHarness(adoptedFacts("", "fixture", "framework", &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}}))
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
	if err := validateHarnessProjectionForClients(exact, nil); err != nil {
		t.Fatalf("exact projection budget rejected: %v", err)
	}
	if err := validateHarnessProjectionForClients(exact+"overflow\n", nil); err == nil {
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
	facts, err := s.harnessFacts(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	harness, err := buildAgentHarness(facts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolveAgentsContent(context.Background(), s, harness); err == nil {
		t.Fatal("dry-run oversized context accepted")
	}
	if got := mustRead(t, path); got != initial {
		t.Fatal("dry-run preflight changed canonical AGENTS.md")
	}
}
