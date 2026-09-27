package adopt

import (
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/paperclip"
	"github.com/cordanaLLM/praetor/internal/util"
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

func requiredStatusContexts(repoPath string) ([]string, error) {
	return forge.RequiredStatusContexts(context.Background(), repoPath)
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

// reconcileLabels writes the canonical label taxonomy sync also writes (forge.DefaultLabelTaxonomy)
// into a repository that has none. An existing taxonomy is the repository's configuration, so
// --force leaves it alone: it used to replace it with a shorter three-label copy.
func reconcileLabels(ctx context.Context, s *adoptSession) error {
	_, err := s.scaffoldFile(ctx, scaffold{
		rel:      labelsFile,
		perm:     filePerm,
		content:  forge.DefaultLabelTaxonomy(),
		force:    false,
		created:  "Scaffolded repository label taxonomy",
		verified: "Existing repository label taxonomy preserved (repository configuration; --force does not replace it)",
	})
	return err
}

func reconcilePaperclip(ctx context.Context, s *adoptSession) error {
	full, err := repoFile(s.repoPath, paperclipFile)
	if err != nil {
		return err
	}
	exists := fileExists(full)
	if exists && !s.opts.Force {
		s.report.recordReconciled(paperclipFile, "Existing Paperclip agent runtime harness verified present")
		return nil
	}
	harness, err := paperclip.SynthesizeHarness(ctx, s.repoPath)
	if errors.Is(err, util.ErrRepoIdentityUnresolved) {
		s.report.recordSkipped(paperclipFile, unresolvedHarnessNote(exists))
		return nil
	}
	if err != nil {
		return fmt.Errorf("synthesize paperclip harness: %w", err)
	}
	if !s.opts.DryRun {
		if err := paperclip.WriteHarness(harness, s.repoPath); err != nil {
			return fmt.Errorf("write paperclip harness: %w", err)
		}
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
