package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// guardedMatrixWorkflow is a pull request workflow with an unconditional job and a matrix job
// behind the repository guard for literal, the shape of the engine's Platform Neutrality matrix.
func guardedMatrixWorkflow(literal string) string {
	return "on: pull_request\njobs:\n  unit:\n    name: Unit Tests\n    runs-on: ubuntu-latest\n" +
		"  matrix:\n    name: Leg (${{ matrix.name }})\n" +
		"    if: github.repository == (vars.PRAETOR_CANONICAL_REPOSITORY || '" + literal + "')\n" +
		"    runs-on: ubuntu-latest\n    strategy:\n      matrix:\n        include:\n" +
		"          - name: Linux\n          - name: Windows\n"
}

// guardedRemoteFixture is a sync fixture for acme/widgets whose manifest records source (empty
// for the canonical repository), carrying guardedMatrixWorkflow for the guard literal the
// manifest resolves to, a ruleset rendered from it and an origin on github.com/acme/widgets.
func guardedRemoteFixture(t *testing.T, source string) *auditFixture {
	t.Helper()
	f := newSyncValidationFixture(t)
	literal := "acme/widgets"
	if source != "" {
		literal = source
		manifest := readFixtureFile(t, f.dir, ".standards.yaml")
		withSource := strings.Replace(manifest, "  visibility: \"public\"\n", "  visibility: \"public\"\n  source: \""+source+"\"\n", 1)
		if withSource == manifest {
			t.Fatal("fixture manifest carries no visibility line to anchor repository.source on")
		}
		writeFixtureFile(t, f.dir, ".standards.yaml", withSource)
	}
	writeFixtureFile(t, f.dir, ".github/workflows/ci.yml", guardedMatrixWorkflow(literal))
	contexts, err := forge.RequiredStatusContexts(t.Context(), f.dir)
	if err != nil {
		t.Fatalf("RequiredStatusContexts: %v", err)
	}
	if err := synthesizeRuleset(f.dir, "main", config.DefaultPolicy().BranchProtection, contexts); err != nil {
		t.Fatal(err)
	}
	env := initGitFixture(t, f.dir)
	if out, err := runFixtureGit(t, f.dir, env, "remote", "add", "origin", "https://github.com/acme/widgets.git"); err != nil {
		t.Fatalf("remote add: %v (%s)", err, out)
	}
	return f
}

// runGuardedRemoteSync runs sync --remote for f against stub and returns the output, the ruleset
// the stub stored under id 1 and the committed ruleset.
func runGuardedRemoteSync(t *testing.T, f *auditFixture, stub *forgeStub) (out, remote, local string) {
	t.Helper()
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+srv.URL)
	if err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	return out, stub.storedRuleset(t, 1), readFixtureFile(t, f.dir, forge.RepositoryRulesetPath)
}

// A matrix job behind a repository guard is skipped before its matrix expands wherever the guard
// is false, so none of its per-leg checks is ever reported there (actions/runner#952).
//
// Positive: in the canonical repository the committed ruleset and the one written to GitHub both
// require every leg, and nothing is reported as left off. Negative: an operational fork, whose
// manifest resolves the guard to its source (#255), keeps the legs in its committed ruleset,
// which its audit validates, but GitHub is given only the checks its own runs report, and the
// command names the ones it left off. Boundary: the unconditional job is required in both.
func TestSync_Remote_RequiresOnlyChecksThatReportInTheRepository(t *testing.T) {
	legs := []string{"Leg (Linux)", "Leg (Windows)"}
	const unit = "Unit Tests"

	out, remote, local := runGuardedRemoteSync(t, guardedRemoteFixture(t, ""), &forgeStub{})
	for _, check := range append([]string{unit}, legs...) {
		requireRulesetCheck(t, "canonical GitHub ruleset", remote, check, true)
		requireRulesetCheck(t, "canonical committed ruleset", local, check, true)
	}
	if strings.Contains(out, "Not required on GitHub") || strings.Contains(out, "Still required on GitHub") {
		t.Errorf("canonical: sync reported checks left off:\n%s", out)
	}

	out, remote, local = runGuardedRemoteSync(t, guardedRemoteFixture(t, "upstream/widgets"), &forgeStub{})
	requireRulesetCheck(t, "fork GitHub ruleset", remote, unit, true)
	for _, leg := range legs {
		requireRulesetCheck(t, "fork GitHub ruleset", remote, leg, false)
		requireRulesetCheck(t, "fork committed ruleset", local, leg, true)
	}
	mustContain(t, out, "[INFO] Not required on GitHub for acme/widgets, because a repository guard skips their jobs there: Leg (Linux), Leg (Windows)")
}

// liveLegRuleset is a live praetor ruleset whose only rule requires the named checks, as an
// operational fork's ruleset looks after a sync that wrote every rendered check.
func liveLegRuleset(checks ...string) map[string]any {
	required := make([]any, 0, len(checks))
	for _, check := range checks {
		required = append(required, map[string]any{"context": check})
	}
	return map[string]any{
		"id": 1, "name": forge.RepositoryRulesetName, "target": "branch", "enforcement": "active",
		"rules": []any{map[string]any{"type": "required_status_checks", "parameters": map[string]any{
			"strict_required_status_checks_policy": true, "required_status_checks": required,
		}}},
	}
}

// Before sync --remote judged checks per repository, an operational fork's sync wrote every
// rendered check, the Platform Neutrality legs included. The merge never removes a live required
// check, so leaving a leg off does not unrequire it: sync reads the live ruleset back and warns
// about every left-off check it still requires, instead of claiming GitHub does not require it.
//
// Positive: both legs live, both named in the warning and no [INFO] line. Boundary: one leg
// live, so the warning names it and the [INFO] line names only the other. Negative: the merge
// keeps the live leg on GitHub (the operator removes it), and the committed ruleset is unchanged.
func TestSync_Remote_WarnsAboutLeftOffChecksTheLiveRulesetStillRequires(t *testing.T) {
	const warn = "[WARN] Still required on GitHub for acme/widgets, although a repository guard skips their jobs there, so every pull request waits for them: "

	out, remote, _ := runGuardedRemoteSync(t, guardedRemoteFixture(t, "upstream/widgets"),
		&forgeStub{rulesets: map[int]map[string]any{1: liveLegRuleset("Leg (Linux)", "Leg (Windows)")}})
	mustContain(t, out, warn+"Leg (Linux), Leg (Windows). sync --remote never removes a live required check",
		`remove them from ruleset "praetor-main-protection" by hand`)
	if strings.Contains(out, "Not required on GitHub") {
		t.Errorf("sync claims a live required leg is not required:\n%s", out)
	}
	requireRulesetCheck(t, "fork GitHub ruleset", remote, "Leg (Linux)", true)

	out, remote, local := runGuardedRemoteSync(t, guardedRemoteFixture(t, "upstream/widgets"),
		&forgeStub{rulesets: map[int]map[string]any{1: liveLegRuleset("Leg (Linux)")}})
	mustContain(t, out, warn+"Leg (Linux). ",
		"[INFO] Not required on GitHub for acme/widgets, because a repository guard skips their jobs there: Leg (Windows)\n")
	requireRulesetCheck(t, "fork GitHub ruleset", remote, "Leg (Linux)", true)
	requireRulesetCheck(t, "fork GitHub ruleset", remote, "Leg (Windows)", false)
	requireRulesetCheck(t, "fork GitHub ruleset", remote, "Unit Tests", true)
	requireRulesetCheck(t, "fork committed ruleset", local, "Leg (Windows)", true)
}

// requireRulesetCheck fails unless ruleset requires the status check exactly when want is true.
func requireRulesetCheck(t *testing.T, label, ruleset, check string, want bool) {
	t.Helper()
	required, err := forge.RulesetRequiresStatusContext([]byte(ruleset), check)
	if err != nil || required != want {
		t.Errorf("%s requires %q = %v (%v), want %v:\n%s", label, check, required, err, want, ruleset)
	}
}
