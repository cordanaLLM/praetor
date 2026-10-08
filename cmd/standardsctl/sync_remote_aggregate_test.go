package main

import (
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// aggregateCI is path-filtered CI whose merge gate needs the planner, the lane and a matrix job,
// runs on every run and fails when any of them failed or was cancelled.
const aggregateCI = "on: pull_request\njobs:\n" +
	"  plan:\n    name: CI impact plan\n    runs-on: ubuntu-latest\n" +
	"  go:\n    name: Go lane\n    needs: plan\n    if: needs.plan.outputs.go == 'true'\n    runs-on: ubuntu-latest\n" +
	"  test:\n    name: Test (${{ matrix.os }})\n    needs: go\n    runs-on: ${{ matrix.os }}\n" +
	"    strategy:\n      matrix:\n        os: [ubuntu-latest, macos-latest]\n" +
	"  gate:\n    name: Merge gate\n    needs: [plan, go, test]\n    if: always()\n    runs-on: ubuntu-latest\n" +
	"    steps:\n      - if: contains(needs.*.result, 'failure') || contains(needs.*.result, 'cancelled')\n        run: exit 1\n"

// leafChecks are the checks a ruleset rendered before the gate was proven required.
var leafChecks = []string{"CI impact plan", "Test (ubuntu-latest)", "Test (macos-latest)"}

// aggregateRemoteFixture is a sync fixture for acme/widgets carrying aggregateCI and the ruleset
// rendered from it, with its origin on github.com/acme/widgets and a stub forge serving it.
func aggregateRemoteFixture(t *testing.T, stub *forgeStub) (*auditFixture, string) {
	t.Helper()
	f := newSyncValidationFixture(t)
	writeFixtureFile(t, f.dir, ".github/workflows/ci.yml", aggregateCI)
	contexts, err := forge.RequiredStatusContexts(t.Context(), f.dir)
	if err != nil || !slices.Equal(contexts, []string{"Merge gate"}) {
		t.Fatalf("RequiredStatusContexts = %v, %v; want the merge gate alone", contexts, err)
	}
	if err := synthesizeRuleset(f.dir, "main", config.DefaultPolicy().BranchProtection, contexts); err != nil {
		t.Fatal(err)
	}
	f.gitEnv = initGitFixture(t, f.dir)
	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "remote", "add", "origin", "https://github.com/acme/widgets.git"); err != nil {
		t.Fatalf("remote add: %v (%s)", err, out)
	}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	return f, srv.URL
}

// Positive (#76): sync --remote creates a ruleset that requires the proven merge gate alone, and
// plan --remote compares the live branch protection with that same list, so it reports no drift.
// Had plan judged the leaves, the ruleset sync created would lack them and plan would drift.
func TestSync_Remote_RequiresTheProvenAggregateAlone(t *testing.T) {
	stub := &forgeStub{}
	f, endpoint := aggregateRemoteFixture(t, stub)
	if out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint); err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	remote := stub.storedRuleset(t, 1)
	requireRulesetCheck(t, "GitHub ruleset", remote, "Merge gate", true)
	for _, leaf := range leafChecks {
		requireRulesetCheck(t, "GitHub ruleset", remote, leaf, false)
	}
	out, err := runPlanCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint)
	if err != nil {
		t.Fatalf("plan --remote: %v\n%s", err, out)
	}
	mustContain(t, out, "Status: GitHub enforces every declared branch protection property.")
}

// Boundary (migration): a live ruleset an earlier sync wrote with the leaf checks keeps them,
// because sync --remote never removes a live required check, and gains the merge gate; the
// committed ruleset requires the gate alone. The operator removes the leaves by hand.
func TestSync_Remote_KeepsLiveLeafChecksAndAddsTheAggregate(t *testing.T) {
	stub := &forgeStub{rulesets: map[int]map[string]any{1: liveLegRuleset(leafChecks...)}}
	f, endpoint := aggregateRemoteFixture(t, stub)
	if out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint); err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	remote := stub.storedRuleset(t, 1)
	local := readFixtureFile(t, f.dir, forge.RepositoryRulesetPath)
	requireRulesetCheck(t, "GitHub ruleset", remote, "Merge gate", true)
	requireRulesetCheck(t, "committed ruleset", local, "Merge gate", true)
	for _, leaf := range leafChecks {
		requireRulesetCheck(t, "GitHub ruleset", remote, leaf, true)
		requireRulesetCheck(t, "committed ruleset", local, leaf, false)
	}
}
