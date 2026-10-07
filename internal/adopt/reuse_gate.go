// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/supplychain"
	"github.com/cordanaLLM/praetor/internal/util"
)

// The REUSE gate adoption emits into a repository that declares its licensing the REUSE way
// (supplychain.ReuseDeclared: REUSE.toml or LICENSES/ at the root): the reuse-lint pre-commit job
// of lefthook.yml (reuseLintCommand) and the hosted workflow below, both at the one REUSE pin,
// supplychain.ReuseActionVersion. A repository with neither gets neither: reuse lint fails every
// tree that declares no licensing its way, so the gate would only block; a root that stops
// declaring REUSE loses both (migrateReuseSwitch, removeReuseGate).

const (
	// reuseWorkflowFile is the hosted REUSE gate adoption writes.
	reuseWorkflowFile = ".github/workflows/reuse.yml"
	// reuseGateStep is the adoption step that writes it, the name adoption.decline takes.
	reuseGateStep = "reuse-gate"
)

// priorReuseWorkflowDigests are the digests (priorRendering) of every hosted REUSE gate a Praetor
// release wrote at reuseWorkflowFile, keyed to what produced it; the current rendering for the
// default branch main is one of them, so the release that changes it still refreshes this one
// without --force. The current rendering for any other default branch is recognised from the file
// itself (reuseRenderingBranch). testdata/reuse-workflow reproduces each digest
// (reuse_gate_test.go).
var priorReuseWorkflowDigests = map[string]string{
	"17dc517cc1e1190158eb83eab35f0a6eb61ad469ac45549630f71d0a721b85ae": "reuse-action v6, checkout v7, default branch main, 10-minute timeout",
}

// reusePushBranches opens the line of the hosted REUSE gate that names the default branch its
// push trigger runs on, the line the hosted workflows of the managed asset families use too: the
// branch single-quoted, so a name YAML would read as a number or a boolean stays a string, which
// config.ValidBranchName leaves nothing to escape in.
const reusePushBranches = "\n    branches: ['"

// reuseWorkflow renders the hosted REUSE gate for the repository's default branch, the branch
// the ruleset requiring its job protects: one job, on the runner the flavor workflows use, with a
// timeout, that checks the commit out without keeping the token and runs
// supplychain.ReuseActionRef, which runs reuse lint over the checkout. It runs on a push to the
// default branch and on a pull request into it.
func reuseWorkflow(branch string) string {
	return "---\n" +
		"# REUSE licensing gate, written by praetorctl adopt because the repository\n" +
		"# carries REUSE.toml or LICENSES/. It runs reuse lint at the release line the\n" +
		"# reuse-lint pre-commit job of lefthook.yml requires.\n" +
		"name: REUSE\n" +
		"\n" +
		"'on':\n" +
		"  push:" + reusePushBranches + branch + "']\n" +
		"  pull_request:" + reusePushBranches + branch + "']\n" +
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
		"      - uses: actions/checkout@v7\n" +
		"        with:\n" +
		"          persist-credentials: false\n" +
		"      - name: reuse lint\n" +
		"        uses: " + supplychain.ReuseActionRef() + "\n"
}

// reuseRenderingBranch returns the default branch data is the hosted REUSE gate rendered for
// (reuseWorkflow), in one consistent line-ending style, and whether data is such a rendering:
// Praetor's unedited output, for the current default branch or another one, such as before the
// branch was renamed.
func reuseRenderingBranch(data []byte) (string, bool) {
	_, rest, found := strings.Cut(string(data), reusePushBranches)
	branch, _, closed := strings.Cut(rest, "']")
	if !found || !closed || !config.ValidBranchName(branch) {
		return "", false
	}
	equal, err := util.CanonicalTextEquivalent(data, []byte(reuseWorkflow(branch)))
	return branch, err == nil && equal
}

// isReuseRendering reports whether data is a hosted REUSE gate adoption wrote and nobody edited:
// an earlier rendering (priorReuseWorkflowDigests) or the current one for any default branch.
func isReuseRendering(data []byte) bool {
	_, current := reuseRenderingBranch(data)
	return current || isPriorRendering(data, priorReuseWorkflowDigests)
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
func reconcileReuseGate(ctx context.Context, s *adoptSession) error {
	declared, err := supplychain.ReuseDeclared(ctx, s.repoPath)
	if err != nil {
		return fmt.Errorf("decide the hosted REUSE gate: %w", err)
	}
	if !declared {
		return s.removeReuseGate(ctx)
	}
	branch, err := reuseDefaultBranch(ctx, s.repoPath)
	if err != nil {
		return err
	}
	prior, err := s.reusePriorDigests(ctx)
	if err != nil {
		return err
	}
	_, err = s.scaffoldFile(ctx, scaffold{
		rel:       reuseWorkflowFile,
		perm:      filePerm,
		content:   []byte(reuseWorkflow(branch)),
		created:   "Scaffolded the hosted REUSE gate: reuse lint at " + supplychain.ReuseActionRef() + " on " + branch,
		verified:  "Existing hosted REUSE gate verified present",
		prior:     prior,
		refreshed: "Refreshed an unedited earlier Praetor REUSE gate to the current rendering for " + branch,
	})
	return err
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
	if branch, rendered := reuseRenderingBranch(existing); exists && rendered {
		digest, _, err := util.CanonicalTextDigest(existing)
		if err != nil {
			return nil, fmt.Errorf("digest %s: %w", reuseWorkflowFile, err)
		}
		prior[digest] = "the current rendering for default branch " + branch
	}
	return prior, nil
}

// removeReuseGate removes the hosted REUSE gate from a repository whose root no longer carries
// REUSE.toml or LICENSES/, when the file is Praetor's unedited rendering (isReuseRendering):
// reuse lint fails such a tree, so the gate, a required check of the ruleset derived from it,
// would block every pull request. The removal is bound to the bytes read, so a file edited in
// between is kept. An edited gate is the repository's: kept, with a warning naming the check it
// fails. A dry run records the removal it would make.
func (s *adoptSession) removeReuseGate(ctx context.Context) error {
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
	if !isReuseRendering(actual) {
		s.report.addWarning("%s kept: the root carries neither %s nor %s/, so its REUSE lint job fails every run and, while the "+
			"branch ruleset requires it, every pull request; it is edited, so adoption does not remove it: remove it, or add %s or %s/ back",
			reuseWorkflowFile, supplychain.ReuseFile, supplychain.LicensesDir, supplychain.ReuseFile, supplychain.LicensesDir)
		return nil
	}
	if !s.opts.DryRun {
		if err := contextopt.RemoveSnapshot(ctx, full, actual); err != nil {
			return fmt.Errorf("remove %s: %w", reuseWorkflowFile, err)
		}
	}
	s.planDryRunRemoval(reuseWorkflowFile)
	s.report.recordReconciledAs(reuseWorkflowFile, actionRemove, "Removed the hosted REUSE gate: the root carries neither "+
		supplychain.ReuseFile+" nor "+supplychain.LicensesDir+"/, so reuse lint would fail every pull request")
	return nil
}

// plannedReuseWorkflow lists the hosted REUSE gate when this run leaves it as adoption's
// rendering: the step is not declined, the root declares REUSE, and the file is absent or holds
// a Praetor rendering, current or earlier, for any default branch (isReuseRendering). A file it cannot read is not claimed: the harness
// under-claims rather than credit a gate the repository may not get.
func (s *adoptSession) plannedReuseWorkflow(ctx context.Context) ([]flavor.PlannedTemplate, error) {
	declined, err := ArtifactDeclined(s.declined, reuseGateStep)
	if err != nil {
		return nil, fmt.Errorf("resolve adoption.decline for the hosted REUSE gate: %w", err)
	}
	if declined {
		return nil, nil
	}
	declared, err := supplychain.ReuseDeclared(ctx, s.repoPath)
	if err != nil || !declared {
		return nil, err
	}
	branch, err := reuseDefaultBranch(ctx, s.repoPath)
	if err != nil {
		return nil, err
	}
	existing, exists, err := contextopt.ObserveSnapshotIn(ctx, s.repoPath, reuseWorkflowFile)
	if err != nil || (exists && !isReuseRendering(existing)) {
		return nil, ctx.Err()
	}
	return []flavor.PlannedTemplate{{Path: reuseWorkflowFile, Content: reuseWorkflow(branch)}}, nil
}

// migrateReuseSwitch writes target's rendering over existing when existing is the current
// rendering for the other REUSE switch (lefthookShape.otherReuse): adoption wrote it before the
// root gained or lost REUSE.toml and LICENSES/. The checkpoint jobs it carries are kept, as
// writeLefthookConfig keeps them on the current rendering. It reports whether it wrote.
func (s *adoptSession) migrateReuseSwitch(target lefthookTarget, existing []byte) (bool, error) {
	switched := matchCurrentLefthook(existing, target.shape.otherReuse())
	if !switched.found {
		return false, nil
	}
	rendering := buildLefthookYAMLFor(target.shape, target.checkpoint || switched.checkpoint)
	return true, s.migrateLefthookConfig(rendering, reuseJobMigration(target.shape))
}

// reuseJobMigration is the report detail of a lefthook.yml migrated to the rendering for the
// other REUSE switch (lefthookShape.otherReuse).
func reuseJobMigration(shape lefthookShape) string {
	if shape.reuse {
		return "Added the reuse-lint job to the Praetor-generated Lefthook configuration: the root carries " +
			supplychain.ReuseFile + " or " + supplychain.LicensesDir + "/"
	}
	return "Removed the reuse-lint job from the Praetor-generated Lefthook configuration: the root carries neither " +
		supplychain.ReuseFile + " nor " + supplychain.LicensesDir + "/"
}
