package forge

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// platformNeutralityLegs are the per-leg contexts of the portability matrix: the checks every pull
// request of the canonical repository waits for (operator decision 2026-09-28, after all three
// legs passed on main).
var platformNeutralityLegs = []string{
	"Platform Neutrality (Linux)", "Platform Neutrality (macOS)", "Platform Neutrality (Windows)",
}

// securityScanContext is the security job's check. Its guard is joined to the schedule-leg term,
// so it reports on every pull request of every repository.
const securityScanContext = "Go Vulnerability & AST Security Scan"

// Positive: the canonical repository's rendered ruleset and the committed one both require every
// Platform Neutrality leg, and the stated-reason skip job is required in neither. A guard or name
// change that drops a leg fails here, not only in the audit.
func TestCanonicalRulesetRequiresEveryPlatformNeutralityLeg(t *testing.T) {
	policy, err := RepositoryBranchPolicy(t.Context(), engineRoot)
	if err != nil {
		t.Fatalf("RepositoryBranchPolicy: %v", err)
	}
	rendered, contexts, err := RenderRulesetForRepository(t.Context(), engineRoot, policy, nil)
	if err != nil {
		t.Fatalf("RenderRulesetForRepository: %v", err)
	}
	committed, err := os.ReadFile(filepath.Join(engineRoot, filepath.FromSlash(RepositoryRulesetPath)))
	if err != nil {
		t.Fatalf("read committed ruleset: %v", err)
	}
	for name, ruleset := range map[string][]byte{"rendered": rendered, "committed": committed} {
		for _, leg := range platformNeutralityLegs {
			if required, err := RulesetRequiresStatusContext(ruleset, leg); err != nil || !required {
				t.Errorf("%s ruleset requires %q = %v, %v", name, leg, required, err)
			}
		}
	}
	for _, context := range contexts {
		if strings.Contains(context, "skipped") {
			t.Errorf("the stated-reason skip job became a required context: %q", context)
		}
	}
}

// Positive, negative and boundary coverage for RequiredStatusContextsIn: the canonical repository
// keeps every leg; an operational fork, whose manifest resolves the guard to the canonical
// identity (#255), keeps the legs in its checked-in rendering but not on its own forge, where the
// matrix is skipped before it expands; the security job, which runs on every pull request
// everywhere, stays required in both; a malformed repository or a nil context is refused.
func TestRequiredStatusContextsIn(t *testing.T) {
	canonical, err := RequiredStatusContextsIn(t.Context(), engineRoot, "cordanaLLM/praetor")
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	fromManifest, err := RequiredStatusContexts(t.Context(), engineRoot)
	if err != nil {
		t.Fatalf("RequiredStatusContexts: %v", err)
	}
	if !slices.Equal(canonical, fromManifest) {
		t.Errorf("canonical forge contexts %v differ from the rendered ones %v", canonical, fromManifest)
	}
	requireContexts(t, "canonical", canonical, append([]string{securityScanContext}, platformNeutralityLegs...), nil)

	fork := overlaidEngineRoot(t)
	forkFile, err := RequiredStatusContexts(t.Context(), fork)
	if err != nil {
		t.Fatalf("fork rendering: %v", err)
	}
	requireContexts(t, "fork rendering", forkFile, platformNeutralityLegs, nil)
	forkForge, err := RequiredStatusContextsIn(t.Context(), fork, "example-owner/praetor")
	if err != nil {
		t.Fatalf("fork forge: %v", err)
	}
	requireContexts(t, "fork forge", forkForge, []string{securityScanContext}, platformNeutralityLegs)
	if len(forkForge) != len(forkFile)-len(platformNeutralityLegs) {
		t.Errorf("fork forge contexts %v, want the rendering %v without the legs alone", forkForge, forkFile)
	}

	for _, repository := range []string{"", "praetor", "cordanaLLM/", "/praetor", "cordanaLLM/praetor/extra"} {
		if got, err := RequiredStatusContextsIn(t.Context(), engineRoot, repository); err == nil || got != nil {
			t.Errorf("repository %q: %v, %v; want a refusal", repository, got, err)
		}
	}
	var missing context.Context
	if _, err := RequiredStatusContextsIn(missing, engineRoot, "cordanaLLM/praetor"); err == nil {
		t.Error("a nil context was accepted")
	}
}

// requireContexts fails when contexts lacks a name of want or holds a name of absent.
func requireContexts(t *testing.T, label string, contexts, want, absent []string) {
	t.Helper()
	for _, name := range want {
		if !slices.Contains(contexts, name) {
			t.Errorf("%s: missing required context %q in %v", label, name, contexts)
		}
	}
	for _, name := range absent {
		if slices.Contains(contexts, name) {
			t.Errorf("%s: context %q is required although no run there reports it: %v", label, name, contexts)
		}
	}
}

// Positive, negative and boundary coverage for the schedule-leg term: alone or in a disjunction
// it holds on every pull request run; joined by a conjunction, negated or spelled as the opposite
// comparison it does not; an unrelated condition does not.
func TestHoldsOnEveryPullRequestRun(t *testing.T) {
	guard := repositoryGuard("acme/engine")
	cases := []struct {
		condition string
		want      bool
	}{
		{pullRequestRunTerm, true},
		{scheduleLegPrefix + guard, true},
		{"${{ " + scheduleLegPrefix + guard + " }}", true},
		{"", false},
		{guard, false},
		{"github.event_name == 'schedule'", false},
		{pullRequestRunTerm + " && " + guard, false},
		{"!(" + pullRequestRunTerm + ")", false},
	}
	for _, tc := range cases {
		if got := holdsOnEveryPullRequestRun(tc.condition); got != tc.want {
			t.Errorf("holdsOnEveryPullRequestRun(%q) = %v, want %v", tc.condition, got, tc.want)
		}
	}
	workflow := []byte("on: pull_request\njobs:\n  scan:\n    name: Scan\n    if: " + scheduleLegPrefix + guard + "\n")
	for _, identity := range []string{"", "acme/engine", forkIdentity} {
		if contexts, err := workflowContextsIn(workflow, identity); err != nil || !slices.Equal(contexts, []string{"Scan"}) {
			t.Errorf("identity %q: contexts %v, %v; want the schedule-guarded job required", identity, contexts, err)
		}
	}
}
