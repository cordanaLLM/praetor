package adopt

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
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
	// register is the text register block compile-context splices for this repository
	// (compiler.LoadRegisterBlock): the manifest's register policy, and the dispatch hook
	// sentence only where a pre-dispatch hook is registered. Adoption registers only the
	// pre-tool row, so the agent-hooks step that runs later cannot change it. Empty renders the
	// block of a repository with no manifest policy and no dispatch hook.
	register string
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
	register, err := harnessRegisterSection(facts.register)
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

// harnessRegisterSection renders the text register section around block, the block
// compile-context splices for the repository (harnessFacts.register), so an adopted AGENTS.md
// verifies against the manifest's register policy without a compile-context run first (#502).
// An empty block renders the default policy without the dispatch hook sentence.
func harnessRegisterSection(block string) (string, error) {
	if block == "" {
		rendered, err := config.RenderRegisterBlock(config.DefaultRegisterPolicy(), false)
		if err != nil {
			return "", fmt.Errorf("render harness text register: %w", err)
		}
		block = rendered
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

// harnessTitleSuffix ends the H1 of every harness praetor writes (agentHarnessTemplate).
const harnessTitleSuffix = "Agent Operating Harness"

// harnessLintDisable opens the markdownlint comment every praetor harness has written on the
// line above its H1, whatever rules it named (MD013 now; MD013 MD025, or none, earlier).
const harnessLintDisable = "<!-- markdownlint-disable"

// maxHarnessLines bounds the line walk over an existing AGENTS.md (HISS-02). readRepoFile caps
// the file at contextopt.MaxSourceBytes, so no file it returns comes near it.
const maxHarnessLines = 1 << 20

// hasHarness reports whether content already carries a praetor harness: a harness H1 or the
// invariant heading on a line of its own, outside fenced code (harnessStart). Prose that only
// names the harness is repository text, so a forced refresh never takes its first "---" for a
// harness boundary.
func hasHarness(content string) bool {
	return harnessStart(content) >= 0
}

// harnessStart returns the byte offset of the line an existing harness starts at, or -1 when
// content carries none. The harness opens at its first H1 ending in harnessTitleSuffix. A
// harness whose H1 was renamed is found by its invariant heading and opens at the nearest H1
// above that heading, or at the top of content when no H1 precedes it: every harness praetor
// writes opens with an H1, so the text between that H1 and the heading is harness intro. Kept
// as preamble, it would sit above the regenerated intro on every later --force, with its
// edited lines never replaced. A markdownlint-disable comment on the line directly above the
// start line is part of the harness. Text above the start is the repository's preamble, such
// as an SPDX header. H1s and headings inside fenced code are skipped.
func harnessStart(content string) int {
	lines := strings.SplitN(content, "\n", maxHarnessLines)
	var fence util.MarkdownFence
	offset, nearestTitle := 0, 0
	for i := 0; i < len(lines) && i < maxHarnessLines; i++ {
		trimmed := strings.TrimSpace(lines[i])
		start := lineStartWithLint(lines, i, offset)
		offset += len(lines[i]) + 1
		if fence.Inside(trimmed) {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "# ") && strings.HasSuffix(trimmed, harnessTitleSuffix):
			return start
		case strings.HasPrefix(trimmed, hisscatalog.GatedInvariantsHeading):
			return nearestTitle
		case strings.HasPrefix(trimmed, "# "):
			nearestTitle = start
		}
	}
	return -1
}

// lineStartWithLint returns offset, where line i of lines starts, or where line i-1 starts
// when that line is a markdownlint-disable comment (harnessLintDisable).
func lineStartWithLint(lines []string, i, offset int) int {
	if i > 0 && strings.HasPrefix(strings.TrimSpace(lines[i-1]), harnessLintDisable) {
		return offset - len(lines[i-1]) - 1
	}
	return offset
}

// splitHarnessTail splits body, text that opens with an existing harness, at the harness
// boundary: head is the harness text before it, tail the repository-specific instructions
// after it. It recognises, in order, the end marker written by current versions, the footer
// of harnesses written before the marker existed, and a bare "---" separator. ok is false
// when no boundary can be identified.
func splitHarnessTail(body string) (head, tail string, ok bool) {
	headEnd, tailStart := -1, -1
	if idx := strings.Index(body, harnessEndMarker); idx >= 0 {
		headEnd, tailStart = idx, idx+len(harnessEndMarker)
	} else if idx := strings.Index(body, harnessFooterHeading); idx >= 0 {
		headEnd = footerEnd(body, idx)
		tailStart = headEnd
	} else if idx := strings.Index(body, harnessSeparator); idx >= 0 {
		headEnd, tailStart = idx, idx+len(harnessSeparator)
	}
	if headEnd < 0 {
		return "", "", false
	}
	return body[:headEnd], trimSeparator(body[tailStart:]), true
}

// footerEnd returns where the footer section opening at start ends, after the closing fence of
// its shell block, or -1 when the block is not closed.
func footerEnd(body string, start int) int {
	rest := body[start:]
	open := strings.Index(rest, codeFence)
	if open < 0 {
		return -1
	}
	closing := strings.Index(rest[open+len(codeFence):], codeFence)
	if closing < 0 {
		return -1
	}
	return start + open + len(codeFence) + closing + len(codeFence)
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
	// Before the harness renders the register block, which names the skills the run leaves (#235).
	if err := s.installRegisterSkills(ctx); err != nil {
		return err
	}
	facts, err := s.harnessFacts(ctx, declared.AgentClients)
	if err != nil {
		return err
	}
	// Read before resolveAgentsContent rewrites AGENTS.md: the vendor files an unedited run of
	// compile-context left are the projections of AGENTS.md as the run found it.
	prior, err := priorVendorTexts(ctx, s.repoPath, declared.AgentClients)
	if err != nil {
		return err
	}
	agentsContent, err := resolveAgentsContent(ctx, s, facts)
	if err != nil {
		return err
	}
	if err := projectVendorContext(ctx, s, agentsContent, declared.AgentClients, prior); err != nil {
		return err
	}
	// The gate compile-context --verify and audit apply; the text adoption keeps from the
	// repository can fail it, so the pillar warns instead of claiming a linted harness.
	if _, err := compiler.LintContextText(agentsFile, agentsContent); err != nil {
		s.report.addWarning("%v", err)
	}
	return nil
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
	families, err := enabledManagedFamiliesForSession(ctx, s)
	if err != nil {
		return harnessFacts{}, fmt.Errorf("resolve the managed gates for the harness: %w", err)
	}
	workflows, err := s.scaffoldedWorkflows(ctx, families)
	if err != nil {
		return harnessFacts{}, err
	}
	register, err := s.registerBlock(ctx)
	if err != nil {
		return harnessFacts{}, err
	}
	owner, name := s.harnessIdentity()
	return harnessFacts{owner: owner, name: name, arch: s.arch, plan: s.verification, pipelines: pipelines,
		hooks: hooks, targets: targets, workflows: workflows, hiss: s.hissFacts(), register: register}, nil
}

func resolveAgentsContent(ctx context.Context, s *adoptSession, facts harnessFacts) (string, error) {
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
	return mergeExistingAgentsContent(ctx, s, full, string(existingBytes), harness)
}

func validateHarnessProjection(content string) error {
	if _, err := compiler.NewTranspiler().CompileContent(content); err != nil {
		return fmt.Errorf("context composition cannot produce valid projections: %w", err)
	}
	return nil
}

// mergeExistingAgentsContent prepends the harness to a foreign AGENTS.md, leaves an
// existing harness alone without Force, and with Force regenerates the harness while keeping
// what the repository added around it (refreshAgentHarness).
func mergeExistingAgentsContent(ctx context.Context, s *adoptSession, full, existing, harness string) (string, error) {
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
		return keepAgentHarness(ctx, s, existing, harness)
	}
	return refreshAgentHarness(ctx, s, existing, harness)
}

// keepAgentHarness keeps an existing harness on a run without --force, with its text register
// block spliced from the manifest (splicedKeptHarness), as compile-context splices it before
// every compile. The block is rendered, never hand-written, so a kept block that no longer
// matches the manifest is refreshed rather than left to fail the compile-context --verify that
// runs after the chain (verifyAgentContext). Every other line stays as written. A splice goes
// through replaceExisting, as refreshAgentHarness does: the prior bytes are backed up, the
// report lists AGENTS.md as replaced with its line delta, and the write lands only over the
// bytes this run read (ReplaceSnapshotIn with Expected).
func keepAgentHarness(ctx context.Context, s *adoptSession, existing, harness string) (string, error) {
	block, err := s.registerBlock(ctx)
	if err != nil {
		return "", err
	}
	kept, spliced, err := splicedKeptHarness(existing, block)
	if err != nil {
		return "", err
	}
	detail := "Existing Praetor Agent Operating Harness preserved; command synchronization not verified"
	if !spliced {
		s.report.recordReconciled(agentsFile, detail)
	} else if err := s.replaceExisting(ctx, replacement{
		rel: agentsFile, before: []byte(existing), after: []byte(kept),
		detail: detail + "; text register block spliced from the manifest, as compile-context splices it",
		publish: func(ctx context.Context) error {
			return contextopt.ReplaceSnapshotIn(ctx, s.repoPath, agentsFile, []byte(kept),
				contextopt.ReplaceOptions{Expected: []byte(existing), Exists: true, Mode: filePerm})
		},
	}); err != nil {
		return "", fmt.Errorf("splice the text register block into %s: %w", agentsFile, err)
	}
	if kept != harness {
		s.report.addWarning("Existing AGENTS.md was preserved; review its commands against the verification plan or run %s "+
			"to refresh a recognized harness boundary.", s.forceCommand())
	}
	return kept, nil
}

// splicedKeptHarness returns existing, a harness a run without --force keeps, with its text
// register block spliced from block (compiler.SpliceRegisterBlock), and whether the splice
// changed it. A changed harness must still compile into valid projections
// (validateHarnessProjection). keepAgentHarness writes the result and preflightKeptHarness checks
// it before the first step writes, so both refuse the same file.
func splicedKeptHarness(existing, block string) (string, bool, error) {
	kept, spliced, err := compiler.SpliceRegisterBlock(existing, block)
	if err != nil {
		return "", false, fmt.Errorf("%s: text register block: %w", agentsFile, err)
	}
	if spliced {
		if err := validateHarnessProjection(kept); err != nil {
			return "", false, err
		}
	}
	return kept, spliced, nil
}

// preflightKeptHarness runs keepAgentHarness's refusals before the first step writes, on a run
// without --force whose AGENTS.md holds a harness: a register block the splice refuses, such as
// one with no end marker, a spliced harness with no valid projection, and, when the splice
// changes the file, a backup root checkBackupRoot refuses. Checked only in the step, each came
// after the manifest, the lock and the catalog were written. Under --force refreshAgentHarness
// runs instead, and preflightForceBackupRoot checks the backup root.
func preflightKeptHarness(ctx context.Context, s *adoptSession, block string) error {
	if s.opts.Force {
		return nil
	}
	full, err := repoFile(s.repoPath, agentsFile)
	if err != nil || !fileExists(full) {
		return err
	}
	existing, err := readRepoFile(full)
	if err != nil || !hasHarness(string(existing)) {
		return err
	}
	_, spliced, err := splicedKeptHarness(string(existing), block)
	if err != nil || !spliced {
		return err
	}
	return checkBackupRoot(ctx, s.repoPath)
}

// refreshAgentHarness regenerates the harness of existing under --force and keeps what the
// repository added around it (harnessAdditions): the preamble above the harness start line,
// invariant rows under IDs of its own, and the instructions after the harness boundary. Every
// other harness line is praetor's and is regenerated; an edited one is reported through
// replaceExisting, with the line delta and the backup of the prior bytes. The work runs on LF
// text and the result keeps the file's CRLF convention (util.NormalizeLineEndings), as
// compile-context splices the register block. When the boundary of the harness cannot be
// identified the file is left untouched and an error is recorded rather than silently
// discarding repository instructions.
func refreshAgentHarness(ctx context.Context, s *adoptSession, existing, harness string) (string, error) {
	lf, crlf := util.NormalizeLineEndings(existing)
	kept, ok, err := splitHarnessAdditions(lf, harness)
	if err != nil {
		return "", err
	}
	if !ok {
		s.report.addError("%s: cannot locate the end of the existing harness; file left untouched (separate repository instructions from the harness with a '---' line and re-run)", agentsFile)
		s.report.recordReconciled(agentsFile, "Existing harness left untouched: boundary to repository instructions not found")
		return existing, nil
	}
	merged, err := kept.join(harness)
	if err != nil {
		return "", err
	}
	merged = util.RestoreLineEndings(merged, crlf)
	if err := validateHarnessProjection(merged); err != nil {
		return "", err
	}
	if merged == existing {
		s.report.recordReconciled(agentsFile, "Praetor Agent Operating Harness already current"+kept.describe())
		return existing, nil
	}
	err = s.replaceExisting(ctx, replacement{
		rel: agentsFile, before: []byte(existing), after: []byte(merged),
		detail: "Refreshed Praetor Agent Operating Harness" + kept.describe(),
		publish: func(ctx context.Context) error {
			return contextopt.ReplaceSnapshotIn(ctx, s.repoPath, agentsFile, []byte(merged),
				contextopt.ReplaceOptions{Expected: []byte(existing), Exists: true, Mode: filePerm})
		},
	})
	if err != nil {
		return "", fmt.Errorf("refresh %s: %w", agentsFile, err)
	}
	return merged, nil
}

// harnessAdditions is what a forced refresh keeps of an existing AGENTS.md around the harness it
// regenerates.
type harnessAdditions struct {
	// preamble is the text above the harness start line (harnessStart), as written.
	preamble string
	// rows are the invariant rows the repository added (repositoryInvariantRows).
	rows []hisscatalog.InvariantRow
	// tail is the instructions after the harness boundary, a text register section dropped.
	tail string
}

// splitHarnessAdditions splits lf, an existing AGENTS.md as LF text, around its harness, with
// harness the one that replaces it. ok is false when lf carries no harness or no boundary
// after its start can be identified.
func splitHarnessAdditions(lf, harness string) (harnessAdditions, bool, error) {
	start := harnessStart(lf)
	if start < 0 {
		return harnessAdditions{}, false, nil
	}
	head, tail, ok := splitHarnessTail(lf[start:])
	if !ok {
		return harnessAdditions{}, false, nil
	}
	rows, err := repositoryInvariantRows(head, harness)
	if err != nil {
		return harnessAdditions{}, false, err
	}
	return harnessAdditions{preamble: lf[:start], rows: rows, tail: dropRegisterSection(tail)}, true, nil
}

// repositoryInvariantRows returns the rows of head's invariant table the repository added:
// rows under an ID the table of harness lacks and outside the catalog's ID grammar, in their
// order. Both tables are read by the one row splitter (hisscatalog.InvariantTableRows). A HISS
// row the catalog does not define is praetor's, and hisscatalog.ParseGatedInvariants refuses
// it, so it is regenerated away rather than kept.
func repositoryInvariantRows(head, harness string) ([]hisscatalog.InvariantRow, error) {
	existing, err := hisscatalog.InvariantTableRows(head)
	if err != nil {
		return nil, fmt.Errorf("read the invariant table of %s: %w", agentsFile, err)
	}
	generated, err := hisscatalog.InvariantTableRows(harness)
	if err != nil {
		return nil, fmt.Errorf("read the generated invariant table: %w", err)
	}
	have := make(map[string]bool, len(generated))
	for _, row := range generated {
		have[row.ID] = true
	}
	var kept []hisscatalog.InvariantRow
	for _, row := range existing {
		if !have[row.ID] && !hisscatalog.InCatalogNamespace(row.ID) {
			kept = append(kept, row)
		}
	}
	return kept, nil
}

// join renders the refreshed file: the preamble, harness with the kept rows after its last
// generated row, then the separator and the tail.
func (a harnessAdditions) join(harness string) (string, error) {
	body, err := insertInvariantRows(harness, a.rows)
	if err != nil {
		return "", err
	}
	merged := a.preamble + strings.TrimSpace(body) + "\n"
	if a.tail != "" {
		merged += harnessSeparator + "\n" + a.tail + "\n"
	}
	return merged, nil
}

// insertInvariantRows returns harness with rows appended, as written, after the last row of its
// invariant table.
func insertInvariantRows(harness string, rows []hisscatalog.InvariantRow) (string, error) {
	if len(rows) == 0 {
		return harness, nil
	}
	generated, err := hisscatalog.InvariantTableRows(harness)
	if err != nil {
		return "", fmt.Errorf("read the generated invariant table: %w", err)
	}
	if len(generated) == 0 {
		return "", fmt.Errorf("generated harness carries no invariant row to keep %s rows after", agentsFile)
	}
	last := generated[len(generated)-1].Line + "\n"
	at := strings.Index(harness, last)
	if at < 0 {
		return "", fmt.Errorf("generated harness does not carry its last invariant row on a line of its own")
	}
	at += len(last)
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.Line+"\n")
	}
	return harness[:at] + strings.Join(lines, "") + harness[at:], nil
}

// describe names what the refresh kept, for the report entry.
func (a harnessAdditions) describe() string {
	var kept []string
	if a.preamble != "" {
		kept = append(kept, fmt.Sprintf("preamble (%d lines)", strings.Count(a.preamble, "\n")))
	}
	if len(a.rows) > 0 {
		ids := make([]string, 0, len(a.rows))
		for i := 0; i < len(a.rows) && i < maxDeltaQuotedLines; i++ {
			ids = append(ids, a.rows[i].ID)
		}
		if more := len(a.rows) - len(ids); more > 0 {
			ids = append(ids, fmt.Sprintf("%d more", more))
		}
		kept = append(kept, fmt.Sprintf("%d repository invariant rows (%s)", len(a.rows), strings.Join(ids, ", ")))
	}
	if a.tail != "" {
		kept = append(kept, "repository-specific instructions")
	}
	if len(kept) == 0 {
		return ""
	}
	return "; kept " + strings.Join(kept, ", ")
}

// projectVendorContext compiles AGENTS.md into the vendor context files of the selected
// agent clients (nil selects every client): the vendor half of compile-context's projection
// (compiler.CompileContextProjections), with the renderer and the writer
// compiler.CompileVendorTargets uses (Transpiler.CompileContent, Transpiler.WriteOutputsContext);
// the persona and plugin half is the agent-definitions step's (projectAgentSurfaces). It compiles
// the text the step composed rather than the file, so a dry run plans the same files. A compile
// failure is fatal: HISS-16 guarantees that the vendor files mirror AGENTS.md. Every target is
// checked before the first is written, and no symlink below the repository is followed. An
// existing file that is neither its new projection nor its prior one (priorVendorTexts) holds a
// hand edit: it is backed up first and reported as replaced, on a plain run too
// (vendor_targets.go).
func projectVendorContext(ctx context.Context, s *adoptSession, agentsContent string, clients []string, prior priorVendorProjections) error {
	tr := compiler.NewTranspiler()
	tr.Clients = clients
	res, err := tr.CompileContent(agentsContent)
	if err != nil {
		return fmt.Errorf("context compilation: %w", err)
	}
	for i := 0; i < len(res.NotApplicable) && i < maxTranspileTargets; i++ {
		s.report.recordNotApplicable(res.NotApplicable[i], "Not selected by agent_clients in "+manifestFile)
	}
	targets, err := observeVendorTargets(ctx, s.repoPath, res.Files)
	if err != nil {
		return err
	}
	publish := func(ctx context.Context) error {
		if err := tr.WriteOutputsContext(ctx, res, s.repoPath); err != nil {
			return fmt.Errorf("write vendor context targets: %w", err)
		}
		return nil
	}
	if err := s.replaceExistingAll(ctx, projectionReplacements(targets, prior, vendorContextLabels), publish); err != nil {
		return err
	}
	recordProjections(s.report, targets, prior, vendorContextLabels)
	return nil
}
