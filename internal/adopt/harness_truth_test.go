package adopt

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/compiler"
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
		pipelines: hisscatalog.AllPipelines, hooks: hooksLefthook, targets: agentcontext.AllVendorTargets(),
		hiss: repositoryFacts(plan, 0)}
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
	goFacts := repositoryFacts(truthPlan, 0)
	for i, rule := range rules {
		check, failure := rule.AdoptedFor(hisscatalog.AllPipelines, goFacts)
		want := "| **" + rule.ID + "** " + rule.Scope + " | " + rule.AdoptedDirective(goFacts) +
			" | " + check + " | " + failure + " |"
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
	if !strings.Contains(harness, "Signed Ed25519 Exit-0 receipt only from `praetorctl gate run`; report no receipt it did not mint.") {
		t.Error("harness does not say where a receipt comes from")
	}
	if strings.Contains(harnessReceiptLine, verifyCommand) {
		t.Errorf("receipt line ties a receipt to verify-all: %s", harnessReceiptLine)
	}
	if row := invariantRows(harness)[0]; !strings.Contains(row, "Rust, Python direct recursion") || !strings.Contains(row, "new finding fails verify-all") {
		t.Errorf("HISS-01 row is not qualified by language: %s", row)
	}
}

// summaryFor renders the verify-all summary of one plan under the given pipelines.
func summaryFor(plan *VerificationPlan, pipelines hisscatalog.Pipeline) string {
	return verificationSummary(harnessFacts{plan: plan, pipelines: pipelines})
}

// TestHarnessSummaryDescribesVerifyAllAsRepositoryGate: the harness calls verify-all the
// repository's own gate and points at the Makefile, never listing the steps the target runs or
// tying a receipt to it (#503). Positive: the generated and the preserved target read as the
// repository's gate. Negative: no summary, and no whole harness, names a step of the generated
// target or promises a receipt. Boundary: a declined makefile step and an unavailable plan keep
// their own sentences.
func TestHarnessSummaryDescribesVerifyAllAsRepositoryGate(t *testing.T) {
	generated := summaryFor(truthPlan, hisscatalog.AllPipelines)
	preserved := summaryFor(&VerificationPlan{Status: verificationPreserved}, hisscatalog.PipelineLefthook)
	if !strings.Contains(generated, "`make verify-all` = repository gate. Steps live in `Makefile`") ||
		!strings.Contains(preserved, "`make verify-all` = repository-owned gate") || !strings.Contains(preserved, "Steps live in `Makefile`") {
		t.Errorf("verify-all not described as the repository gate:\n%s\n%s", generated, preserved)
	}
	makefile := buildMakefile(truthPlan) + DocumentationMakefileBlock()
	declined := summaryFor(truthPlan, hisscatalog.PipelineLefthook)
	unavailable := summaryFor(&VerificationPlan{Status: verificationUnavailable}, hisscatalog.AllPipelines)
	harness := renderTruthHarness(t, "", "fixture", truthPlan)
	for _, step := range []string{"compile-context --verify", "caveman check --configured-sources", "praetorctl audit", "docs-lint"} {
		if !strings.Contains(makefile, strings.TrimPrefix(step, "praetorctl ")) {
			t.Fatalf("fixture precondition: generated Makefile no longer runs %q", step)
		}
		for name, summary := range map[string]string{"generated": generated, "preserved": preserved, "unavailable": unavailable} {
			if strings.Contains(summary, step) || strings.Contains(summary, "receipt") {
				t.Errorf("%s summary restates the target (%q):\n%s", name, step, summary)
			}
		}
	}
	for _, claim := range []string{"All pass -> Ed25519", "Exit-0 receipt.", "mints none", "Run all formatting, linting, and security gates"} {
		if strings.Contains(harness, claim) {
			t.Errorf("harness still claims %q", claim)
		}
	}
	if !strings.Contains(declined, "No `make verify-all` target: `makefile` declined") || !strings.Contains(unavailable, "fails until") {
		t.Errorf("declined and unavailable summaries:\n%s\n%s", declined, unavailable)
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

// rule3Files returns the files rule 3 forbids editing by hand, in the order it names them.
func rule3Files(harness string) []string {
	for _, line := range strings.Split(harness, "\n") {
		if !strings.HasPrefix(line, "3. **Context transpiler first.** Never edit ") {
			continue
		}
		list, _, _ := strings.Cut(strings.TrimPrefix(line, "3. **Context transpiler first.** Never edit "), " manually.")
		var files []string
		for _, name := range strings.Split(list, ", ") {
			files = append(files, strings.Trim(name, "`"))
		}
		return files
	}
	return nil
}

// TestHarnessRule3NamesExactlyTheCompiledFiles: rule 3's never-hand-edit list is the set
// compile-context writes for the same agent_clients selection, taken from one source, so
// .gemini/GEMINI.md and .codex/rules.md are named whenever they are written (#503). Positive:
// every client. Negative: a selection names only its own files. Boundary: an empty selection
// writes and names none.
func TestHarnessRule3NamesExactlyTheCompiledFiles(t *testing.T) {
	for _, clients := range [][]string{nil, {"gemini", "codex"}, {}} {
		targets, err := agentcontext.VendorTargets(clients)
		if err != nil {
			t.Fatal(err)
		}
		facts := adoptedFacts("", "fixture", "framework", truthPlan)
		facts.targets = targets
		harness, err := buildAgentHarness(facts)
		if err != nil {
			t.Fatal(err)
		}
		tr := compiler.NewTranspiler()
		tr.Clients = clients
		res, err := tr.CompileContent(harness)
		if err != nil {
			t.Fatalf("clients %q: compile: %v", clients, err)
		}
		written := make([]string, 0, len(res.Files))
		for _, file := range res.Files {
			written = append(written, file.RelativePath)
		}
		if named := rule3Files(harness); !slices.Equal(named, written) {
			t.Errorf("clients %q: rule 3 names %q, compile-context writes %q", clients, named, written)
		}
	}
	all := rule3Files(renderTruthHarness(t, "", "fixture", truthPlan))
	for _, want := range []string{".gemini/GEMINI.md", ".codex/rules.md"} {
		if !slices.Contains(all, want) {
			t.Errorf("rule 3 omits %s: %q", want, all)
		}
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
	for _, want := range []string{"# acme/gadget Agent Operating Harness", "'go' 'build' '-v' './...'", "'go' 'test' '-v' '-race' './...'", "repository-owned gate", "# Gadget rules"} {
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
		"inactive": {owner: "acme", name: "widget", plan: truthPlan, pipelines: hisscatalog.PipelineVerifyAll, hooks: hooksInactive},
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
	const footerVerify = "# Repository gate; steps live in Makefile\nmake verify-all\n"
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
