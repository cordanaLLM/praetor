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
// public API contract, which enables the Go API compatibility gate (#357).
func APICompatibilityEnabled(facets []string) (bool, error) {
	return config.DeclaresFacet(facets, managedasset.APIContractFacet)
}

// APICompatibilityFamilies returns the managed asset families api:public-contract enables, in
// registry order.
func APICompatibilityFamilies() []managedasset.Family {
	return managedasset.ForFacet(managedasset.APIContractFacet)
}

// reconcileAPICompatibilityGate emits every API compatibility family while the facet is
// declared and removes them once it is not. Removing the family drops its workflow's required
// context, so an unforced removal stops while the branch ruleset still requires it, as the
// documentation gate's does (preflightContextRemoval).
func reconcileAPICompatibilityGate(ctx context.Context, s *adoptSession) error {
	enabled, err := facetDeclaredForSession(s, managedasset.APIContractFacet)
	if err != nil {
		return fmt.Errorf("resolve the API compatibility facet: %w", err)
	}
	families := APICompatibilityFamilies()
	if !enabled {
		if err := preflightContextRemoval(ctx, s, families); err != nil {
			return err
		}
		return removeManagedFamilies(ctx, s, families)
	}
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		if err := reconcileManagedFamily(ctx, s, families[index]); err != nil {
			return err
		}
	}
	return nil
}

// EnabledManagedFamilies returns the managed asset families of every facet facets declares,
// in registry order: the families adoption emits and audit locks for that inventory.
func EnabledManagedFamilies(facets []string) ([]managedasset.Family, error) {
	return selectEnabledFamilies(func(facet string) (bool, error) { return config.DeclaresFacet(facets, facet) })
}

// enabledManagedFamiliesForSession is EnabledManagedFamilies over the facets the session
// resolved (facetDeclaredForSession).
func enabledManagedFamiliesForSession(s *adoptSession) ([]managedasset.Family, error) {
	return selectEnabledFamilies(func(facet string) (bool, error) { return facetDeclaredForSession(s, facet) })
}

// selectEnabledFamilies returns the registry's families whose facet declared reports declared.
func selectEnabledFamilies(declared func(string) (bool, error)) ([]managedasset.Family, error) {
	all := managedasset.Families()
	enabled := make([]managedasset.Family, 0, len(all))
	for index := 0; index < len(all) && index < managedasset.MaxFamilies; index++ {
		on, err := declared(all[index].Facet)
		if err != nil {
			return nil, fmt.Errorf("resolve facet %s: %w", all[index].Facet, err)
		}
		if on {
			enabled = append(enabled, all[index])
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
