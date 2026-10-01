// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
)

const (
	// APICompatibilityWorkflowFile is the hosted Go API compatibility gate the
	// api:public-contract facet emits.
	APICompatibilityWorkflowFile = apiassets.WorkflowFile
	// APICompatibilityStatusContext is the check name its workflow reports.
	APICompatibilityStatusContext = apiassets.StatusContext
)

// APICompatibilityEnabled reports whether the validated manifest facet inventory declares the
// public API contract, which enables the Go API compatibility gate (#357) wherever git tracks a
// go.mod (APICompatibilityApplies).
func APICompatibilityEnabled(facets []string) (bool, error) {
	return config.DeclaresFacet(facets, managedasset.APIContractFacet)
}

// APICompatibilityApplies reports whether the Go API compatibility gate applies to the
// repository at repoPath: whether git tracks a go.mod the gate discovers, at the root or nested
// (apiassets.TracksModule). A repository declaring api:public-contract without one gets no gate,
// since no API compatibility checker exists for its languages.
func APICompatibilityApplies(ctx context.Context, repoPath string) (bool, error) {
	families, err := enabledFamiliesOf(ctx, repoPath, APICompatibilityFamilies(), true)
	if err != nil {
		return false, err
	}
	return len(families) > 0, nil
}

// APICompatibilityFamilies returns the managed asset families api:public-contract enables, in
// registry order.
func APICompatibilityFamilies() []managedasset.Family {
	return managedasset.ForFacet(managedasset.APIContractFacet)
}

// NoAPICompatibilityChecker is the line adoption and audit report for a repository that declares
// api:public-contract and tracks no go.mod: the facet then claims no enforcement it lacks.
const NoAPICompatibilityChecker = "No API compatibility checker runs for this repository's languages: " +
	"api:public-contract is declared, but git tracks no go.mod, and the Go API Compatibility gate " +
	"compares Go modules only"

// reconcileAPICompatibilityGate emits every API compatibility family while the facet is
// declared and git tracks a go.mod, and removes them otherwise: once the facet is gone, or once
// the repository's last go.mod is. Removing the family drops its workflow's required context, so
// an unforced removal stops while the branch ruleset still requires it, as the documentation
// gate's does (preflightContextRemoval).
func reconcileAPICompatibilityGate(ctx context.Context, s *adoptSession) error {
	declared, err := facetDeclaredForSession(s, managedasset.APIContractFacet)
	if err != nil {
		return fmt.Errorf("resolve the API compatibility facet: %w", err)
	}
	families := APICompatibilityFamilies()
	enabled, err := enabledFamiliesOf(ctx, s.repoPath, families, declared)
	if err != nil {
		return err
	}
	if len(enabled) == 0 {
		return retireAPICompatibilityGate(ctx, s, families, declared)
	}
	for index := 0; index < len(enabled) && index < managedasset.MaxFamilies; index++ {
		if err := reconcileManagedFamily(ctx, s, enabled[index]); err != nil {
			return err
		}
	}
	return nil
}

// retireAPICompatibilityGate removes the API compatibility families. While the facet is declared,
// the repository tracks no go.mod: the run records that no checker runs for its languages, and a
// refused removal names the missing go.mod as its cause.
func retireAPICompatibilityGate(ctx context.Context, s *adoptSession, families []managedasset.Family, declared bool) error {
	if !declared {
		if err := preflightContextRemoval(ctx, s, families); err != nil {
			return err
		}
		return removeManagedFamilies(ctx, s, families)
	}
	s.report.recordNotApplicable(APICompatibilityWorkflowFile, NoAPICompatibilityChecker)
	if err := preflightContextRemoval(ctx, s, families); err != nil {
		return fmt.Errorf("git tracks no go.mod in this repository: %w", err)
	}
	return removeManagedFamilies(ctx, s, families)
}

// EnabledManagedFamilies returns the managed asset families enabled for the repository at
// repoPath by the facets it declares, in registry order: the families of every declared facet
// that apply to it (managedasset.Family.Enabled), which adoption emits and audit locks.
func EnabledManagedFamilies(ctx context.Context, repoPath string, facets []string) ([]managedasset.Family, error) {
	return selectEnabledFamilies(ctx, repoPath, func(facet string) (bool, error) { return config.DeclaresFacet(facets, facet) })
}

// enabledManagedFamiliesForSession is EnabledManagedFamilies over the facets the session
// resolved (facetDeclaredForSession).
func enabledManagedFamiliesForSession(ctx context.Context, s *adoptSession) ([]managedasset.Family, error) {
	return selectEnabledFamilies(ctx, s.repoPath, func(facet string) (bool, error) { return facetDeclaredForSession(s, facet) })
}

// selectEnabledFamilies returns the registry's families whose facet declared reports declared
// and which apply to the repository at repoPath.
func selectEnabledFamilies(ctx context.Context, repoPath string, declared func(string) (bool, error)) ([]managedasset.Family, error) {
	all := managedasset.Families()
	enabled := make([]managedasset.Family, 0, len(all))
	for index := 0; index < len(all) && index < managedasset.MaxFamilies; index++ {
		on, err := declared(all[index].Facet)
		if err != nil {
			return nil, fmt.Errorf("resolve facet %s: %w", all[index].Facet, err)
		}
		selected, err := enabledFamiliesOf(ctx, repoPath, all[index:index+1], on)
		if err != nil {
			return nil, err
		}
		enabled = append(enabled, selected...)
	}
	return enabled, nil
}

// enabledFamiliesOf returns the families of one facet that are enabled for the repository at
// repoPath, declared telling whether the facet is declared (managedasset.Family.Enabled).
func enabledFamiliesOf(ctx context.Context, repoPath string, families []managedasset.Family, declared bool) ([]managedasset.Family, error) {
	enabled := make([]managedasset.Family, 0, len(families))
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		on, err := families[index].Enabled(ctx, repoPath, declared)
		if err != nil {
			return nil, err
		}
		if on {
			enabled = append(enabled, families[index])
		}
	}
	return enabled, nil
}

// facetDeclaredForSession reports whether the session declares facet: through the manifest
// policy once the policy-catalog step resolved it, else through the facets adoption was given.
func facetDeclaredForSession(s *adoptSession, facet string) (bool, error) {
	if s.policy != nil && s.policy.Manifest != nil {
		return config.DeclaresFacet(s.policy.Manifest.Facets, facet)
	}
	return config.DeclaresFacet(s.facets, facet)
}
