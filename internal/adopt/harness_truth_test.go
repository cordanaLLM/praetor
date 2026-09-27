package adopt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// truthPlan is a declared plan whose generated Makefile the claims below are checked against.
var truthPlan = &VerificationPlan{Status: verificationDeclared, Runtimes: []string{"go"},
	Build: [][]string{{"go", "build", "./..."}}, Test: [][]string{{"go", "test", "./..."}}}

// adoptedFacts is a full adoption's harness facts: both pipelines generated, lefthook's hooks
// installed, every vendor file selected.
func adoptedFacts(owner, name, arch string, plan *VerificationPlan) harnessFacts {
	return harnessFacts{owner: owner, name: name, arch: arch, plan: plan,
		pipelines: hisscatalog.AllPipelines, hooks: hooksLefthook, targets: agentcontext.AllVendorTargets()}
}

func renderTruthHarness(t *testing.T, owner, name string, plan *VerificationPlan) string {
	t.Helper()
	harness, err := buildAgentHarness(adoptedFacts(owner, name, "framework", plan))
	if err != nil {
		t.Fatal(err)
	}
	return harness
}

// invariantRows returns the harness table rows in order, keyed by the bold rule identifier.
func invariantRows(harness string) []string {
	var rows []string
	for _, line := range strings.Split(harness, "\n") {
		if strings.HasPrefix(line, "| **HISS-") {
			rows = append(rows, line)
		}
	}
	return rows
}

// TestHarnessTableListsEveryRegisteredInvariant: the table renders the whole HISS catalog in
// order, the six rules it used to omit included, and each row carries the adopted check the
// catalog states or says the rule is not enforced (BUG-779, BUG-804).
func TestHarnessTableListsEveryRegisteredInvariant(t *testing.T) {
	rows := invariantRows(renderTruthHarness(t, "", "fixture", truthPlan))
	rules := hisscatalog.Rules()
	if len(rows) != len(rules) {
		t.Fatalf("table has %d rows, catalog %d", len(rows), len(rules))
	}
	for i, rule := range rules {
		check, failure := rule.Adopted(hisscatalog.AllPipelines)
		want := "| **" + rule.ID + "** " + rule.Scope + " | " + rule.Directive + " | " + check + " | " + failure + " |"
		if rows[i] != want {
			t.Errorf("row %d:\n got %s\nwant %s", i, rows[i], want)
		}
	}
	for _, id := range []string{"HISS-05", "HISS-06", "HISS-11", "HISS-12", "HISS-13", "HISS-14"} {
		if !strings.Contains(strings.Join(rows, "\n"), "| **"+id+"** ") {
			t.Errorf("table omits %s", id)
		}
	}
}

// TestHarnessMakesNoUnbackedClaims: the generated verify-all never runs `gate run`, the only
// command that mints a receipt, and adoption generates no pull-request workflow, so the
// harness must promise neither a receipt for verify-all nor a bot re-check (BUG-804).
func TestHarnessMakesNoUnbackedClaims(t *testing.T) {
	if strings.Contains(buildMakefile(truthPlan), "gate run") {
		t.Fatal("fixture precondition: generated verify-all now mints a receipt; revisit harnessReceiptLine")
	}
	harness := renderTruthHarness(t, "", "fixture", truthPlan)
	for _, claim := range []string{"All pass -> Ed25519", "cordana-standards[bot]", "re-checks every pull request", "Immediate build failure", "Semgrep"} {
		if strings.Contains(harness, claim) {
			t.Errorf("harness still claims %q", claim)
		}
	}
	if !strings.Contains(harness, "receipt = `praetorctl gate run` only; generated `make verify-all` mints none.") {
		t.Error("harness does not say where a receipt comes from")
	}
	if line := harnessReceiptLine(0); strings.Contains(line, verifyCommand) || !strings.Contains(line, "`praetorctl gate run` only.") {
		t.Errorf("receipt line names a verify-all adoption did not generate: %s", line)
	}
	if row := invariantRows(harness)[0]; !strings.Contains(row, "Rust, Python direct recursion") || !strings.Contains(row, "new finding fails verify-all") {
		t.Errorf("HISS-01 row is not qualified by language: %s", row)
	}
}

// summaryFor renders the verify-all summary of one plan under the given pipelines.
func summaryFor(plan *VerificationPlan, pipelines hisscatalog.Pipeline, docsGate bool) string {
	return verificationSummary(harnessFacts{plan: plan, pipelines: pipelines, docsGate: docsGate})
}

// TestHarnessSummaryMatchesGeneratedVerifyAll: every command the declared summary attributes
// to verify-all is one the generated Makefile runs, docs-lint included exactly when the
// documentation gate adds it, so the summary cannot drift from the target.
func TestHarnessSummaryMatchesGeneratedVerifyAll(t *testing.T) {
	makefile := buildMakefile(truthPlan)
	summary := summaryFor(truthPlan, hisscatalog.AllPipelines, false)
	for _, command := range []string{"compile-context --verify", "caveman check --configured-sources", "audit"} {
		if !strings.Contains(summary, "`praetorctl "+command+"`") || !strings.Contains(makefile, command) {
			t.Errorf("summary and generated Makefile disagree on %q:\n%s", command, summary)
		}
	}
	docs := summaryFor(truthPlan, hisscatalog.AllPipelines, true)
	if strings.Contains(summary, "docs-lint") || !strings.Contains(docs, "`docs-lint`") ||
		!strings.Contains(DocumentationMakefileBlock(), "verify-all: docs-lint") {
		t.Errorf("docs-lint summary does not follow the documentation gate:\n%s\n%s", summary, docs)
	}
	preserved := summaryFor(&VerificationPlan{Status: verificationPreserved}, hisscatalog.PipelineLefthook, false)
	unavailable := summaryFor(&VerificationPlan{Status: verificationUnavailable}, hisscatalog.AllPipelines, false)
	declined := summaryFor(truthPlan, hisscatalog.PipelineLefthook, false)
	if strings.Contains(preserved, "praetorctl audit") || !strings.Contains(preserved, "repository-owned") ||
		!strings.Contains(unavailable, "fails until") ||
		strings.Contains(declined, "praetorctl audit") || !strings.Contains(declined, "`makefile` declined") {
		t.Errorf("summaries claim gates adoption cannot vouch for:\n%s\n%s\n%s", preserved, unavailable, declined)
	}
}

// TestHarnessNamesEveryProtectedContextFile: rule 3 names each file compile-context writes,
// not four of the six (BUG-840).
func TestHarnessNamesEveryProtectedContextFile(t *testing.T) {
	harness := renderTruthHarness(t, "", "fixture", truthPlan)
	for _, target := range agentcontext.AllVendorTargets() {
		if !strings.Contains(harness, "`"+target.Path+"`") {
			t.Errorf("rule 3 omits %s", target.Path)
		}
	}
	if strings.Contains(harness, ".cursor/rules/*.mdc") {
		t.Error("rule 3 still names a glob instead of the compiled Cursor file")
	}
}

// TestHarnessTitleNamesOwner covers the title's three identity sources: the origin remote,
// the manifest when no remote resolves, and the boundary where neither names an owner.
func TestHarnessTitleNamesOwner(t *testing.T) {
	if got := renderTruthHarness(t, "acme", "gadget", truthPlan); !strings.Contains(got, "\n# acme/gadget Agent Operating Harness\n") {
		t.Fatalf("owner dropped from title:\n%s", got[:120])
	}
	if got := renderTruthHarness(t, "", "gadget", truthPlan); !strings.Contains(got, "\n# gadget Agent Operating Harness\n") {
		t.Fatalf("unresolved owner title:\n%s", got[:120])
	}

	remote := identitySession(t, newTestRepo(t, "checkout"))
	if owner, name := remote.harnessIdentity(); owner != "acme" || name != "checkout" {
		t.Errorf("remote identity = %q/%q", owner, name)
	}

	repo := newTestRepo(t, "no-remote")
	mustWrite(t, filepath.Join(repo, ".git", "config"), "[core]\n\trepositoryformatversion = 0\n")
	unresolved := identitySession(t, repo)
	if owner, name := unresolved.harnessIdentity(); owner != "" || name != "no-remote" {
		t.Errorf("unresolved identity = %q/%q", owner, name)
	}
	unresolved.policy = &config.EffectivePolicy{Manifest: &config.Manifest{Repository: config.RepositoryMetadata{Owner: "declared", Name: "widget"}}}
	if owner, name := unresolved.harnessIdentity(); owner != "declared" || name != "widget" {
		t.Errorf("manifest identity = %q/%q", owner, name)
	}
	unresolved.policy.Manifest.Repository.Owner = ""
	if owner, name := unresolved.harnessIdentity(); owner != "" || name != "no-remote" {
		t.Errorf("half-declared manifest identity = %q/%q", owner, name)
	}
}

// TestAdoptForceKeepsOwnerAndDeclaredCommands reproduces the measured regression: --force over a
// repository with a custom verify-all keeps owner/name in the title and lists the commands the
// project markers declare, not only the preserved target (BUG-949).
func TestAdoptForceKeepsOwnerAndDeclaredCommands(t *testing.T) {
	repo := newTestRepo(t, "gadget")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/gadget\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(repo, makefileName), ".PHONY: verify-all\nverify-all:\n\tgo vet ./...\n")
	mustWrite(t, filepath.Join(repo, agentsFile), "# legacy/gadget Agent Operating Harness\n\n## Core Directives & Invariants\nold table\n\n---\n\n# Gadget rules\nKeep this.\n")
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Profile: "framework", Force: true})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if rep.Verification == nil || rep.Verification.Status != verificationPreserved {
		t.Fatalf("fixture precondition: custom verify-all must be preserved, got %+v", rep.Verification)
	}
	content := mustRead(t, filepath.Join(repo, agentsFile))
	for _, want := range []string{"# acme/gadget Agent Operating Harness", "'go' 'build' '-v' './...'", "'go' 'test' '-v' '-race' './...'", "repository-owned target", "# Gadget rules"} {
		if !strings.Contains(content, want) {
			t.Errorf("refreshed harness lacks %q", want)
		}
	}
}

// TestHarnessVariantsPassCavemanLint: the preserved, unavailable, declined and inactive-hook
// variants land in adopted AGENTS.md files too, so each lints clean.
func TestHarnessVariantsPassCavemanLint(t *testing.T) {
	variants := map[string]harnessFacts{
		"preserved": adoptedFacts("acme", "widget", "framework",
			&VerificationPlan{Status: verificationPreserved, Test: [][]string{{"make", "verify-all"}}, Declared: truthPlan.Test}),
		"unavailable": adoptedFacts("acme", "widget", "framework",
			&VerificationPlan{Status: verificationUnavailable, Reasons: []string{"A required build or test command is absent."}}),
		"declined": {owner: "acme", name: "widget", plan: truthPlan, hooks: hooksNone},
		"inactive": {owner: "acme", name: "widget", plan: truthPlan, pipelines: hisscatalog.PipelineVerifyAll, hooks: hooksInactive, docsGate: true},
	}
	for name, facts := range variants {
		harness, err := buildAgentHarness(facts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if report := lintHarness(harness); !report.Passed() {
			t.Errorf("%s harness fails the lint: %+v", name, report.Findings)
		}
	}
}

// TestHarnessEntrypointFollowsVerifyAll: the turn-end command block names `make verify-all`
// only where the repository has that target. Positive: a generated target is the entrypoint.
// Negative: a declined makefile step names the praetor gates instead, in the header and the
// footer. Boundary: a declined step over a preserved repository-owned target keeps it.
func TestHarnessEntrypointFollowsVerifyAll(t *testing.T) {
	const makeBlock = "```bash\nmake verify-all\n```"
	const gatesBlock = "```bash\npraetorctl compile-context --verify\npraetorctl caveman check --configured-sources\npraetorctl audit\n```"
	const footerVerify = "# Run all formatting, linting, and security gates\nmake verify-all\n"
	render := func(plan *VerificationPlan, pipelines hisscatalog.Pipeline) string {
		harness, err := buildAgentHarness(harnessFacts{name: "widget", plan: plan, pipelines: pipelines})
		if err != nil {
			t.Fatal(err)
		}
		return harness
	}
	if generated := render(truthPlan, hisscatalog.PipelineVerifyAll); !strings.Contains(generated, makeBlock) || !strings.Contains(generated, footerVerify) {
		t.Errorf("generated verify-all is not the entrypoint:\n%s", generated)
	}
	declined := render(truthPlan, 0)
	if strings.Contains(declined, makeBlock) || strings.Contains(declined, footerVerify) || !strings.Contains(declined, gatesBlock) ||
		!strings.Contains(declined, "No `make verify-all` target: `makefile` declined") {
		t.Errorf("declined makefile step still names make verify-all:\n%s", declined)
	}
	preserved := render(&VerificationPlan{Status: verificationPreserved, Declared: truthPlan.Test}, 0)
	if !strings.Contains(preserved, makeBlock) || strings.Contains(preserved, gatesBlock) {
		t.Errorf("preserved repository-owned verify-all is not the entrypoint:\n%s", preserved)
	}
}

// TestADRIndexCitesRegisteredRule: the scaffolded ADR index cites HISS-14 by the catalog's
// title, not the numbering lattice no rule states (BUG-779).
func TestADRIndexCitesRegisteredRule(t *testing.T) {
	rule, ok := hisscatalog.LookupRule("HISS-14")
	index := buildADRIndex()
	if !ok || !strings.Contains(index, rule.Reference()) || strings.Contains(index, "numbering lattice") {
		t.Fatalf("ADR index citation:\n%s", index)
	}
}
