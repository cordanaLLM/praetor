package adopt

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/paperclip"
)

const (
	rulesetFile      = ".github/rulesets/main.json"
	labelsFile       = ".config/labels.yaml"
	paperclipFile    = ".paperclip/harness.json"
	auditorAgentFile = ".agents/agents/repo-auditor.md"
	gatekeeperFile   = ".agents/agents/repo-gatekeeper.md"
)

// buildRulesetJSON delegates policy rendering to the same service as CLI sync and
// remote forge reconciliation. Context selection remains repository-specific.
func buildRulesetJSON(policy config.BranchProtectionPolicy, contexts []string) (string, error) {
	data, err := forge.RenderRepositoryRuleset(policy, contexts)
	return string(data), err
}

func adoptionBranchPolicy(ctx context.Context, s *adoptSession) (config.BranchProtectionPolicy, error) {
	if s.policy != nil {
		return s.policy.Policy.BranchProtection, nil
	}
	// A dry-run without pinned catalog inputs still has a selected manifest. Only
	// branch defaults and its explicit overrides are claimed by this fallback.
	manifest, err := manifestForLock(ctx, s)
	if err != nil {
		return config.BranchProtectionPolicy{}, err
	}
	policy := config.DefaultPolicy()
	policy.ApplyOverrides(manifest.Overrides)
	return policy.BranchProtection, nil
}

func reconcileBranchRuleset(ctx context.Context, s *adoptSession) error {
	contexts, err := forge.RequiredStatusContexts(ctx, s.repoPath)
	if err != nil {
		return err
	}
	policy, err := adoptionBranchPolicy(ctx, s)
	if err != nil {
		return err
	}
	content, err := buildRulesetJSON(policy, contexts)
	if err != nil {
		return err
	}
	_, err = s.scaffoldFile(ctx, scaffold{
		rel:      rulesetFile,
		perm:     filePerm,
		content:  []byte(content),
		force:    true,
		created:  fmt.Sprintf("Scaffolded declarative branch protection ruleset (%d required status checks derived from workflows)", len(contexts)),
		verified: "Existing branch protection ruleset verified present",
	})
	return err
}

// priorLabelTaxonomyDigests are the digests (priorRendering) of every label taxonomy adoption
// wrote before the current forge.DefaultLabelTaxonomy, keyed to what produced them. Only these
// texts are refreshed, in the file's own consistent line-ending style; testdata/labels
// reproduces each digest (ruleset_labels_test.go).
var priorLabelTaxonomyDigests = map[string]string{
	"458258424e4d1f9a0a3cb4c20de5f3c039b44c94a3296f1d0105a5305853ea8c": "fourteen labels, no document start",
}

// reconcileLabels writes the canonical label taxonomy sync also writes (forge.DefaultLabelTaxonomy)
// into a repository that has none. An existing taxonomy is the repository's configuration, so
// --force leaves it alone: it used to replace it with a shorter three-label copy. The one
// exception is an earlier taxonomy text adoption wrote and nobody edited: it holds the same
// labels and failed yamllint's default document-start rule (BUG-782), so it is refreshed.
func reconcileLabels(ctx context.Context, s *adoptSession) error {
	_, err := s.scaffoldFile(ctx, scaffold{
		rel:       labelsFile,
		perm:      filePerm,
		content:   forge.DefaultLabelTaxonomy(),
		force:     false,
		created:   "Scaffolded repository label taxonomy",
		verified:  "Existing repository label taxonomy preserved (repository configuration; --force does not replace it)",
		prior:     priorLabelTaxonomyDigests,
		refreshed: "Refreshed the unmodified earlier Praetor label taxonomy to the current text (same labels)",
	})
	return err
}

func reconcilePaperclip(ctx context.Context, s *adoptSession) error {
	plan, err := planHarness(ctx, s)
	if err != nil {
		return err
	}
	if plan.unresolved && (!plan.onDisk || s.opts.Force) {
		s.report.recordSkipped(paperclipFile, unresolvedHarnessNote(plan.onDisk))
		return nil
	}
	if plan.write == nil {
		s.report.recordReconciled(paperclipFile, "Existing Paperclip agent runtime harness verified present")
		return nil
	}
	if !s.opts.DryRun {
		if err := paperclip.WriteHarnessFiles(plan.write, s.repoPath, plan.rules); err != nil {
			return fmt.Errorf("write paperclip harness: %w", err)
		}
	}
	if plan.refresh {
		s.report.recordReconciled(paperclipFile, refreshedHarnessNote(plan.rules))
		return nil
	}
	s.report.recordCreated(paperclipFile, "Scaffolded Paperclip agent runtime harness and AGit rules")
	return nil
}

// unresolvedHarnessNote says why the paperclip step wrote nothing: the harness platform names
// the repository, and adoption found no identity to name.
func unresolvedHarnessNote(onDisk bool) string {
	action := "Paperclip harness not written"
	if onDisk {
		action = "Existing Paperclip harness kept as is, not compared with the current contract"
	}
	return action + ": its platform needs repository.owner and repository.name in " + manifestFile +
		" or an origin remote naming <owner>/<repo>; set them, or add the remote, and re-run"
}

func refreshedHarnessNote(rules bool) string {
	if rules {
		return "Refreshed unmodified earlier Praetor Paperclip harness and rules to the current contract text"
	}
	return "Refreshed unmodified earlier Praetor Paperclip harness to the current contract text; " +
		".paperclip/rules.md stays absent"
}

// generatedPersonas are the canonical personas adoption writes into an adopted repository.
// The context gate lints every canonical persona (#369), so text praetor generates here has
// to pass praetor's own lint. Both were prose until #380 and #381 reported the gate failing
// on personas nobody had edited. Keeping the set in one place is what lets
// TestGeneratedPersonasPassCavemanLint catch the next one before an adopter's push does, and
// TestGeneratedPersonasPassDefaultMarkdownlint hold both to markdownlint's default rules
// (BUG-806).
func generatedPersonas() []scaffold {
	return []scaffold{{
		rel:      auditorAgentFile,
		perm:     filePerm,
		content:  []byte(defaultAuditorAgentMD),
		force:    true,
		created:  "Scaffolded repository auditor agent definition",
		verified: "Existing repository auditor agent definition verified present",
		confined: true,
	}, {
		rel:      gatekeeperFile,
		perm:     filePerm,
		content:  []byte(defaultGatekeeperAgentMD),
		force:    true,
		created:  "Scaffolded repository gatekeeper agent definition",
		verified: "Existing repository gatekeeper agent definition verified present",
		confined: true,
	}}
}

// reconcileAgentDefinitions writes the canonical personas and projects them into the persona
// directory of every agent client agent_clients selects; the directories it leaves out are
// reported not applicable and never written.
func reconcileAgentDefinitions(ctx context.Context, s *adoptSession) error {
	personas := generatedPersonas()
	for i := 0; i < len(personas) && i < maxTranspileTargets; i++ {
		if _, err := s.scaffoldFile(ctx, personas[i]); err != nil {
			return err
		}
	}
	_, excluded, err := compiler.SelectPersonaDirs(ctx, s.repoPath)
	if err != nil {
		return fmt.Errorf("read agent_clients selection from %s: %w", manifestFile, err)
	}
	for i := 0; i < len(excluded) && i < maxTranspileTargets; i++ {
		s.report.recordNotApplicable(excluded[i], "Not selected by agent_clients in "+manifestFile)
	}
	if s.opts.DryRun {
		return nil
	}
	projected, err := compiler.CompileAgents(ctx, s.repoPath)
	if err != nil {
		return fmt.Errorf("compile agent definitions: %w", err)
	}
	for i := 0; i < len(projected) && i < maxTranspileTargets; i++ {
		s.report.recordCreated(projected[i].VendorTarget, "Projected canonical agent definition to vendor target")
	}
	return nil
}

const defaultAuditorAgentMD = `---
name: repo-auditor
description: "Autonomous agent for repository HISS invariant sweeps and praetorctl compliance."
mainAgent: true
subagent: true
commandExecutionPolicy: auto
---

# Repository Governance Auditor Persona

Authoritative repository governance auditor. Purpose: run autonomous sweeps
across codebases and git commits; guarantee 100% adherence to declared
standards.

## Execution Command

` + "```bash\npraetorctl audit\n```\n"

// defaultGatekeeperAgentMD runs the full gate, not a dry run: the persona's mission is the
// dependency verification, security scans and receipt that a dry run records as skipped.
const defaultGatekeeperAgentMD = `---
name: repo-gatekeeper
description: "Autonomous subagent for dependency verification, SCA security scans, isolated-worktree race tests, and Ed25519 Exit-0 receipt signing."
mainAgent: true
subagent: true
commandExecutionPolicy: auto
---

# Repository Gatekeeper Persona

Repository gatekeeper. Mission: enforce anti-direct-merge policy strictly;
verify every verification gate before shipping.

Gate stages: lockfiles + module prefetch, HISS ratchet, security scans
(govulncheck, gosec), flavor conformance, race tests in isolated worktree,
Ed25519 Exit-0 receipt. Verdict per stage: passed, failed, skipped,
not_applicable. Skipped != passed.

## Execution Command

` + "```bash\n" + gating.RepoRunCommand + "\n```\n" + `
Full gate; mints ` + "`" + gating.ReceiptFileName + "`" + `. Read-only preflight:
append ` + "`--dry-run`" + `; lockfiles, HISS scan, flavor only; mints nothing.
`
