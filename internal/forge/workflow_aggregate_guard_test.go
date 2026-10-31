// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

const shardExitSteps = "    steps:\n      - name: Fail when a shard failed, was cancelled or was skipped\n" +
	"        if: contains(needs.*.result, 'failure') || contains(needs.*.result, 'cancelled') || contains(needs.*.result, 'skipped')\n" +
	"        run: |\n          echo \"::error::a shard failed, was cancelled or was skipped\"\n          exit 1\n"

// guardedShardWorkflow builds a pull_request workflow with a matrix shard job that begins with
// the draft step and an aggregate windows job that needs it.
func guardedShardWorkflow(shardIf, gateIf, steps string) string {
	shardCond := ""
	if shardIf != "" {
		shardCond = "    if: \"" + shardIf + "\"\n"
	}
	gateCond := ""
	if gateIf != "" {
		gateCond = "    if: \"" + gateIf + "\"\n"
	}
	return readyTypes +
		"  windows-shard:\n    name: Windows shard (${{ matrix.shard }})\n" + shardCond +
		"    runs-on: windows-2025\n    strategy:\n      matrix:\n        shard: [1, 2]\n    steps:\n" +
		ghworkflow.HostedGateDraftStep + gateRunStep +
		"  windows:\n    name: Platform Neutrality (Windows)\n    needs: windows-shard\n" + gateCond +
		"    runs-on: ubuntu-26.04\n" + steps
}

// Positive: an aggregate under <renovate skip> && (<repo guard>) && always(), or (<repo guard>) &&
// always() without the Renovate skip, covers a need carrying the same guard and reports only in
// the canonical repository.
func TestRequiredStatusContexts_Positive_GuardedAggregateCoversMatchingNeed(t *testing.T) {
	guard := repositoryGuard("acme/engine") + " || " + portabilityVariable
	cases := []struct {
		name, shardIf, gateIf string
	}{
		{
			"renovate skip and repository guard",
			renovateBranchSkip + " && (" + guard + ")",
			renovateBranchSkip + " && (" + guard + ") && always()",
		},
		{
			"repository guard without renovate skip",
			guard,
			"(" + guard + ") && always()",
		},
		{
			"single guard with renovate skip",
			renovateBranchSkip + " && " + repositoryGuard("acme/engine"),
			renovateBranchSkip + " && (" + repositoryGuard("acme/engine") + ") && always()",
		},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			doc := []byte(guardedShardWorkflow(tc.shardIf, tc.gateIf, shardExitSteps))
			got, err := workflowContextsIn(doc, "acme/engine")
			if err != nil || !slices.Equal(got, []string{"Platform Neutrality (Windows)"}) {
				t.Fatalf("canonical contexts = %v, %v; want [Platform Neutrality (Windows)]", got, err)
			}
			for _, other := range []string{"operator/engine", ""} {
				if fork, err := workflowContextsIn(doc, other); err != nil || len(fork) != 0 {
					t.Fatalf("identity %q: contexts = %v, %v; want none", other, fork, err)
				}
			}
		})
	}
	cond := "(" + guard + ") && always()"
	if !guardHoldsInRepository(cond, "acme/engine") || guardHoldsInRepository(cond, "operator/engine") || guardHoldsInRepository(cond, "") {
		t.Fatalf("guardHoldsInRepository(%q) did not hold only for acme/engine", cond)
	}
}

// Negative: a guarded aggregate does not cover a need without the guard or with a different guard,
// and an aggregate condition without always() or with && or !( inside its guard is refused.
func TestRequiredStatusContexts_Negative_GuardedAggregateRefusesMismatchedNeedOrCondition(t *testing.T) {
	guard := repositoryGuard("acme/engine") + " || " + portabilityVariable
	gateIf := renovateBranchSkip + " && (" + guard + ") && always()"
	bothShardsAndGate := []string{"Platform Neutrality (Windows)", "Windows shard (1)", "Windows shard (2)"}
	shardsOnly := []string{"Windows shard (1)", "Windows shard (2)"}
	cases := []struct {
		name, shardIf, gateIf string
		wantCanonical         []string
	}{
		{"need without guard", renovateBranchSkip, gateIf, bothShardsAndGate},
		{"unconditional need", "", "(" + guard + ") && always()", bothShardsAndGate},
		{"need missing renovate skip", guard, gateIf, bothShardsAndGate},
		{"need with narrower guard", renovateBranchSkip + " && " + repositoryGuard("acme/engine"), gateIf, bothShardsAndGate},
		{
			"need with another repo guard",
			renovateBranchSkip + " && " + repositoryGuard("acme/engine"),
			renovateBranchSkip + " && (" + repositoryGuard("other/engine") + ") && always()",
			shardsOnly,
		},
		{"aggregate without always", renovateBranchSkip + " && (" + guard + ")", renovateBranchSkip + " && (" + guard + ")", bothShardsAndGate},
		{
			"guard containing conjunction",
			repositoryGuard("acme/engine") + " && github.ref == 'refs/heads/main'",
			"(" + repositoryGuard("acme/engine") + " && github.ref == 'refs/heads/main') && always()",
			nil,
		},
		{
			"guard containing negation",
			"!(" + repositoryGuard("acme/engine") + ")",
			"(!(" + repositoryGuard("acme/engine") + ")) && always()",
			nil,
		},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			spec, err := ghworkflow.Parse([]byte(guardedShardWorkflow(tc.shardIf, tc.gateIf, shardExitSteps)))
			if err != nil {
				t.Fatal(err)
			}
			job := spec.Jobs["windows"]
			if covered := provenAggregateNeeds(&spec, &job); len(covered) != 0 {
				t.Fatalf("provenAggregateNeeds = %v, want none", covered)
			}
			got, err := workflowContextsIn([]byte(guardedShardWorkflow(tc.shardIf, tc.gateIf, shardExitSteps)), "acme/engine")
			if err != nil || !slices.Equal(got, tc.wantCanonical) {
				t.Fatalf("contexts = %v, %v; want %v", got, err, tc.wantCanonical)
			}
		})
	}
}

// Boundary: whitespace variants around the parenthesised guard and && always() are accepted;
// ${{ }}-wrapped always() stays accepted when unguarded while wrapped guarded forms are refused.
func TestAggregateCondition_Boundary_SpellingsAndWithoutTrailingAlways(t *testing.T) {
	guard := repositoryGuard("acme/engine") + " || " + portabilityVariable
	spacedGate := "  " + renovateBranchSkip + " && (  " + guard + "  )   && always()  "
	spacedShard := "  " + renovateBranchSkip + " && (  " + guard + "  )  "
	got, err := workflowContextsIn([]byte(guardedShardWorkflow(spacedShard, spacedGate, shardExitSteps)), "acme/engine")
	if err != nil || !slices.Equal(got, []string{"Platform Neutrality (Windows)"}) {
		t.Fatalf("spaced guarded aggregate contexts = %v, %v; want [Platform Neutrality (Windows)]", got, err)
	}
	if got := withoutTrailingAlways("  ( " + guard + " )  && always()  "); got != guard {
		t.Fatalf("withoutTrailingAlways spaced = %q, want %q", got, guard)
	}
	for _, unchanged := range []string{guard, guard + " && always()", "(a) && (b) && always()", "(" + guard + ") && !cancelled()"} {
		if got := withoutTrailingAlways(unchanged); got != unchanged {
			t.Fatalf("withoutTrailingAlways(%q) = %q, want unchanged", unchanged, got)
		}
	}
	for _, wrapped := range []string{"${{ always() }}", "${{ always( ) }}", renovateBranchSkip + " && ${{ always( ) }}"} {
		if parsed, ok := aggregateCondition(wrapped); !ok || parsed.repository != "" {
			t.Fatalf("aggregateCondition(%q) = %+v, %v; want unguarded always", wrapped, parsed, ok)
		}
	}
	for _, refused := range []string{"${{ (" + guard + ") && always() }}", "(" + guard + ") && ${{ always() }}"} {
		if parsed, ok := aggregateCondition(refused); ok {
			t.Fatalf("aggregateCondition(%q) = %+v, true; want false", refused, parsed)
		}
	}
}

// Positive and negative: a proven aggregate whose needs are all covered and each begin with the
// draft step is draft-proven; if any need lacks the draft step or is not covered, it is not.
func TestAuditWorkflowTriggers_DraftStepAggregate(t *testing.T) {
	guard := repositoryGuard("acme/engine") + " || " + portabilityVariable
	shardIf := renovateBranchSkip + " && (" + guard + ")"
	gateIf := renovateBranchSkip + " && (" + guard + ") && always()"
	positive := guardedShardWorkflow(shardIf, gateIf, shardExitSteps)
	if out := auditTriggers(t, triggerRepository(t, map[string]string{"portability.yml": positive})); !strings.HasPrefix(out, "[PASS]") {
		t.Fatalf("draft-step aggregate did not pass:\n%s", out)
	}
	noDraftNeed := strings.Replace(positive, ghworkflow.HostedGateDraftStep, "", 1)
	if out := auditTriggers(t, triggerRepository(t, map[string]string{"portability.yml": noDraftNeed})); !strings.Contains(out, "pull_request job windows-shard runs on a draft") ||
		!strings.Contains(out, "pull_request job windows runs on a draft") {
		t.Fatalf("aggregate over a need without the draft step was not reported:\n%s", out)
	}
	partial := strings.Replace(positive, "  windows:\n    name: Platform Neutrality (Windows)\n    needs: windows-shard\n",
		"  extra-shard:\n    runs-on: windows-2025\n    steps:\n"+ghworkflow.HostedGateDraftStep+gateRunStep+
			"  windows:\n    name: Platform Neutrality (Windows)\n    needs: [windows-shard, extra-shard]\n", 1)
	spec, err := ghworkflow.Parse([]byte(partial))
	if err != nil {
		t.Fatal(err)
	}
	if provenDraftStepAggregate(&spec, "windows") {
		t.Fatal("aggregate covering only some of its needs was marked draft-proven")
	}
	if out := auditTriggers(t, triggerRepository(t, map[string]string{"portability.yml": partial})); !strings.Contains(out, "pull_request job windows may be skipped on a draft") {
		t.Fatalf("partially covering aggregate was not reported:\n%s", out)
	}
}
