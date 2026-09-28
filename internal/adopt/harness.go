package adopt

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

const (
	// harnessEndMarker terminates every harness praetor writes so that a later
	// --force update can locate the boundary to repository-specific instructions, and the
	// context gate can judge the text below it on its own (compiler.CheckContextText).
	harnessEndMarker = compiler.HarnessEndMarker
	// harnessSeparator separates the harness from repository-specific instructions.
	harnessSeparator = "\n---\n"
	// harnessFooterHeading starts the last section of the generated harness.
	harnessFooterHeading = "## Primary Verification Commands"
	// verifyCommand is the single verification entrypoint advertised everywhere.
	verifyCommand = "make verify-all"
	// codeFence delimits the shell block of the harness footer.
	codeFence = "```"
)

// harnessLintScopeEnd closes the harness's markdownlint scope. The harness carries a table
// and rule lines no 80-column wrap can hold, so it disables MD013 at its first line and
// enables it again here, before the repository's own instructions start; those follow a
// second H1, so MD025 is disabled from here on and only there. A disable on the first line
// alone used to silence both rules for everything the repository wrote below the harness
// (BUG-806).
const harnessLintScopeEnd = "<!-- markdownlint-enable MD013 -->\n<!-- markdownlint-disable MD025 -->\n"

// agentHarnessTemplate opens the harness. It is agent-only text, so it is written in the
// internal register and passes the caveman lint that compile-context --verify and audit run
// over the whole AGENTS.md (TestHarnessPassesCavemanLint). The title names owner/name when the
// repository identity resolves, so a --force refresh keeps the owner an earlier harness named
// (BUG-949). Without a verify-all target (VerifyCmd empty) the turn ends with the praetor gates
// it would have run, never with a make target the repository lacks.
const agentHarnessTemplate = `<!-- markdownlint-disable MD013 -->
# {{ if .Owner }}{{ .Owner }}/{{ end }}{{ .RepoName }} Agent Operating Harness

Before concluding any turn:

` + "```bash\n{{ if .VerifyCmd }}{{ .VerifyCmd }}{{ else }}praetorctl compile-context --verify\npraetorctl caveman check --configured-sources\npraetorctl audit{{ end }}\n```\n\n"

// harnessReceiptLine states where a signed receipt comes from: only `praetorctl gate run` mints
// one (cmd/standardsctl/gate.go). What verify-all runs is the repository's own Makefile, so the
// harness promises no receipt for passing it (BUG-804, #503).
const harnessReceiptLine = "Pass = exit 0. Signed Ed25519 Exit-0 receipt only from `praetorctl gate run`; report no receipt it did not mint. " +
	"Fail -> SARIF diagnostic distillation (<= 1500 tokens).\n\n"

// hasVerifyAll reports whether the repository has a verify-all target after this run: the one
// adoption generates, or a repository-owned one it preserved.
func hasVerifyAll(plan *VerificationPlan, pipelines hisscatalog.Pipeline) bool {
	return plan.Status == verificationPreserved || pipelines&hisscatalog.PipelineVerifyAll != 0
}

// verificationSummary describes verify-all as the repository's own gate. Its steps live in the
// Makefile, which the repository owns and may change after adoption, so the harness never lists
// them: a restated recipe drifts from the target, and an agent reports what the list says
// instead of what runs (#503). Per plan it says whether adoption generated the target, kept a
// repository-owned one unread, has none because the makefile step is declined, or wrote one that
// fails until the project declares build and test commands.
func verificationSummary(facts harnessFacts) string {
	plan, pipelines := facts.plan, facts.pipelines
	switch {
	case plan.Status == verificationPreserved:
		return "`" + verifyCommand + "` = repository-owned gate; adoption kept it unread + unexecuted. Steps live in `" + makefileName +
			"`: read there, never restate. Footer commands = project markers only.\n"
	case pipelines&hisscatalog.PipelineVerifyAll == 0:
		return "No `" + verifyCommand + "` target: `makefile` declined in `" + manifestFile + "`. Run gates above + declared build/test commands (footer) directly.\n"
	case plan.Status == verificationUnavailable:
		return "`" + verifyCommand + "` fails until project build + test commands exist; reasons in footer.\n"
	}
	return "`" + verifyCommand + "` = repository gate. Steps live in `" + makefileName + "`: read there, never restate; adoption executed none.\n"
}

const agentHarnessFooterTemplate = harnessFooterHeading + `

` + "```bash\n" + `# Fast local test suite
{{ .TestCmd }}

# Recompile and verify cross-agent context outputs
praetorctl compile-context --verify

# Audit repository against declared HISS standards
praetorctl audit
{{ if .VerifyCmd }}
# Repository gate; steps live in Makefile
{{ .VerifyCmd }}
{{ end }}` + "```\n"

// harnessFacts is what one adoption run knows when it renders the harness: the identity for
// the title, the verification plan, the pipelines the run generates, what the git-hooks step
// leaves active, and the vendor files compile-context writes under agent_clients. The harness
// states nothing beyond them.
type harnessFacts struct {
	// owner is empty when the repository identity is unresolved; the title then names name.
	owner, name, arch string
	plan              *VerificationPlan
	pipelines         hisscatalog.Pipeline
	hooks             hookActivation
	targets           []agentcontext.VendorTarget
	// workflows are the CI workflows this run scaffolds, with what each runs (rule 5).
	workflows []scaffoldedWorkflow
	// hiss is what the invariant rows depend on: languages and the enforced function length.
	hiss hisscatalog.Facts
	// dispatchGated reports a registered pre-dispatch hook (agenthook.DispatchGateRegistered);
	// only then does the text register section say a hook denies a brief without `task:`.
	// Adoption registers only the pre-tool row, so the agent-hooks step that runs later
	// cannot change the answer.
	dispatchGated bool
}

// buildAgentHarness renders the canonical harness, terminated by harnessEndMarker.
func buildAgentHarness(facts harnessFacts) (string, error) {
	plan := facts.plan
	verifyAll := hasVerifyAll(plan, facts.pipelines)
	tCtx := templates.Context{
		RepoName:  facts.name,
		Owner:     facts.owner,
		Archetype: facts.arch,
		TestCmd:   verificationTestText(plan),
		Runtime:   strings.Join(plan.Runtimes, ", "),
	}
	if verifyAll {
		tCtx.VerifyCmd = verifyCommand
	}
	header, err := templates.Render("harness_header", agentHarnessTemplate, tCtx)
	if err != nil {
		return "", fmt.Errorf("render harness header: %w", err)
	}
	footer, err := templates.Render("harness_footer", agentHarnessFooterTemplate, tCtx)
	if err != nil {
		return "", fmt.Errorf("render harness footer: %w", err)
	}
	register, err := harnessRegisterSection(facts.dispatchGated)
	if err != nil {
		return "", err
	}
	header += verificationSummary(facts) + harnessReceiptLine
	directives := buildAgentHarnessDirectives(facts)
	return header + directives + register + footer + "\n" + harnessLintScopeEnd + harnessEndMarker + "\n", nil
}

// harnessIdentity returns the owner and name the harness title carries: the origin remote's
// identity, else the one the manifest declares, else no owner and the prose label.
func (s *adoptSession) harnessIdentity() (owner, name string) {
	if s.identity.resolved() {
		return s.identity.owner, s.identity.name
	}
	if s.policy != nil && s.policy.Manifest != nil {
		declared := s.policy.Manifest.Repository
		if declared.Owner != "" && declared.Name != "" {
			return declared.Owner, declared.Name
		}
	}
	return "", s.repoName
}

// harnessRegisterSection renders the default text register section. Adoption needs neither
// the adoptee's manifest nor its routing file here: the adoptee's own compile-context
// re-splices the block from its manifest, and audit reports the difference until it does. The
// dispatch hook sentence follows harnessFacts.dispatchGated, read the way compile-context reads
// it.
func harnessRegisterSection(dispatchGated bool) (string, error) {
	block, err := config.RenderRegisterBlock(config.DefaultRegisterPolicy(), dispatchGated)
	if err != nil {
		return "", fmt.Errorf("render harness text register: %w", err)
	}
	return config.RegisterSectionPrefix + block + "\n\n", nil
}

// dropRegisterSection removes a text register section from repository instructions that
// are about to be joined with a harness carrying its own. compile-context appends the
// section to an AGENTS.md that has none, so the instructions kept across a harness refresh
// can hold one; two marker pairs in one file would fail every later compile. Content whose
// markers cannot be located is returned unchanged for compile-context to report.
func dropRegisterSection(content string) string {
	first, last, err := util.FindMarkedBlock(content, config.RegisterBlockStart, config.RegisterBlockEnd)
	if err != nil || first < 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	start := first
	switch {
	case first >= 2 && strings.TrimSpace(lines[first-1]) == "" && strings.TrimSpace(lines[first-2]) == config.RegisterBlockHeading:
		start = first - 2
	case first >= 1 && strings.TrimSpace(lines[first-1]) == config.RegisterBlockHeading:
		start = first - 1
	}
	kept := append(append([]string{}, lines[:start]...), lines[last+1:]...)
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// foreignInstructions returns an AGENTS.md that carries no harness yet as it will follow
// the new one: byte-identical, unless it held a text register section of its own.
func foreignInstructions(existing string) string {
	if stripped := dropRegisterSection(existing); stripped != existing {
		return stripped + "\n"
	}
	return existing
}

// buildAgentHarnessDirectives renders the invariant table and the operational rules. Every row
// comes from the one HISS catalog (hisscatalog.Rules), so the table lists every rule the MCP
// explain_rule tool explains, and each row states the check the pipelines this run generates
// give it, or that none exists (BUG-779, BUG-804). The rows keep the shape
// hisscatalog.ParseGatedInvariants reads, the parser the generated wiki uses. Each rule's
// directive names only the language constructs of the repository's languages and the
// function length its audit enforces (#68). Rule 5 states the local hooks and the CI
// workflows this run scaffolds.
func buildAgentHarnessDirectives(facts harnessFacts) string {
	var b strings.Builder
	b.WriteString(hisscatalog.GatedInvariantsHeading + "\n\n")
	b.WriteString(adoptedCheckLegend(facts.pipelines))
	b.WriteString("| Invariant | Rule | Adopted check | On fail |\n| :--- | :--- | :--- | :--- |\n")
	for _, rule := range hisscatalog.Rules() {
		check, failure := rule.AdoptedFor(facts.pipelines, facts.hiss)
		fmt.Fprintf(&b, "| **%s** %s | %s | %s | %s |\n", rule.ID, rule.Scope, rule.AdoptedDirective(facts.hiss), check, failure)
	}
	b.WriteString("\n" + harnessOperationalRules)
	b.WriteString(transpilerRuleHead(facts.targets))
	b.WriteString(harnessTranspilerRule)
	b.WriteString(harnessEvasionRule + hooksClaim(facts.hooks) + ciClaim(facts.workflows))
	b.WriteString(harnessAntiLoopRule)
	return b.String()
}

// adoptedCheckLegend names the pipelines the Adopted check column reads from, or says there
// are none, so a reader never takes a row for a gate the run did not generate.
func adoptedCheckLegend(pipelines hisscatalog.Pipeline) string {
	var generated []string
	if pipelines&hisscatalog.PipelineVerifyAll != 0 {
		generated = append(generated, "generated `"+verifyCommand+"`")
	}
	if pipelines&hisscatalog.PipelineLefthook != 0 {
		generated = append(generated, "praetor `"+lefthookFile+"`")
	}
	source := "none: no generated `" + verifyCommand + "`, no praetor `" + lefthookFile + "`"
	if len(generated) > 0 {
		source = strings.Join(generated, " + ")
	}
	return "Adopted check = check adoption generated here (" + source + "), as written at adoption; later edits to those files not reflected. `" +
		hisscatalog.NotEnforced + "` = rule binds, no generated check decides it for repository languages.\n\n"
}

// transpilerRuleHead opens rule 3 with the files compile-context writes under agent_clients,
// read from the transpiler's own registry (agentcontext.VendorTargets), so the rule names
// neither fewer files than compile-context overwrites (BUG-840) nor files the selection
// leaves out.
func transpilerRuleHead(targets []agentcontext.VendorTarget) string {
	if len(targets) == 0 {
		return "3. **Context transpiler first.** `agent_clients` selects no compiled vendor file. All agent instruction updates -> `AGENTS.md`, then:\n\n"
	}
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, "`"+target.Path+"`")
	}
	return "3. **Context transpiler first.** Never edit " + strings.Join(names, ", ") +
		" manually. All agent instruction updates -> `AGENTS.md`, then:\n\n"
}

// hooksClaim continues rule 5 with the local gate this run installs; ciClaim completes it with
// the server side.
func hooksClaim(hooks hookActivation) string {
	switch hooks {
	case hooksLefthook:
		return "Hooks = local gate adoption installs. "
	case hooksInactive:
		return "`" + lefthookFile + "` = praetor-written, but `lefthook` did not run at adoption: its hooks stay inactive until `lefthook install`. " +
			"Fallback pre-commit hook, where installed, runs `praetorctl compile-context --verify` + `praetorctl audit` only. "
	}
	return "Adoption installed no hooks here (`" + lefthookFile + "` not praetor-written, or `git-hooks` declined). "
}

const harnessOperationalRules = `## Operational Rules

1. **Act on verified state.** Read source files, run real commands before hypothesis or edit. Never guess flag names, library signatures, repo configuration from memory.

2. **Lead with output.** Direct answers, diffs, commands. No filler preamble, no "Based on", no restatement, no chatter.

`

// harnessTranspilerRule completes rule 3 and carries rule 4.
const harnessTranspilerRule = `   ` + "```bash\n   praetorctl compile-context\n   ```\n\n" + `   - ` + "`AGENTS.md`" + ` = agent-only text -> caveman (internal register). ` + "`praetorctl compile-context --verify`" + ` + ` + "`praetorctl audit`" + ` run caveman lint; findings fail gate; no opt-out. Check first: ` + "`praetorctl caveman check --kind=context AGENTS.md`" + `.

4. **SARIF diagnostic distillation.** Compiler/linter errors -> distill to $\le 1,500$ tokens ($< 60$ lines): top 3 root-cause failures with file/line pointers; full SARIF logs -> ephemeral storage.

`

// harnessEvasionRule opens rule 5; hooksClaim completes it.
const harnessEvasionRule = `5. **No evasion.** Never attempt ` + "`--no-verify`" + `, ` + "`LEFTHOOK=0`" + `, or modifying ` + "`.git/hooks`" + `. `

const harnessAntiLoopRule = `6. **Anti-loop interception.** Same AST diff + error category repeats $\ge 3$ times -> halt immediately. Re-evaluate design; no micro-textual retries.

`

// hasHarness reports whether content already carries a praetor harness.
func hasHarness(content string) bool {
	return strings.Contains(content, "Agent Operating Harness") ||
		strings.Contains(content, "## Core Directives & Invariants")
}

// splitHarnessTail returns the repository-specific instructions that follow an existing
// harness. It recognises, in order, the end marker written by current versions, the
// footer of harnesses written before the marker existed, and a bare "---" separator.
// ok is false when no boundary can be identified.
func splitHarnessTail(existing string) (tail string, ok bool) {
	if idx := strings.Index(existing, harnessEndMarker); idx >= 0 {
		return trimSeparator(existing[idx+len(harnessEndMarker):]), true
	}
	if idx := strings.Index(existing, harnessFooterHeading); idx >= 0 {
		rest := existing[idx:]
		open := strings.Index(rest, codeFence)
		if open < 0 {
			return "", false
		}
		closing := strings.Index(rest[open+len(codeFence):], codeFence)
		if closing < 0 {
			return "", false
		}
		end := open + len(codeFence) + closing + len(codeFence)
		return trimSeparator(rest[end:]), true
	}
	if parts := strings.SplitN(existing, harnessSeparator, 2); len(parts) == 2 {
		return trimSeparator(parts[1]), true
	}
	return "", false
}

// trimSeparator drops surrounding whitespace and one leading "---" separator line.
func trimSeparator(tail string) string {
	tail = strings.TrimSpace(tail)
	if strings.HasPrefix(tail, "---") {
		tail = strings.TrimSpace(strings.TrimPrefix(tail, "---"))
	}
	return tail
}

func reconcileAgentHarness(ctx context.Context, s *adoptSession) error {
	declared, err := config.LoadDeclaredTooling(ctx, s.repoPath)
	if err != nil {
		return fmt.Errorf("read agent_clients selection from %s: %w", manifestFile, err)
	}
	facts, err := s.harnessFacts(ctx, declared.AgentClients)
	if err != nil {
		return err
	}
	agentsContent, err := resolveAgentsContent(s, facts)
	if err != nil {
		return err
	}
	return transpileAgentTargets(ctx, s, agentsContent, declared.AgentClients)
}

// harnessFacts gathers what the harness may state about this run: identity, plan, the
// pipelines the run generates (generatedPipelines), the vendor files agent_clients selects and
// the CI workflows the run scaffolds (scaffoldedWorkflows).
func (s *adoptSession) harnessFacts(ctx context.Context, clients []string) (harnessFacts, error) {
	pipelines, hooks, err := s.generatedPipelines(ctx)
	if err != nil {
		return harnessFacts{}, err
	}
	targets, err := agentcontext.VendorTargets(clients)
	if err != nil {
		return harnessFacts{}, fmt.Errorf("agent_clients in %s: %w", manifestFile, err)
	}
	docsGate, err := documentationEnabledForSession(s)
	if err != nil {
		return harnessFacts{}, fmt.Errorf("resolve the documentation gate for the harness: %w", err)
	}
	workflows, err := s.scaffoldedWorkflows(ctx, docsGate)
	if err != nil {
		return harnessFacts{}, err
	}
	gated, err := agenthook.DispatchGateRegistered(ctx, s.repoPath)
	if err != nil {
		return harnessFacts{}, fmt.Errorf("read dispatch hook registration for the harness: %w", err)
	}
	owner, name := s.harnessIdentity()
	return harnessFacts{owner: owner, name: name, arch: s.arch, plan: s.verification, pipelines: pipelines,
		hooks: hooks, targets: targets, workflows: workflows, hiss: s.hissFacts(), dispatchGated: gated}, nil
}

func resolveAgentsContent(s *adoptSession, facts harnessFacts) (string, error) {
	full, err := repoFile(s.repoPath, agentsFile)
	if err != nil {
		return "", err
	}
	harness, err := buildAgentHarness(facts)
	if err != nil {
		return "", err
	}
	if !fileExists(full) {
		if err := validateHarnessProjection(harness); err != nil {
			return "", err
		}
		if err := s.write(full, []byte(harness), filePerm); err != nil {
			return "", err
		}
		s.report.recordCreated(agentsFile, "Synthesized canonical Praetor Agent Operating Harness and HISS invariants")
		return harness, nil
	}
	existingBytes, err := readRepoFile(full)
	if err != nil {
		return "", err
	}
	return mergeExistingAgentsContent(s, full, string(existingBytes), harness)
}

func validateHarnessProjection(content string) error {
	if _, err := compiler.NewTranspiler().CompileContent(content); err != nil {
		return fmt.Errorf("context composition cannot produce valid projections: %w", err)
	}
	return nil
}

// mergeExistingAgentsContent prepends the harness to a foreign AGENTS.md, leaves an
// existing harness alone without Force, and with Force replaces only the harness part
// while keeping everything after its boundary. When the boundary of an existing harness
// cannot be identified the file is left untouched and an error is recorded rather than
// silently discarding repository instructions.
func mergeExistingAgentsContent(s *adoptSession, full, existing, harness string) (string, error) {
	if !hasHarness(existing) {
		merged := harness + harnessSeparator + "\n" + foreignInstructions(existing)
		if err := validateHarnessProjection(merged); err != nil {
			return "", err
		}
		if err := s.write(full, []byte(merged), filePerm); err != nil {
			return "", err
		}
		s.report.recordReconciledAs(agentsFile, actionMerge, "Merged Praetor Agent Operating Harness & HISS directives above existing instructions")
		return merged, nil
	}
	if !s.opts.Force {
		s.report.recordReconciled(agentsFile, "Existing Praetor Agent Operating Harness preserved; command synchronization not verified")
		if existing != harness {
			s.report.addWarning("Existing AGENTS.md was preserved; review its commands against the verification plan or use --force to refresh a recognized harness boundary.")
		}
		return existing, nil
	}
	tail, ok := splitHarnessTail(existing)
	if !ok {
		s.report.addError("%s: cannot locate the end of the existing harness; file left untouched (separate repository instructions from the harness with a '---' line and re-run)", agentsFile)
		s.report.recordReconciled(agentsFile, "Existing harness left untouched: boundary to repository instructions not found")
		return existing, nil
	}
	tail = dropRegisterSection(tail)
	merged := strings.TrimSpace(harness) + "\n"
	if tail != "" {
		merged = strings.TrimSpace(harness) + "\n" + harnessSeparator + "\n" + tail + "\n"
	}
	if err := validateHarnessProjection(merged); err != nil {
		return "", err
	}
	if err := s.write(full, []byte(merged), filePerm); err != nil {
		return "", err
	}
	s.report.recordReconciled(agentsFile, "Updated Praetor Agent Operating Harness while preserving repository-specific instructions")
	return merged, nil
}

// transpileAgentTargets compiles AGENTS.md into the vendor context files of the selected
// agent clients (nil selects every client). A compile failure is fatal: HISS-16 guarantees
// that the vendor files mirror AGENTS.md. The files are written by the writer compile-context
// uses (Transpiler.WriteOutputsContext): every target is checked before the first is written,
// and no symlink below the repository is followed.
func transpileAgentTargets(ctx context.Context, s *adoptSession, agentsContent string, clients []string) error {
	tr := compiler.NewTranspiler()
	tr.Clients = clients
	res, err := tr.CompileContent(agentsContent)
	if err != nil {
		return fmt.Errorf("context compilation: %w", err)
	}
	for i := 0; i < len(res.NotApplicable) && i < maxTranspileTargets; i++ {
		s.report.recordNotApplicable(res.NotApplicable[i], "Not selected by agent_clients in "+manifestFile)
	}
	existed, err := existingVendorTargets(s.repoPath, res.Files)
	if err != nil {
		return err
	}
	if !s.opts.DryRun {
		if err := tr.WriteOutputsContext(ctx, res, s.repoPath); err != nil {
			return fmt.Errorf("write vendor context targets: %w", err)
		}
	}
	recordVendorTargets(s.report, res.Files, existed)
	return nil
}

// existingVendorTargets reports which vendor files already exist, refusing any path that leaves
// the repository (repoFile).
func existingVendorTargets(repoPath string, files []compiler.TargetFile) ([]bool, error) {
	existed := make([]bool, len(files))
	for i := 0; i < len(files) && i < maxTranspileTargets; i++ {
		full, err := repoFile(repoPath, files[i].RelativePath)
		if err != nil {
			return nil, err
		}
		existed[i] = fileExists(full)
	}
	return existed, nil
}

// recordVendorTargets lists each vendor file as reconciled when it existed and created otherwise.
func recordVendorTargets(report *AdoptReport, files []compiler.TargetFile, existed []bool) {
	for i := 0; i < len(files) && i < len(existed) && i < maxTranspileTargets; i++ {
		f := files[i]
		if existed[i] {
			report.recordReconciled(f.RelativePath, fmt.Sprintf("Synchronized vendor context target (%d LOC)", f.LineCount))
			continue
		}
		report.recordCreated(f.RelativePath, fmt.Sprintf("Compiled vendor context target (%d LOC)", f.LineCount))
	}
}
