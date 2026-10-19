// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/supplychain"
	"github.com/cordanaLLM/praetor/internal/util"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
)

// The REUSE gate adoption emits into a repository that declares its licensing the REUSE way
// (supplychain.ReuseDeclared: REUSE.toml or .reuse/dep5 at the root): the reuse-lint pre-commit job
// of lefthook.yml (reuseLintCommand) and the hosted workflow below, both at the one REUSE pin,
// supplychain.ReuseActionVersion. A repository with neither gets neither: reuse lint fails every
// tree that declares no licensing its way, so the gate would only block; a root that stops
// declaring REUSE loses both (migrateReuseSwitch, removeReuseGate), and so does one whose manifest
// declines reuse-gate (reuseGateApplies, #1244).

const (
	// reuseWorkflowFile is the hosted REUSE gate adoption writes.
	reuseWorkflowFile = ".github/workflows/reuse.yml"
	// reuseGateStep is the adoption step that writes it, the name adoption.decline takes.
	reuseGateStep = "reuse-gate"
	// pluranoHoristesLicenceAction is the reusable workflow path called in plurano-owned repositories.
	pluranoHoristesLicenceAction = forge.PluranoHoristesLicenceAction
	// pluranoHoristesLicenceWorkflowSHA is the commit SHA of plurano/horistes .github/workflows/licence.yml
	// pinned in the hosted REUSE gate for plurano-owned repositories (read 2026-10-19).
	pluranoHoristesLicenceWorkflowSHA = forge.PluranoHoristesLicenceWorkflowSHA
	// pluranoHoristesLicencePinDate names when pluranoHoristesLicenceWorkflowSHA was read.
	pluranoHoristesLicencePinDate = forge.PluranoHoristesLicencePinDate
	// pluranoOrgName is the organization name whose repositories use the reusable workflow caller.
	pluranoOrgName = "plurano"
)

// priorReuseWorkflowDigests are the digests (priorRendering) of every hosted REUSE gate a Praetor
// release wrote at reuseWorkflowFile, keyed to what produced it; the current rendering for the
// default branch main is one of them, so the release that changes it still refreshes this one
// without --force. The current rendering for any other default branch is recognised from the file
// itself (reuseRenderingBranch). testdata/reuse-workflow reproduces each digest
// (reuse_gate_test.go).
var priorReuseWorkflowDigests = map[string]string{
	"544d1a325cf241e5f03e4024176f5e46d50a555e5b82fa99976b1fa1cc600e7d": "reuse 6.x hash-pinned requirements.txt in venv, checkout pinned by commit digest, hosted gate shape",
	"2401b69ff8a083d79517191e2c5ed6f7ecee4cea7d90f4b7ae07f98c41e91c82": "reuse-action v6 and checkout pinned by commit digest, hosted gate shape with merge_group and concurrency group",
	"c8b2b85f7d8f3be75dc21737409a7583e159adca329ba91c9578f180ccf17b25": "reuse-action v6, checkout v7, hosted gate shape, default branch main, 10-minute timeout",
	"8edae714aaca03bb34c9069cf294950a566f65d92b5c076be3dee6c98537d210": "reuse-action v6, checkout v7, hosted gate shape with the draft step on bash, default branch main, 10-minute timeout",
	"c20f118f20de38aa547894de8b1ff070ea76619051f9cfabcb9a778905cb4840": "reuse-action v6, checkout v7, hosted gate shape with merge_group before its concurrency group",
	"b2af66a81dfc0edcab9f50f201b037b44307f8df15fefc838e69e002ad5a5a49": "reuse-action v6 and checkout pinned by Renovate from tag pins, hosted gate shape, default branch main, 10-minute timeout",
	"b5460afc9787f9149405b991d77d11664518d38baae5657e4cdb3b643630285e": "reuse-action v6 and checkout pinned by Renovate from tag pins, hosted gate shape with the draft step on bash, default branch main, 10-minute timeout",
	"b094b6973f76e0b817ce24b2b9dd8e92f5e2fa03617309287a6ae840853b3f7b": "reuse-action v6 and checkout pinned by commit digest, hosted gate shape with merge_group before its concurrency group",
}

// scanCheckoutPinnedRef extracts the pinned actions/checkout reference from workflow.
func scanCheckoutPinnedRef(workflow string) (string, error) {
	_, uses, err := util.ScanActionUses(workflow, managedasset.MaxWorkflowLines)
	if err != nil {
		return "", fmt.Errorf("scan workflow actions: %w", err)
	}
	for _, use := range uses {
		if use.Pin.Action == "actions/checkout" && use.Pinned {
			return use.Ref, nil
		}
	}
	return "", errors.New("workflow carries no pinned actions/checkout")
}

// reuseCheckoutRef returns the actions/checkout reference pinned by digest with its version comment,
// taking the SHA and version from markdownassets.Workflow, the same source praetor-docs.yml uses (HISS-19).
func reuseCheckoutRef() (string, error) {
	return scanCheckoutPinnedRef(markdownassets.Workflow)
}

// renderReuseWorkflow renders the hosted REUSE gate for the given default branch using
// the actions/checkout pinned reference scanned from workflow.
func renderReuseWorkflow(workflow, branch string) (string, error) {
	ref, err := scanCheckoutPinnedRef(workflow)
	if err != nil {
		return "", fmt.Errorf("render the hosted REUSE gate: %w", err)
	}
	return renderReuseWorkflowWithRef(ref, branch), nil
}

// renderReuseWorkflowWithRef renders the hosted REUSE gate using ref for actions/checkout.
func renderReuseWorkflowWithRef(ref, branch string) string {
	on := strings.Replace(ghworkflow.HostedGateOn,
		ghworkflow.HostedGatePushBranchesPrefix+ghworkflow.HostedGateDefaultBranch+"']",
		ghworkflow.HostedGatePushBranchesPrefix+branch+"']", 1)
	return "---\n" +
		"# REUSE licensing gate, written by praetorctl adopt because the repository\n" +
		"# carries REUSE.toml or LICENSES/. It runs reuse lint at the release line the\n" +
		"# reuse-lint pre-commit job of lefthook.yml requires.\n" +
		"name: REUSE\n" +
		"\n" +
		on +
		ghworkflow.HostedGateConcurrency +
		"\n" +
		"permissions:\n" +
		"  contents: read\n" +
		"\n" +
		"jobs:\n" +
		"  reuse:\n" +
		"    name: REUSE lint\n" +
		"    runs-on: ubuntu-26.04\n" +
		"    timeout-minutes: 10\n" +
		"    steps:\n" +
		ghworkflow.HostedGateDraftStep +
		"      - name: Check out the source" + ghworkflow.HostedGateStepIf + "\n" +
		"        # yamllint disable-line rule:line-length\n" +
		"        uses: " + ref + "\n" +
		"        with:\n" +
		"          persist-credentials: false\n" +
		"      - name: reuse lint" + ghworkflow.HostedGateStepIf + "\n" +
		"        run: |\n" +
		"          python3 -m venv \"$RUNNER_TEMP/praetor-reuse\"\n" +
		"          \"$RUNNER_TEMP/praetor-reuse/bin/python\" -m pip install \\\n" +
		"            --require-hashes -r .config/reuse/requirements.txt\n" +
		"          \"$RUNNER_TEMP/praetor-reuse/bin/reuse\" lint\n"
}

// reuseWorkflow renders the hosted REUSE gate for the repository's default branch, the branch
// the ruleset requiring its job protects: one job, on the runner the flavor workflows use, with a
// timeout, in the hosted gate shape every hosted gate shares (ghworkflow.HostedGateOn: pull
// request activity and a push to the default branch only; ghworkflow.HostedGateDraftStep first,
// so a draft run fails by design; ghworkflow.HostedGateStepIf on every later step). It checks the
// commit out without keeping the token and runs supplychain.ReuseActionPinnedRef, both pinned by
// full commit SHA with their version comment (HISS-11).
func reuseWorkflow(branch string) (string, error) {
	ref, err := reuseCheckoutRef()
	if err != nil {
		return "", fmt.Errorf("render the hosted REUSE gate: %w", err)
	}
	return renderReuseWorkflowWithRef(ref, branch), nil
}

// isPluranoOwner reports whether owner is "plurano" (case-insensitive).
func isPluranoOwner(owner string) bool {
	return strings.EqualFold(strings.TrimSpace(owner), pluranoOrgName)
}

// renderPluranoLicenceWorkflow renders the hosted REUSE gate as a reusable workflow caller
// of plurano/horistes/.github/workflows/licence.yml for the given default branch and commit SHA.
func renderPluranoLicenceWorkflow(branch, sha string) string {
	on := strings.Replace(ghworkflow.HostedGateOn,
		ghworkflow.HostedGatePushBranchesPrefix+ghworkflow.HostedGateDefaultBranch+"']",
		ghworkflow.HostedGatePushBranchesPrefix+branch+"']", 1)
	return "---\n" +
		"# REUSE licensing gate, written by praetorctl adopt because the repository\n" +
		"# carries REUSE.toml or LICENSES/. It calls plurano/horistes' licence workflow\n" +
		"# at the pinned commit.\n" +
		"name: REUSE\n" +
		"\n" +
		on +
		ghworkflow.HostedGateConcurrency +
		"\n" +
		"permissions:\n" +
		"  contents: read\n" +
		"\n" +
		"jobs:\n" +
		"  licence:\n" +
		"    name: Licence\n" +
		"    uses: " + pluranoHoristesLicenceAction + "@" + sha + "  # " + pluranoHoristesLicencePinDate + "\n"
}

// normalisePluranoCaller normalises any 40-hex commit SHA in a plurano licence caller workflow
// to pluranoHoristesLicenceWorkflowSHA and its canonical pin date comment for digest comparison.
// It returns the normalised text and whether a plurano licence caller was found.
// If the caller uses a non-40-hex ref (a tag, a branch, or a short SHA), it returns an error.
func normalisePluranoCaller(workflow string) (string, bool, error) {
	lines := strings.Split(strings.ReplaceAll(workflow, "\r\n", "\n"), "\n")
	if len(lines) > managedasset.MaxWorkflowLines {
		return "", false, fmt.Errorf("workflow exceeds %d lines", managedasset.MaxWorkflowLines)
	}
	found := false
	for i := 0; i < len(lines); i++ {
		val, ok := util.ActionUsesValue(lines[i])
		if !ok {
			continue
		}
		cleanVal := strings.Trim(val, `"'`)
		action, rest, hasAt := strings.Cut(cleanVal, "@")
		if strings.TrimSpace(action) != pluranoHoristesLicenceAction {
			continue
		}
		found = true
		ref, _, _ := strings.Cut(rest, "#")
		ref = strings.TrimSpace(ref)
		if !hasAt || !forge.IsFullCommitSHA(ref) {
			return "", false, fmt.Errorf("plurano licence caller %s: reference %q is not a 40-hex commit SHA", reuseWorkflowFile, val)
		}
		lines[i] = "    uses: " + pluranoHoristesLicenceAction + "@" + pluranoHoristesLicenceWorkflowSHA + "  # " + pluranoHoristesLicencePinDate
	}
	if !found {
		return "", false, nil
	}
	return strings.Join(lines, "\n"), true, nil
}

// reuseRenderingBranch returns the default branch data is the hosted REUSE gate rendered for
// (reuseWorkflow or renderPluranoLicenceWorkflow), in one consistent line-ending style, and whether
// data is such a rendering: Praetor's unedited output, for the current default branch or another one,
// such as before the branch was renamed.
func reuseRenderingBranch(data []byte) (string, bool, error) {
	_, rest, found := strings.Cut(string(data), ghworkflow.HostedGatePushBranchesPrefix)
	branch, _, closed := strings.Cut(rest, "']")
	if !found || !closed || !config.ValidBranchName(branch) {
		return "", false, nil
	}
	normalised, isCaller, err := normalisePluranoCaller(string(data))
	if err != nil {
		return "", false, err
	}
	if isCaller {
		expected := renderPluranoLicenceWorkflow(branch, pluranoHoristesLicenceWorkflowSHA)
		if equal, err := util.CanonicalTextEquivalent([]byte(normalised), []byte(expected)); err == nil && equal {
			return branch, true, nil
		}
		return "", false, nil
	}
	expected, err := reuseWorkflow(branch)
	if err != nil {
		return "", false, err
	}
	if equal, err := util.CanonicalTextEquivalent(data, []byte(expected)); err == nil && equal {
		return branch, true, nil
	}
	return "", false, nil
}

// isReuseRendering reports whether data is a hosted REUSE gate adoption wrote and nobody edited:
// an earlier rendering (priorReuseWorkflowDigests) or the current one for any default branch.
func isReuseRendering(data []byte) (bool, error) {
	_, current, err := reuseRenderingBranch(data)
	if err != nil {
		return false, err
	}
	return current || isPriorRendering(data, priorReuseWorkflowDigests), nil
}

// reuseDefaultBranch is the default branch the hosted REUSE gate is rendered for: the one the
// branch ruleset protects, resolved as forge.RenderRulesetForRepository resolves it.
func reuseDefaultBranch(ctx context.Context, repoPath string) (string, error) {
	branch, err := forge.RepositoryDefaultBranch(ctx, repoPath, nil)
	if err != nil {
		return "", fmt.Errorf("render the hosted REUSE gate %s: %w", reuseWorkflowFile, err)
	}
	return branch, nil
}

// reconcileReuseGate writes the hosted REUSE gate (reuseWorkflow) into a repository that declares
// REUSE, rendered for its default branch, and removes Praetor's rendering from one that does not
// (removeReuseGate). An existing gate holding an earlier Praetor rendering, or the current one for
// another default branch, is refreshed; any other file there is the repository's and is kept,
// --force included, with a warning.
// decideReuseRemoval resolves the reason the hosted REUSE gate is removed.
func (s *adoptSession) decideReuseRemoval(ctx context.Context) (reuseRemoval, error) {
	if declined, err := ArtifactDeclined(s.declined, reuseGateStep); err != nil {
		return reuseNotDeclared, fmt.Errorf("check declined artifacts: %w", err)
	} else if declined {
		return reuseGateDeclined, nil
	}
	declared, err := supplychain.ReuseDeclared(ctx, s.repoPath)
	if err != nil {
		return reuseNotDeclared, fmt.Errorf("check REUSE declared: %w", err)
	}
	if declared {
		return reuseJobDeclared, nil
	}
	return reuseNotDeclared, nil
}

func (s *adoptSession) reconcileInactiveReuseGate(ctx context.Context) error {
	notice, err := supplychain.ReuseHeadersOnlyNotice(ctx, s.repoPath)
	if err != nil {
		return fmt.Errorf("decide the hosted REUSE gate: %w", err)
	}
	if notice != "" {
		s.report.addWarning("%s", notice)
	}
	reason, err := s.decideReuseRemoval(ctx)
	if err != nil {
		return err
	}
	if err := s.removeReuseGate(ctx, reason); err != nil {
		return err
	}
	return s.removeReuseRequirements(ctx)
}

func (s *adoptSession) repoOwner() string {
	if s.identity.owner != "" {
		return s.identity.owner
	}
	if s.policy != nil && s.policy.Manifest != nil {
		return s.policy.Manifest.Repository.Owner
	}
	return ""
}

func (s *adoptSession) reuseWorkflow(branch string) (string, error) {
	if isPluranoOwner(s.repoOwner()) {
		return renderPluranoLicenceWorkflow(branch, pluranoHoristesLicenceWorkflowSHA), nil
	}
	return reuseWorkflow(branch)
}

func (s *adoptSession) activePluranoWorkflow(ctx context.Context, branch, defaultWorkflow string) (string, error) {
	existing, exists, err := contextopt.ObserveSnapshotIn(ctx, s.repoPath, reuseWorkflowFile)
	if err != nil || !exists {
		return defaultWorkflow, err
	}
	normalised, isCaller, err := normalisePluranoCaller(string(existing))
	if err != nil {
		return "", err
	}
	if !isCaller {
		return defaultWorkflow, nil
	}
	expected := renderPluranoLicenceWorkflow(branch, pluranoHoristesLicenceWorkflowSHA)
	if equal, err := util.CanonicalTextEquivalent([]byte(normalised), []byte(expected)); err == nil && equal {
		return string(existing), nil
	}
	return defaultWorkflow, nil
}

func (s *adoptSession) scaffoldActiveReuseGate(ctx context.Context, branch string) error {
	prior, err := s.reusePriorDigests(ctx)
	if err != nil {
		return err
	}
	workflow, err := s.reuseWorkflow(branch)
	if err != nil {
		return err
	}
	if isPluranoOwner(s.repoOwner()) {
		workflow, err = s.activePluranoWorkflow(ctx, branch, workflow)
		if err != nil {
			return err
		}
	}
	_, err = s.scaffoldFile(ctx, scaffold{
		rel:       reuseWorkflowFile,
		perm:      filePerm,
		content:   []byte(workflow),
		created:   "Scaffolded the hosted REUSE gate: reuse lint on " + branch,
		verified:  "Existing hosted REUSE gate verified present",
		prior:     prior,
		refreshed: "Refreshed an unedited earlier Praetor REUSE gate to the current rendering for " + branch,
	})
	if err != nil {
		return err
	}
	_, err = s.scaffoldFile(ctx, scaffold{
		rel:       reuseRequirementsFile,
		perm:      filePerm,
		content:   []byte(reuseRequirementsTxt),
		created:   "Scaffolded the REUSE requirements lock: reuse==6.2.0 hash-pinned",
		verified:  "Existing REUSE requirements lock verified present",
		refreshed: "Refreshed the REUSE requirements lock",
	})
	return err
}

func reconcileReuseGate(ctx context.Context, s *adoptSession) error {
	applies, err := s.reuseGateApplies(ctx)
	if err != nil {
		return err
	}
	if !applies {
		return s.reconcileInactiveReuseGate(ctx)
	}
	branch, err := reuseDefaultBranch(ctx, s.repoPath)
	if err != nil {
		return err
	}
	return s.scaffoldActiveReuseGate(ctx, branch)
}

// reusePriorDigests is the earlier-text set of the hosted REUSE gate scaffold (scaffold.prior):
// priorReuseWorkflowDigests, and the digest of the file at reuseWorkflowFile when it holds the
// current rendering for another default branch (reuseRenderingBranch).
func (s *adoptSession) reusePriorDigests(ctx context.Context) (map[string]string, error) {
	existing, exists, err := contextopt.ObserveSnapshotIn(ctx, s.repoPath, reuseWorkflowFile)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", reuseWorkflowFile, err)
	}
	prior := maps.Clone(priorReuseWorkflowDigests)
	if exists {
		branch, rendered, err := reuseRenderingBranch(existing)
		if err != nil {
			return nil, err
		}
		if rendered {
			digest, _, err := util.CanonicalTextDigest(existing)
			if err != nil {
				return nil, fmt.Errorf("digest %s: %w", reuseWorkflowFile, err)
			}
			prior[digest] = "the current rendering for default branch " + branch
		}
	}
	if isPluranoOwner(s.repoOwner()) {
		if digest, branch, ok := copiedReuseDigest(ctx, s.repoPath); ok {
			prior[digest] = "copied REUSE gate for default branch " + branch
		}
	}
	return prior, nil
}

// copiedReuseDigest identifies the older copied gate rendering when its default
// branch, workflow, and canonical digest can all be resolved.
func copiedReuseDigest(ctx context.Context, repoPath string) (string, string, bool) {
	branch, err := reuseDefaultBranch(ctx, repoPath)
	if err != nil {
		return "", "", false
	}
	copied, err := reuseWorkflow(branch)
	if err != nil {
		return "", "", false
	}
	digest, _, err := util.CanonicalTextDigest([]byte(copied))
	return digest, branch, err == nil
}

// reuseRemoval is why adoption removes the hosted REUSE gate: the root no longer declares REUSE,
// or adoption.decline names reuseGateStep (#1244).
type reuseRemoval int

const (
	// reuseNotDeclared: the root does not declare REUSE (supplychain.ReuseDeclared).
	reuseNotDeclared reuseRemoval = iota
	// reuseGateDeclined: adoption.decline names reuseGateStep.
	reuseGateDeclined
	// reuseJobDeclared: the repository already declares its own REUSE lint job (#1327).
	reuseJobDeclared
)

// reuseGateApplies reports whether this run emits the REUSE gate, the hosted workflow and the
// reuse-lint job of lefthook.yml alike: the root declares REUSE (supplychain.ReuseDeclared) and
// adoption.decline does not name reuseGateStep. A declined step takes both with it (#1244), so a
// repository whose LICENSES/ holds third-party texts can opt out of a gate that cannot pass there
// without declining every hook.
func (s *adoptSession) reuseGateApplies(ctx context.Context) (bool, error) {
	declined, err := ArtifactDeclined(s.declined, reuseGateStep)
	if err != nil {
		return false, fmt.Errorf("resolve adoption.decline for the REUSE gate: %w", err)
	}
	if declined {
		return false, nil
	}
	declared, err := supplychain.ReuseDeclared(ctx, s.repoPath)
	if err != nil {
		return false, fmt.Errorf("decide the REUSE gate: %w", err)
	}
	if !declared {
		return false, nil
	}
	existing, err := adopterDeclaresReuseJob(ctx, s.repoPath)
	if err != nil {
		return false, fmt.Errorf("check existing REUSE jobs: %w", err)
	}
	return !existing, nil
}

// removeDeclinedReuseGate is what a declined reuse-gate step does (declinedRemovals): it removes
// Praetor's unedited hosted REUSE gate an earlier adoption wrote, as removeReuseGate does for a
// root that stopped declaring REUSE; the git-hooks step drops the reuse-lint job
// (reuseGateApplies).
func removeDeclinedReuseGate(ctx context.Context, s *adoptSession) error {
	return s.removeReuseGate(ctx, reuseGateDeclined)
}

// removeReuseGate removes the hosted REUSE gate when the file is Praetor's unedited rendering
// (isReuseRendering) and the root no longer declares REUSE, where reuse lint fails every tree so
// the gate, a required check of the ruleset derived from it, would block every pull request, or the
// manifest declines the step (reason). The removal is bound to the bytes read, so a file edited in
// between is kept. An edited gate is the repository's: kept, with a warning naming why it would
// have gone. A dry run records the removal it would make.
func (s *adoptSession) warnKeptUnrenderedReuseGate(reason reuseRemoval) {
	switch reason {
	case reuseGateDeclined:
		s.report.addWarning("%s kept: adoption.decline names %s, but the file is edited, so adoption does not remove it: "+
			"remove it yourself, or drop %s from adoption.decline", reuseWorkflowFile, reuseGateStep, reuseGateStep)
	case reuseJobDeclared:
		// Repo declares its own job; edited reuse.yml kept without warning.
	case reuseNotDeclared:
		s.report.addWarning("%s kept: the root carries no %s, so its REUSE lint job fails every run and, while the "+
			"branch ruleset requires it, every pull request; it is edited, so adoption does not remove it: remove it, or add %s",
			reuseWorkflowFile, supplychain.ReuseDeclarationFiles(), supplychain.ReuseFile)
	}
}

func reuseRemovalDetail(reason reuseRemoval) string {
	switch reason {
	case reuseGateDeclined:
		return "Removed the hosted REUSE gate: adoption.decline names " + reuseGateStep
	case reuseJobDeclared:
		return "Removed the hosted REUSE gate: the repository already declares its own REUSE lint job"
	default:
		return "Removed the hosted REUSE gate: the root carries no " + supplychain.ReuseDeclarationFiles() +
			", so reuse lint would fail every pull request"
	}
}

// removeReuseGate is what an inactive hosted REUSE gate does: it removes Praetor's unedited
// rendering (isReuseRendering) and the root no longer declares REUSE, where reuse lint fails every tree so
// keeping the check would break every PR. The gap between an earlier rendering and the current one is
// crossed by an adopt run (migrateReuseSwitch); until then, a gate holding a text this binary knows in
// between is kept. An edited gate is the repository's: kept, with a warning naming why it would
// have gone. A dry run records the removal it would make.
func (s *adoptSession) removeReuseGate(ctx context.Context, reason reuseRemoval) error {
	full, err := repoFile(s.repoPath, reuseWorkflowFile)
	if err != nil {
		return err
	}
	actual, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", reuseWorkflowFile, err)
	}
	if !exists {
		return nil
	}
	rendering, err := isReuseRendering(actual)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", reuseWorkflowFile, err)
	}
	if !rendering {
		s.warnKeptUnrenderedReuseGate(reason)
		return nil
	}
	if !s.opts.DryRun {
		if err := contextopt.RemoveSnapshot(ctx, full, actual); err != nil {
			return fmt.Errorf("remove %s: %w", reuseWorkflowFile, err)
		}
	}
	s.planDryRunRemoval(reuseWorkflowFile)
	s.report.recordReconciledAs(reuseWorkflowFile, actionRemove, reuseRemovalDetail(reason))
	return nil
}

// removeReuseRequirements removes the managed REUSE requirements lock when it matches
// reuseRequirementsTxt and the REUSE gate does not apply.
func (s *adoptSession) removeReuseRequirements(ctx context.Context) error {
	full, err := repoFile(s.repoPath, reuseRequirementsFile)
	if err != nil {
		return err
	}
	actual, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil || !exists {
		return err
	}
	if string(actual) != reuseRequirementsTxt {
		return nil
	}
	if !s.opts.DryRun {
		if err := contextopt.RemoveSnapshot(ctx, full, actual); err != nil {
			return fmt.Errorf("remove %s: %w", reuseRequirementsFile, err)
		}
	}
	s.planDryRunRemoval(reuseRequirementsFile)
	s.report.recordReconciledAs(reuseRequirementsFile, actionRemove, "Removed the REUSE requirements lock: REUSE gate is not active")
	return nil
}

func reuseRenderingAllowed(ctx context.Context, repoPath string) (bool, error) {
	existing, exists, err := contextopt.ObserveSnapshotIn(ctx, repoPath, reuseWorkflowFile)
	if err != nil {
		return false, err
	}
	if !exists {
		return true, nil
	}
	return isReuseRendering(existing)
}

// plannedReuseWorkflow lists the hosted REUSE gate when this run leaves it as adoption's
// rendering: the step is not declined, the root declares REUSE, and the file is absent or holds
// a Praetor rendering, current or earlier, for any default branch (isReuseRendering). A file it cannot read is not claimed: the harness
// under-claims rather than credit a gate the repository may not get.
func (s *adoptSession) plannedReuseWorkflow(ctx context.Context) ([]flavor.PlannedTemplate, error) {
	applies, err := s.reuseGateApplies(ctx)
	if err != nil || !applies {
		return nil, err
	}
	branch, err := reuseDefaultBranch(ctx, s.repoPath)
	if err != nil {
		return nil, err
	}
	allowed, err := reuseRenderingAllowed(ctx, s.repoPath)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, nil
	}
	workflow, err := s.reuseWorkflow(branch)
	if err != nil {
		return nil, err
	}
	return []flavor.PlannedTemplate{{Path: reuseWorkflowFile, Content: workflow}}, nil
}

// reuseManagedPaths returns the hosted REUSE gate path when the repository declares REUSE,
// the reuse-gate step is not declined, and the file is absent or a Praetor rendering, so
// adoption includes it in the Renovate packageRules entry that disables updates on
// Praetor-managed files. --force does not count: it regenerates only audit-locked scaffolds,
// and the REUSE gate is not one, so an edited or adopter-owned copy is kept and stays
// under the adopter's Renovate. For plurano-owned repositories, the caller is not managed so
// Renovate's github-actions manager updates its commit pin.
func reuseManagedPaths(ctx context.Context, s *adoptSession) ([]string, error) {
	applies, err := s.reuseGateApplies(ctx)
	if err != nil || !applies {
		return nil, err
	}
	var managed []string
	if !isPluranoOwner(s.repoOwner()) {
		data, exists, err := observeAdoptionInput(ctx, s, reuseWorkflowFile)
		if err != nil {
			_, err = uninspectableReason(ctx, err)
			return nil, err
		}
		if exists {
			rendering, err := isReuseRendering(data)
			if err != nil {
				return nil, err
			}
			if !rendering {
				return nil, nil
			}
		}
		managed = append(managed, reuseWorkflowFile)
	}
	reqData, reqExists, err := observeAdoptionInput(ctx, s, reuseRequirementsFile)
	if err != nil {
		_, err = uninspectableReason(ctx, err)
		return nil, err
	}
	if !reqExists || string(reqData) == reuseRequirementsTxt {
		managed = append(managed, reuseRequirementsFile)
	}
	return managed, nil
}

// migrateReuseSwitch writes target's rendering over existing when existing is the current
// rendering for the other REUSE switch (lefthookShape.otherReuse): adoption wrote it before the
// root gained or lost its REUSE declaration (supplychain.ReuseDeclared), or the manifest declined
// or stopped declining reuse-gate. The checkpoint jobs it carries are kept, as
// writeLefthookConfig keeps them on the current rendering. It reports whether it wrote.
func (s *adoptSession) migrateReuseSwitch(target lefthookTarget, existing []byte) (bool, error) {
	switched := matchCurrentLefthook(existing, target.shape.otherReuse())
	if !switched.found {
		return false, nil
	}
	declined, err := ArtifactDeclined(s.declined, reuseGateStep)
	if err != nil {
		return false, fmt.Errorf("resolve adoption.decline for the reuse-lint job: %w", err)
	}
	rendering := buildLefthookYAMLFor(target.shape, target.checkpoint || switched.checkpoint)
	return true, s.migrateLefthookConfig(rendering, reuseJobMigration(target.shape, declined))
}

// reuseJobMigration is the report detail of a lefthook.yml migrated to the rendering for the
// other REUSE switch (lefthookShape.otherReuse); declined says adoption.decline names
// reuseGateStep, which is then why the job goes.
func reuseJobMigration(shape lefthookShape, declined bool) string {
	if shape.reuse {
		return "Added the reuse-lint job to the Praetor-generated Lefthook configuration: the root carries " +
			supplychain.ReuseDeclarationFiles()
	}
	if declined {
		return "Removed the reuse-lint job from the Praetor-generated Lefthook configuration: adoption.decline names " +
			reuseGateStep
	}
	return "Removed the reuse-lint job from the Praetor-generated Lefthook configuration: the root carries no " +
		supplychain.ReuseDeclarationFiles()
}
