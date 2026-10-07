// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/supplychain"
)

// The REUSE gate adoption emits into a repository that declares its licensing the REUSE way
// (supplychain.ReuseDeclared: REUSE.toml or LICENSES/ at the root): the reuse-lint pre-commit job
// of lefthook.yml (reuseLintCommand) and the hosted workflow below, both at the one REUSE pin,
// supplychain.ReuseActionVersion. A repository with neither gets neither: reuse lint fails every
// tree that declares no licensing its way, so the gate would only block.

const (
	// reuseWorkflowFile is the hosted REUSE gate adoption writes.
	reuseWorkflowFile = ".github/workflows/reuse.yml"
	// reuseGateStep is the adoption step that writes it, the name adoption.decline takes.
	reuseGateStep = "reuse-gate"
)

// priorReuseWorkflowDigests are the digests (priorRendering) of every hosted REUSE gate a Praetor
// release wrote at reuseWorkflowFile, keyed to what produced it; the current rendering is one of
// them, so the release that changes it still refreshes this one without --force.
// testdata/reuse-workflow reproduces each digest (reuse_gate_test.go).
var priorReuseWorkflowDigests = map[string]string{
	"fc2628dc30374936a688ecf51ae2d71eec29495809994c86383ad7dbeac5ab76": "reuse-action v6, checkout v7",
}

// reuseWorkflow renders the hosted REUSE gate: one job, on the runner the flavor workflows use,
// that checks the commit out without keeping the token and runs supplychain.ReuseActionRef, which
// runs reuse lint over the checkout.
func reuseWorkflow() string {
	return "---\n" +
		"# REUSE licensing gate, written by praetorctl adopt because the repository\n" +
		"# carries REUSE.toml or LICENSES/. It runs reuse lint at the release line the\n" +
		"# reuse-lint pre-commit job of lefthook.yml requires.\n" +
		"name: REUSE\n" +
		"\n" +
		"'on':\n" +
		"  push:\n" +
		"    branches: [main]\n" +
		"  pull_request:\n" +
		"    branches: [main]\n" +
		"\n" +
		"permissions:\n" +
		"  contents: read\n" +
		"\n" +
		"jobs:\n" +
		"  reuse:\n" +
		"    name: REUSE lint\n" +
		"    runs-on: ubuntu-26.04\n" +
		"    steps:\n" +
		"      - uses: actions/checkout@v7\n" +
		"        with:\n" +
		"          persist-credentials: false\n" +
		"      - name: reuse lint\n" +
		"        uses: " + supplychain.ReuseActionRef() + "\n"
}

// reconcileReuseGate writes the hosted REUSE gate (reuseWorkflow) into a repository that declares
// REUSE, and writes nothing into one that does not. An existing gate holding an earlier Praetor
// rendering (priorReuseWorkflowDigests) is refreshed; any other file there is the repository's
// and is kept, --force included, with a warning.
func reconcileReuseGate(ctx context.Context, s *adoptSession) error {
	declared, err := supplychain.ReuseDeclared(ctx, s.repoPath)
	if err != nil {
		return fmt.Errorf("decide the hosted REUSE gate: %w", err)
	}
	if !declared {
		return nil
	}
	_, err = s.scaffoldFile(ctx, scaffold{
		rel:       reuseWorkflowFile,
		perm:      filePerm,
		content:   []byte(reuseWorkflow()),
		created:   "Scaffolded the hosted REUSE gate: reuse lint at " + supplychain.ReuseActionRef(),
		verified:  "Existing hosted REUSE gate verified present",
		prior:     priorReuseWorkflowDigests,
		refreshed: "Refreshed an unedited earlier Praetor REUSE gate to the current rendering",
	})
	return err
}

// plannedReuseWorkflow lists the hosted REUSE gate when this run leaves it as adoption's
// rendering: the step is not declined, the root declares REUSE, and the file is absent or holds
// a Praetor rendering, current or earlier. A file it cannot read is not claimed: the harness
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
	existing, exists, err := contextopt.ObserveSnapshotIn(ctx, s.repoPath, reuseWorkflowFile)
	if err != nil || (exists && !isPriorRendering(existing, priorReuseWorkflowDigests)) {
		return nil, ctx.Err()
	}
	return []flavor.PlannedTemplate{{Path: reuseWorkflowFile, Content: reuseWorkflow()}}, nil
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
