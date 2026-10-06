package harvester

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
)

// scanOneRepo scans a dev directory holding one repository with the given files and returns
// the report.
func scanOneRepo(t *testing.T, files ...string) *WorkstationReport {
	t.Helper()
	dev := t.TempDir()
	repo := filepath.Join(dev, "repo")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	for _, rel := range files {
		mustWriteFile(t, filepath.Join(repo, filepath.FromSlash(rel)), "rules\n")
	}
	rep, err := ScanLocalWorkstation(context.Background(), dev)
	if err != nil {
		t.Fatalf("ScanLocalWorkstation: %v", err)
	}
	return rep
}

// TestScanRecognizesEveryVendorRuleFile: a repository whose only agent instructions are one
// compiled vendor file is governed, whichever vendor it is. The hand-kept list reported
// Cursor-only and Copilot-only repositories as missing rules (BUG-840).
func TestScanRecognizesEveryVendorRuleFile(t *testing.T) {
	for _, target := range agentcontext.AllVendorTargets() {
		rep := scanOneRepo(t, target.Path)
		if len(rep.MissingRulesRepos) != 0 {
			t.Errorf("%s: repository reported missing rules", target.Path)
		}
		if want := filepath.Join("repo", filepath.FromSlash(target.Path)); !slices.Equal(rep.DiscoveredAgentDoc, []string{want}) {
			t.Errorf("%s: discovered %v, want [%s]", target.Path, rep.DiscoveredAgentDoc, want)
		}
	}
}

// TestScanIgnoresLookalikeRuleFiles: a file beside a vendor target is not the target, so a
// repository holding only such files still lacks rules.
func TestScanIgnoresLookalikeRuleFiles(t *testing.T) {
	rep := scanOneRepo(t, ".cursor/rules/other.mdc", ".github/instructions.md", "agents.txt")
	if !slices.Equal(rep.MissingRulesRepos, []string{"repo"}) || len(rep.DiscoveredAgentDoc) != 0 {
		t.Fatalf("lookalike files counted as rules: missing=%v discovered=%v", rep.MissingRulesRepos, rep.DiscoveredAgentDoc)
	}
}

// TestScanListsEveryRuleFileInRegistryOrder: with AGENTS.md and every vendor file present,
// discovery reports all of them once, canonical file first, then compile order. Discovery
// reads agentcontext.ContextFiles, the list compile-context writes, and keeps no copy (HISS-19).
func TestScanListsEveryRuleFileInRegistryOrder(t *testing.T) {
	files := agentcontext.ContextFiles()
	vendor := make([]string, 0, len(files))
	for _, target := range agentcontext.AllVendorTargets() {
		vendor = append(vendor, target.Path)
	}
	if len(files) == 0 || files[0] != "AGENTS.md" || !slices.Equal(files[1:], vendor) {
		t.Fatalf("ContextFiles = %v, want AGENTS.md then %v", files, vendor)
	}
	rep := scanOneRepo(t, files...)
	want := make([]string, 0, len(files))
	for _, rel := range files {
		want = append(want, filepath.Join("repo", filepath.FromSlash(rel)))
	}
	if !slices.Equal(rep.DiscoveredAgentDoc, want) || len(rep.MissingRulesRepos) != 0 {
		t.Fatalf("discovered %v, want %v (missing %v)", rep.DiscoveredAgentDoc, want, rep.MissingRulesRepos)
	}
}

// TestOnboardPlanNamesEveryTranspileTarget: the dry-run plan names each vendor section the
// transpiler writes, Codex included (BUG-840).
func TestOnboardPlanNamesEveryTranspileTarget(t *testing.T) {
	plan, err := OnboardRepository(context.Background(), t.TempDir(), true)
	if err != nil {
		t.Fatalf("OnboardRepository dry run: %v", err)
	}
	var action string
	for _, candidate := range plan.Actions {
		if strings.HasPrefix(candidate, "Transpile AGENTS.md -> ") {
			action = candidate
		}
	}
	for _, target := range agentcontext.AllVendorTargets() {
		if !strings.Contains(action, target.Section) {
			t.Errorf("transpile action %q omits %s", action, target.Section)
		}
	}
	if !strings.HasSuffix(action, "Codex") {
		t.Errorf("transpile action %q does not end with the last target", action)
	}
}

// onboardTranspileAction runs a dry-run onboarding of a repository whose manifest selects
// agent clients and returns its transpile action.
func onboardTranspileAction(t *testing.T, manifest string) (string, error) {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".standards.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := OnboardRepository(context.Background(), repo, true)
	if err != nil {
		return "", err
	}
	for _, candidate := range plan.Actions {
		if strings.HasPrefix(candidate, "Transpile AGENTS.md -> ") {
			return candidate, nil
		}
	}
	t.Fatalf("plan has no transpile action: %v", plan.Actions)
	return "", nil
}

// TestOnboardPlanFollowsAgentClients: the plan names the files the run compiles under the
// manifest's agent_clients. Positive: a subset names only its sections. Boundary: an empty
// selection names none. Negative: an unknown id fails the plan as it would fail the compile.
func TestOnboardPlanFollowsAgentClients(t *testing.T) {
	action, err := onboardTranspileAction(t, "agent_clients: [claude, codex]\n")
	if err != nil || action != "Transpile AGENTS.md -> Claude Code, Codex" {
		t.Fatalf("subset action = %q, %v", action, err)
	}
	action, err = onboardTranspileAction(t, "agent_clients: []\n")
	if err != nil || action != "Transpile AGENTS.md -> no vendor file (agent_clients selects none)" {
		t.Fatalf("empty selection action = %q, %v", action, err)
	}
	if _, err := onboardTranspileAction(t, "agent_clients: [claude, vim]\n"); err == nil ||
		!strings.Contains(err.Error(), "unknown agent client id(s): vim") {
		t.Fatalf("unknown client accepted: %v", err)
	}
}
