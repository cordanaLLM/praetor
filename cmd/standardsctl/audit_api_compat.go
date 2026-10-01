// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"
	"slices"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/managedasset"
)

// auditAPICompatibilityGate verifies the Go API compatibility gate of api:public-contract
// (#357). While the facet is declared and git tracks a go.mod, the gate program and its hosted
// workflow must hold the locked texts, git must commit them, and the workflow must report its
// context on every pull request (auditFamilyContextsReported), which is what makes the rendered
// branch ruleset require it; the branch ruleset audit then checks that requirement. A declared
// facet without a tracked go.mod gets no gate: the audit says that no API compatibility checker
// runs for the repository's languages, neither a pass nor a failure. Once the facet or the last
// go.mod is gone, no Praetor file of the gate and no ruleset requirement of its context may
// remain.
func auditAPICompatibilityGate(ctx context.Context, manifest *config.Manifest, rootDir string) error {
	enabled, err := adopt.APICompatibilityEnabled(manifest.Facets)
	if err != nil {
		return fmt.Errorf("[FAIL] Resolve API compatibility facet: %w", err)
	}
	families := adopt.APICompatibilityFamilies()
	if !enabled {
		return auditDisabledAPICompatibility(ctx, manifest, rootDir, families)
	}
	applies, err := adopt.APICompatibilityApplies(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("[FAIL] Resolve whether git tracks a Go module for the API compatibility gate: %w", err)
	}
	if !applies {
		return auditAPICompatibilityWithoutGo(ctx, manifest, rootDir, families)
	}
	return auditEnabledAPICompatibility(ctx, rootDir, families)
}

// auditAPICompatibilityWithoutGo prints, as information, that no API compatibility checker runs
// for a repository declaring api:public-contract without a tracked go.mod, and then holds it to
// the disabled facet's terms: a gate file or required context left from a Go past fails, naming
// the adoption run that retires it.
func auditAPICompatibilityWithoutGo(ctx context.Context, manifest *config.Manifest, rootDir string, families []managedasset.Family) error {
	fmt.Printf("[INFO] %s.\n", adopt.NoAPICompatibilityChecker)
	if err := auditDisabledAPICompatibility(ctx, manifest, rootDir, families); err != nil {
		return fmt.Errorf("%w; git tracks no go.mod, so rerun praetorctl adopt to retire the gate", err)
	}
	return nil
}

// auditEnabledAPICompatibility verifies the gate of a repository that declares the facet and
// tracks a go.mod: locked texts, committed files and a context reported on every pull request.
func auditEnabledAPICompatibility(ctx context.Context, rootDir string, families []managedasset.Family) error {
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		if _, err := auditManagedFamily(ctx, rootDir, families[index]); err != nil {
			return err
		}
	}
	if err := auditFamilyFilesCommitted(ctx, rootDir, families); err != nil {
		return err
	}
	if err := auditFamilyContextsReported(ctx, rootDir, families); err != nil {
		return err
	}
	fmt.Printf("[PASS] Locked API compatibility gate verified (%s reports %q on every pull request).\n",
		adopt.APICompatibilityWorkflowFile, adopt.APICompatibilityStatusContext)
	return nil
}

// auditDisabledAPICompatibility fails while a facet-less repository keeps a Praetor file of
// the gate, or, unless adoption.decline names the branch ruleset, a ruleset requiring its
// context.
func auditDisabledAPICompatibility(ctx context.Context, manifest *config.Manifest, rootDir string, families []managedasset.Family) error {
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		if err := auditDisabledManagedFamily(ctx, rootDir, families[index]); err != nil {
			return err
		}
	}
	declined, err := adopt.ManifestArtifactDeclined(manifest, "branch-ruleset")
	if err != nil {
		return fmt.Errorf("[FAIL] Resolve branch-ruleset adoption decline: %w", err)
	}
	if declined {
		return nil
	}
	return auditDisabledFamilyContexts(ctx, rootDir, families)
}

// auditFamilyContextsReported requires the hosted workflow of each of families to report the
// family's status context on every pull request: a job without a condition and not advisory,
// in a workflow triggered by every pull request (forge.RequiredStatusContextsOf). A context
// only some pull requests report would pass whether or not the gate ran.
func auditFamilyContextsReported(ctx context.Context, rootDir string, families []managedasset.Family) error {
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		family := families[index]
		if family.WorkflowFile == "" {
			continue
		}
		contexts, err := forge.RequiredStatusContextsOf(ctx, rootDir, []string{family.WorkflowFile})
		if err != nil {
			return fmt.Errorf("[FAIL] Discover the hosted %s context: %w", family.Kind, err)
		}
		if !slices.Contains(contexts, family.StatusContext) {
			return fmt.Errorf("[FAIL] Hosted %s workflow %s does not report required context %q on every pull request",
				family.Kind, family.WorkflowFile, family.StatusContext)
		}
	}
	return nil
}
