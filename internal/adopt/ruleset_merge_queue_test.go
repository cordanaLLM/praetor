package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

const (
	queueCIWorkflow   = "name: CI\non:\n  pull_request:\n  merge_group:\njobs:\n  test:\n    name: Unit Tests\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n"
	queueDocsWorkflow = "name: Docs\non:\n  pull_request:\njobs:\n  docs:\n    name: Documentation Governance\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n"
)

func writeQueueWorkflows(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"ci.yml": queueCIWorkflow, "docs.yml": queueDocsWorkflow} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func queuePolicy() config.BranchProtectionPolicy {
	policy := signedPolicy()
	policy.MergeQueue = true
	return policy
}

// Positive: a queue-protected repository whose ruleset requires only the merge_group contexts
// passes the audit. Boundary: a repository without a queue keeps requiring every pull request
// context, the workflow without merge_group included.
func TestAuditBranchProtection_MergeQueue_Positive(t *testing.T) {
	root := t.TempDir()
	writeQueueWorkflows(t, root)
	writeAuditRuleset(t, root, renderAuditRuleset(t, queuePolicy(), []string{"Unit Tests"}))
	if _, err := AuditBranchProtectionWithPolicy(t.Context(), &config.Manifest{}, root, &config.ResolvedPolicy{BranchProtection: queuePolicy()}); err != nil {
		t.Fatalf("a queue ruleset requiring only merge_group contexts must pass: %v", err)
	}
	plain := t.TempDir()
	writeQueueWorkflows(t, plain)
	writeAuditRuleset(t, plain, renderAuditRuleset(t, signedPolicy(), []string{"Unit Tests", "Documentation Governance"}))
	if _, err := AuditBranchProtectionWithPolicy(t.Context(), &config.Manifest{}, plain, &config.ResolvedPolicy{BranchProtection: signedPolicy()}); err != nil {
		t.Fatalf("without a queue every pull request context stays required: %v", err)
	}
}

// Negative: a queue-protected branch whose ruleset requires a context of a workflow that lacks
// merge_group fails, naming the workflow path and the missing trigger. The same file is found
// when the policy declares no queue but the ruleset carries a merge_queue rule.
func TestAuditBranchProtection_MergeQueue_Negative(t *testing.T) {
	both := []string{"Unit Tests", "Documentation Governance"}
	cases := map[string]struct {
		policy  config.BranchProtectionPolicy
		ruleset config.BranchProtectionPolicy
	}{
		"declared queue":         {queuePolicy(), queuePolicy()},
		"queue rule in the file": {signedPolicy(), queuePolicy()},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeQueueWorkflows(t, root)
			writeAuditRuleset(t, root, renderAuditRuleset(t, test.ruleset, both))
			_, err := AuditBranchProtectionWithPolicy(t.Context(), &config.Manifest{}, root, &config.ResolvedPolicy{BranchProtection: test.policy})
			if err == nil {
				t.Fatal("a required context no merge group reports must fail the audit")
			}
			for _, want := range []string{"[FAIL]", ".github/workflows/docs.yml", "merge_group", "Documentation Governance"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q lacks %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "ci.yml") {
				t.Fatalf("the workflow that triggers on merge_group is no finding: %v", err)
			}
		})
	}
}

// The renderer and the audit agree: the ruleset RenderRulesetForRepository writes for a queue
// passes the audit it is judged by.
func TestAuditBranchProtection_MergeQueue_RenderedRulesetPasses(t *testing.T) {
	root := t.TempDir()
	writeQueueWorkflows(t, root)
	data, _, err := forge.RenderRulesetForRepository(t.Context(), root, queuePolicy(), nil)
	if err != nil {
		t.Fatal(err)
	}
	writeAuditRuleset(t, root, string(data))
	if _, err := AuditBranchProtectionWithPolicy(t.Context(), &config.Manifest{}, root, &config.ResolvedPolicy{BranchProtection: queuePolicy()}); err != nil {
		t.Fatalf("the rendered queue ruleset must pass the audit: %v", err)
	}
}
