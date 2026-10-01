package adopt

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/paperclip"
)

const (
	rulesetFile   = forge.RepositoryRulesetPath
	labelsFile    = ".config/labels.yaml"
	paperclipFile = ".paperclip/harness.json"
	// paperclipRulesFile is the AGit rules page paperclip.WriteHarnessFiles renders beside the
	// harness.
	paperclipRulesFile = ".paperclip/rules.md"
	auditorAgentFile   = ".agents/agents/repo-auditor.md"
	gatekeeperFile     = ".agents/agents/repo-gatekeeper.md"
)

func adoptionBranchPolicy(ctx context.Context, s *adoptSession) (config.BranchProtectionPolicy, error) {
	if s.policy != nil {
		return s.policy.Policy.BranchProtection, nil
	}
	// A real run renders only from the effective policy: built-in defaults drop what the
	// declared profiles and facets require (#603).
	if !s.opts.DryRun {
		return config.BranchProtectionPolicy{}, errors.New("render the branch ruleset: the effective policy is unresolved")
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

// reconcileBranchRuleset writes the ruleset forge.RenderRulesetForRepository renders under the
// adoption's branch protection policy, the rendering flavor apply writes too. The ruleset that
// was current for the repository as this adoption found it (s.rulesetBaseline,
// forge.PriorRulesetDigests), such as the one flavor apply wrote under the built-in policy before
// adoption pinned one, is refreshed without --force. Any other one that differs, a rendering with
// one value edited included, is the repository's: kept and warned about. --force replaces it only
// while audit compares the ruleset, that is while the policy requires one (rulesetRequired);
// under any other policy audit never reads it, so --force keeps it too.
//
// A dry run writes nothing and previews the file instead (AdoptReport.Previews): the create,
// update, unchanged or keep it would come to, with the rendered ruleset or the diff from the one
// on disk. It renders over the workflows the run would have written by now (previewWorkflows),
// so it previews the checks the run requires.
func reconcileBranchRuleset(ctx context.Context, s *adoptSession) error {
	policy, err := adoptionBranchPolicy(ctx, s)
	if err != nil {
		return err
	}
	planned, err := s.previewWorkflows(ctx)
	if err != nil {
		return err
	}
	content, contexts, err := forge.RenderRulesetForRepository(ctx, s.repoPath, policy, planned)
	if err != nil {
		return err
	}
	checks := len(contexts)
	return s.scaffoldPreviewed(ctx, scaffold{
		rel:         rulesetFile,
		perm:        filePerm,
		content:     content,
		auditLocked: rulesetRequired(policy),
		created:     fmt.Sprintf("Scaffolded declarative branch protection ruleset (%d required status checks derived from workflows)", checks),
		verified:    "Existing branch protection ruleset verified present",
		prior:       s.priorRulesetDigests(content),
		refreshed:   fmt.Sprintf("Refreshed the unedited earlier Praetor branch protection ruleset to the current policy and workflows (%d required status checks)", checks),
	}, rulesetPreviewNote(checks))
}

// readRulesetBaseline reads, before any step writes, what the branch-ruleset step compares an
// existing ruleset against (forge.ReadRulesetBaseline): the policy and the workflows of the
// repository as this adoption found it. Only a repository that carries a ruleset needs it. One
// that cannot be read is warned about and leaves no earlier rendering, so a ruleset that differs
// is kept, never refreshed.
func readRulesetBaseline(ctx context.Context, s *adoptSession) *forge.RulesetBaseline {
	full, err := repoFile(s.repoPath, rulesetFile)
	if err != nil || !fileExists(full) {
		return nil
	}
	baseline, err := forge.ReadRulesetBaseline(ctx, s.repoPath)
	if err != nil {
		s.report.addWarning("%s: the policy and workflows it was rendered for could not be read (%v); "+
			"a ruleset that differs is kept, not refreshed", rulesetFile, err)
		return nil
	}
	return &baseline
}

// resolveDefaultBranch reads, before any step writes, the default branch only this checkout
// records (forge.DefaultBranchToDeclare): an origin HEAD that is not main, which a CI checkout
// without it would not resolve. Without a manifest, the one the manifest step creates declares it
// (declaredAdoptionManifest), so every checkout renders and audits the ruleset for the same
// branch. An existing manifest that declares none is never rewritten and is warned about instead.
// A manifest that declares the branch, or declines the branch-ruleset step, needs no read.
func (s *adoptSession) resolveDefaultBranch(ctx context.Context, declared *config.Manifest) error {
	if declared != nil && (declared.Repository.DefaultBranch != "" || s.declines("branch-ruleset")) {
		return nil
	}
	branch, err := forge.DefaultBranchToDeclare(ctx, s.repoPath)
	if err != nil {
		return fmt.Errorf("read the default branch to declare: %w", err)
	}
	if declared == nil {
		s.defaultBranch = branch
		return nil
	}
	if branch != "" {
		s.report.addWarning("%s declares no repository.default_branch: this checkout's origin HEAD names %s, so the "+
			"ruleset renders for %s here, but a checkout without it (CI usually has none) renders %s and the audit "+
			"reports drift. Declare repository.default_branch: %s in %s; adoption never rewrites an existing manifest",
			manifestFile, branch, branch, forge.FallbackDefaultBranch, branch, manifestFile)
	}
	return nil
}

// priorRulesetDigests is the earlier-text set of the ruleset scaffold (scaffold.prior): the
// digest of the ruleset current for the repository as this adoption found it, unless that is
// content, or none without a baseline.
func (s *adoptSession) priorRulesetDigests(content []byte) map[string]string {
	if s.rulesetBaseline == nil {
		return nil
	}
	return forge.PriorRulesetDigests(*s.rulesetBaseline, content)
}

// rulesetPreviewNote says where a previewed ruleset's status checks come from: the workflows on
// disk with the ones the run writes or removes before its branch-ruleset step applied over them.
func rulesetPreviewNote(contexts int) string {
	return fmt.Sprintf("%d required status checks derived from the workflows on disk "+
		"and those this adoption writes or removes before the ruleset step", contexts)
}

// priorLabelTaxonomyDigests are the digests (priorRendering) of every label taxonomy adoption
// wrote before the current forge.DefaultLabelTaxonomy, keyed to what produced them. Only these
// texts are refreshed, in the file's own consistent line-ending style; testdata/labels
// reproduces each digest (ruleset_labels_test.go).
var priorLabelTaxonomyDigests = map[string]string{
	"458258424e4d1f9a0a3cb4c20de5f3c039b44c94a3296f1d0105a5305853ea8c": "fourteen labels, no document start",
	"2b6660c577b5860002a6125b1ddf805653207b7d371cba2751c989ca1b953971": "fourteen labels, hiss-waiver described as signed",
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
		created:   "Scaffolded repository label taxonomy",
		verified:  "Existing repository label taxonomy preserved (repository configuration; --force does not replace it)",
		prior:     priorLabelTaxonomyDigests,
		refreshed: "Refreshed the unmodified earlier Praetor label taxonomy to the current text (same labels)",
	})
	return err
}

// reconcilePaperclip leaves the harness planHarness planned. It writes a synthesis only where
// none exists or the existing one is unmodified earlier output. An operator-owned harness is
// kept, under --force too; --force sets only a platform naming another repository.
func reconcilePaperclip(ctx context.Context, s *adoptSession) error {
	plan, err := planHarness(ctx, s)
	if err != nil {
		return err
	}
	if plan.unresolved && (!plan.onDisk || s.opts.Force) {
		s.report.recordSkipped(paperclipFile, unresolvedHarnessNote(plan.onDisk))
		return nil
	}
	if plan.patched() {
		return s.patchHarnessPlatform(ctx, plan)
	}
	if plan.write == nil {
		s.recordKeptHarness(plan)
		return nil
	}
	if !s.opts.DryRun {
		if err := paperclip.WriteHarnessFiles(plan.write, s.repoPath, plan.rules); err != nil {
			return fmt.Errorf("write paperclip harness: %w", err)
		}
	}
	s.recordHarnessWrite(plan)
	return nil
}

// recordHarnessWrite records the harness a plan writes and, when it writes rules.md too
// (harnessPlan.rules), that file under its own path: refreshed beside a refreshed harness,
// created beside a new one. rules.md used to go unnamed, so neither run listed it (#366).
func (s *adoptSession) recordHarnessWrite(plan harnessPlan) {
	if plan.refresh {
		s.report.recordReconciled(paperclipFile, refreshedHarnessNote(plan.rules))
	} else {
		s.report.recordCreated(paperclipFile, "Scaffolded Paperclip agent runtime harness")
	}
	switch {
	case !plan.rules:
	case plan.refresh:
		s.report.recordReconciled(paperclipRulesFile, "Refreshed the AGit rules rendered from the unmodified earlier Praetor harness")
	default:
		s.report.recordCreated(paperclipRulesFile, "Scaffolded AGit rules rendered from the Paperclip harness")
	}
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

// patchHarnessPlatform writes the operator-owned harness with only its platform set, bound to
// the bytes the plan read, and records it as replaced with its line delta and backup
// (replaceExisting). rules.md is not touched: it is generated text audit does not verify, so it
// keeps whatever heading it has.
func (s *adoptSession) patchHarnessPlatform(ctx context.Context, plan harnessPlan) error {
	return s.replaceExisting(ctx, replacement{
		rel: paperclipFile, before: plan.owned, after: plan.data,
		detail: "Set platform of the operator-owned Paperclip harness to " + plan.platform +
			" (--force); every other value kept, .paperclip/rules.md untouched",
		publish: func(ctx context.Context) error {
			options := contextopt.ReplaceOptions{Expected: plan.owned, Exists: true, Mode: filePerm}
			if err := contextopt.ReplaceSnapshotIn(ctx, s.repoPath, filepath.FromSlash(paperclipFile), plan.data, options); err != nil {
				return fmt.Errorf("set paperclip harness platform: %w", err)
			}
			return nil
		},
	})
}

// recordKeptHarness reports an existing harness this run leaves as it is: verified when it is
// the current synthesis, kept when it is operator-owned. A kept harness whose platform names
// another repository fails audit, so a plain run warns with the --force that sets it, and a
// run that could not compare or set it, plain or --force, warns with the reason.
func (s *adoptSession) recordKeptHarness(plan harnessPlan) {
	if plan.owned == nil {
		s.report.recordReconciled(paperclipFile, "Existing Paperclip agent runtime harness verified present")
		return
	}
	s.report.recordReconciled(paperclipFile, "Existing operator-owned Paperclip harness kept as written: "+
		"it is neither the current contract nor unmodified earlier Praetor output, so --force keeps it too; "+
		"delete it and re-run adopt to regenerate it")
	if plan.platform != "" {
		s.report.addWarning("%s: platform names another repository; audit expects %q. "+
			"Re-run %s to set platform and keep every other value", paperclipFile, plan.platform, s.forceCommand())
	}
	if plan.unpatched != "" {
		s.report.addWarning("%s: platform not checked or set, harness kept as written: %s", paperclipFile, plan.unpatched)
	}
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
//
// Audit does not compare a persona with its scaffold, so an edited persona is kept, --force
// included. An unedited earlier text of it (priorPersonaDigests) is refreshed on a plain run,
// through the root-pinned writer compile-context uses.
func generatedPersonas() []scaffold {
	return []scaffold{{
		rel:       auditorAgentFile,
		perm:      filePerm,
		content:   []byte(defaultAuditorAgentMD),
		created:   "Scaffolded repository auditor agent definition",
		verified:  "Existing repository auditor agent definition verified present",
		refreshed: "Refreshed the unedited earlier Praetor repository auditor agent definition to the current text",
		confined:  true,
		prior:     priorPersonaDigests[auditorAgentFile],
	}, {
		rel:       gatekeeperFile,
		perm:      filePerm,
		content:   []byte(defaultGatekeeperAgentMD),
		created:   "Scaffolded repository gatekeeper agent definition",
		verified:  "Existing repository gatekeeper agent definition verified present",
		refreshed: "Refreshed the unedited earlier Praetor repository gatekeeper agent definition to the current text",
		confined:  true,
		prior:     priorPersonaDigests[gatekeeperFile],
	}}
}

// priorPersonaDigests are the digests (priorRendering) of every text a Praetor release wrote
// at each canonical persona path, keyed by path and then to what produced it; the current texts
// are among them. testdata/personas reproduces each digest, and
// TestPriorPersonaDigests_Boundary_CurrentTextsRecorded fails until a changed persona is
// recorded here (persona_prior_test.go).
var priorPersonaDigests = map[string]map[string]string{
	auditorAgentFile: {
		"822b8f7f7915257a41ceaa8f041d0678243e7b0b3c4612add77a24eac3ad82f8": "standardsctl command, prose body",
		"13559cb2cf69de10840e16e36bb448c156beb29eec92b3bd8f6dde26f9441c50": "praetorctl command, prose body",
		"fac622020b262d4de75239f0d806ad4ea8321e4f4283bc825db88719ab3f7aeb": "caveman body on one line",
		"759798422574a1a7f203b7015a168b1222032cf2cd42457ae853c76336de88f4": "caveman body wrapped, fence set off",
	},
	gatekeeperFile: {
		"ee9540a57db9eabaf87a85c89a6f3446a89380d764d247c54aba7ff578fa0342": "standardsctl dry-run command, prose body",
		"ccbb8a2ed9189359e34c4c2a871d6366789c3362adce910f7acc87ce5e28c76b": "praetorctl dry-run command, prose body",
		"aff586a20c8a635f7caa3558631b16500c1f1301f7c066154d99e69512c76926": "praetorctl dry-run command, caveman body",
		"848fcfc4156a8f6b0a41aa777d2ec6296705a9ef810d5cec50a950280abcff2d": "full gate command, stages on one line",
		"bb05de9e87afa9c39af2f9fcbac3f51af2e6431ccc54f45b27072ae58b91fb71": "full gate command, body wrapped, fence set off",
	},
}

// reconcileAgentDefinitions writes the canonical personas and projects them into the persona
// directory of every agent client agent_clients selects; the directories it leaves out are
// reported not applicable and never written. A dry run writes nothing and records every copy
// the real run projects (projectAgentSurfaces); it used to stop before the copies, so the
// preview never named them nor the hand-edited ones a forced run replaces (#366).
func reconcileAgentDefinitions(ctx context.Context, s *adoptSession) error {
	// Read before the canonical personas below are refreshed: the copies an unedited earlier
	// run left are the copies of the canonical personas as this run found them.
	prior, err := priorAgentSurfaces(ctx, s.repoPath)
	if err != nil {
		return err
	}
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
	return projectAgentSurfaces(ctx, s, prior)
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
