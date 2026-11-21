package adopt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/compiler"
)

// TestMergeAgentsPreflightsProjectionBeforeWrite refuses an oversized loaded context before any write.
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
	requireHarnessBudgetFailure(t, reconcileAgentHarness(context.Background(), s))
	if got := mustRead(t, path); got != before {
		t.Fatal("oversized preflight changed canonical AGENTS.md")
	}
	for _, vendor := range vendorPaths {
		if got := mustRead(t, filepath.Join(repo, vendor)); got != vendorBefore[vendor] {
			t.Fatalf("oversized preflight changed vendor %s", vendor)
		}
	}
}

// TestForceHarnessPreflightPreservesRecognizedTail refuses oversized force refreshes without losing the tail.
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
	requireHarnessBudgetFailure(t, reconcileAgentHarness(context.Background(), s))
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
	// 192 lines keep the folded AGENTS.md inside the document budget the splice checks, while
	// the Cursor projection plus canonical text exceeds the client's loaded line budget.
	var authored strings.Builder
	for i := 0; i < 192; i++ {
		fmt.Fprintf(&authored, "authored rule %d\n", i)
	}
	mustWrite(t, claude, authored.String())
	requireHarnessBudgetFailure(t, reconcileAgentHarness(context.Background(), s))
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

// TestValidateHarnessProjectionExactBudget accepts exactly the loaded line budget and refuses one more line.
func TestValidateHarnessProjectionExactBudget(t *testing.T) {
	harness, err := buildAgentHarness(adoptedFacts("", "fixture", "framework", &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}}))
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSuffix(harness, "\n") + "\n"
	initial, err := compiler.NewTranspiler().CompileContent(base)
	if err != nil {
		t.Fatal(err)
	}
	fullContent := map[string]bool{}
	for _, target := range agentcontext.AllVendorTargets() {
		fullContent[target.Path] = target.FullContent
	}
	maxLines := 0
	for _, file := range initial.Files {
		// The registry decides what a client loads: a full-content projection alone, a pointer
		// with the canonical file. Import substitutions replace the pointer line.
		loaded := file.LineCount
		if !fullContent[file.RelativePath] {
			loaded += strings.Count(base, "\n") + 1
		}
		if file.RelativePath == "CLAUDE.md" || file.RelativePath == ".gemini/GEMINI.md" {
			loaded--
		}
		maxLines = max(maxLines, loaded)
	}
	exact := base + strings.Repeat("x\n", compiler.MaxLineBudget-maxLines)
	if err := validateHarnessProjectionForClients(exact, nil); err != nil {
		t.Fatalf("exact loaded budget rejected: %v", err)
	}
	requireHarnessBudgetFailure(t, validateHarnessProjectionForClients(exact+"overflow\n", nil))
}

// requireHarnessBudgetFailure requires the client path, measured lines and cap in a preflight refusal.
func requireHarnessBudgetFailure(t *testing.T, err error) {
	t.Helper()
	for _, want := range []string{"context composition cannot produce valid projections", "max line budget (", " > 300)"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("loaded context refusal missing %q: %v", want, err)
		}
	}
	for _, path := range agentcontext.VendorTargetPaths() {
		if strings.Contains(err.Error(), "target file "+path+" ") {
			return
		}
	}
	t.Fatalf("loaded context refusal lacks a client file: %v", err)
}

// TestResolveAgentsContentPreflightHonorsDryRun refuses oversized dry runs without changing canonical text.
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
	_, err = resolveAgentsContent(context.Background(), s, harness)
	requireHarnessBudgetFailure(t, err)
	if got := mustRead(t, path); got != initial {
		t.Fatal("dry-run preflight changed canonical AGENTS.md")
	}
}
