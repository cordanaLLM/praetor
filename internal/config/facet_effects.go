package config

import (
	"context"
	"errors"
	"fmt"
	"path"
)

// FacetEffect is what joining one facet onto the declared profiles raises.
type FacetEffect struct {
	// Facet is the facet id the manifest declares.
	Facet string `json:"facet"`
	// Raises names each branch-protection and supply-chain setting the facet raises above the
	// declared profiles alone, as "<catalog key> <profile value> -> <value>" (raisedSettings);
	// empty when it raises none.
	Raises []string `json:"raises,omitempty"`
}

// FacetEffects names, for each facet p.Manifest declares and in that order, the
// branch-protection and supply-chain settings the facet raises above the declared profiles
// joined onto DefaultPolicy. It reads the catalog texts the policy retained (CatalogArtifacts),
// the exact texts the lock pins, so it opens no catalog of its own. Repository overrides are
// left out: they apply whichever facets are declared. A declared id whose text the policy did
// not retain is an error, as is a policy without its manifest.
func (p *EffectivePolicy) FacetEffects(ctx context.Context) ([]FacetEffect, error) {
	if err := p.checkFacetEffectInputs(ctx); err != nil {
		return nil, err
	}
	layers, err := retainedContributions(ctx, p.CatalogArtifacts)
	if err != nil {
		return nil, err
	}
	base, err := joinRetained(DefaultPolicy(), layers, "profile", p.Manifest.Profiles)
	if err != nil {
		return nil, err
	}
	effects := make([]FacetEffect, 0, len(p.Manifest.Facets))
	for i := 0; i < len(p.Manifest.Facets) && i < maxLockEntries; i++ {
		next, err := joinRetained(base, layers, "facet", p.Manifest.Facets[i:i+1])
		if err != nil {
			return nil, err
		}
		effects = append(effects, FacetEffect{Facet: p.Manifest.Facets[i], Raises: raisedSettings(base, next)})
	}
	return effects, nil
}

// checkFacetEffectInputs refuses what FacetEffects cannot read: no context, no policy, a policy
// without its manifest, and a declaration past the lock bound.
func (p *EffectivePolicy) checkFacetEffectInputs(ctx context.Context) error {
	if ctx == nil || p == nil || p.Manifest == nil {
		return errors.New("facet effects need a context and a resolved policy with its manifest")
	}
	if len(p.Manifest.Profiles) > maxLockEntries || len(p.Manifest.Facets) > maxLockEntries {
		return fmt.Errorf("manifest declares more than %d profiles or facets", maxLockEntries)
	}
	return nil
}

// joinRetained joins onto base the contribution of each id of kind in layers
// (retainedContributions), in order. An id without a retained text is an error.
func joinRetained(base *ResolvedPolicy, layers map[string]*ResolvedPolicy, kind string, ids []string) (*ResolvedPolicy, error) {
	for i := 0; i < len(ids) && i < maxLockEntries; i++ {
		layer, ok := layers[kind+":"+ids[i]]
		if !ok {
			return nil, fmt.Errorf("%s %q: the policy retained no pinned catalog text", kind, ids[i])
		}
		base = Join(base, layer)
	}
	return base, nil
}

// retainedContributions decodes each retained catalog text with the one archetype reader
// (decodeArchetype) into the share of the join its controls contribute, keyed
// "<kind>:<id>" as the policy sources are. The kind follows the catalog directory the loader
// retained the text under (retainArtifact).
func retainedContributions(ctx context.Context, artifacts []PolicyArtifact) (map[string]*ResolvedPolicy, error) {
	if len(artifacts) > maxPolicyLayers {
		return nil, fmt.Errorf("policy retains more than %d catalog texts", maxPolicyLayers)
	}
	facetDir := path.Join(archetypeDirName, facetDirName)
	layers := make(map[string]*ResolvedPolicy, len(artifacts))
	for i := 0; i < len(artifacts) && i < maxPolicyLayers; i++ {
		archetype, err := decodeArchetype(ctx, artifacts[i].RelativePath, artifacts[i].Content)
		if err != nil {
			return nil, err
		}
		kind := "profile"
		if path.Dir(artifacts[i].RelativePath) == facetDir {
			kind = "facet"
		}
		layers[kind+":"+archetype.ID] = PolicyLayer{Controls: archetype.Controls}.policy()
	}
	return layers, nil
}

// raisedSettings names each branch-protection and supply-chain setting whose value differs
// between base and next, by its catalog key, as "<key> <base value> -> <next value>". next is a
// join onto base, which is monotonic, so every difference is a raise.
func raisedSettings(base, next *ResolvedPolicy) []string {
	settings := []struct {
		key           string
		before, after any
	}{
		{"branch_protection.required_approving_reviewers", base.BranchProtection.RequiredApprovingReviewers, next.BranchProtection.RequiredApprovingReviewers},
		{"branch_protection.require_signed_commits", base.BranchProtection.RequireSignedCommits, next.BranchProtection.RequireSignedCommits},
		{"branch_protection.enforce_linear_history", base.BranchProtection.EnforceLinearHistory, next.BranchProtection.EnforceLinearHistory},
		{"branch_protection.dismiss_stale_reviews", base.BranchProtection.DismissStaleReviews, next.BranchProtection.DismissStaleReviews},
		{"supply_chain.slsa_level", base.SupplyChain.SLSALevel, next.SupplyChain.SLSALevel},
		{"supply_chain.enforce_cosign", base.SupplyChain.EnforceCosign, next.SupplyChain.EnforceCosign},
		{"supply_chain.require_sbom", base.SupplyChain.RequireSBOM, next.SupplyChain.RequireSBOM},
	}
	raised := make([]string, 0, len(settings))
	for _, setting := range settings {
		if setting.before != setting.after {
			raised = append(raised, fmt.Sprintf("%s %v -> %v", setting.key, setting.before, setting.after))
		}
	}
	return raised
}
