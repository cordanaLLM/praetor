package adopt

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// AuditBranchProtectionWithPolicy evaluates branch protection ruleset requirements for rootDir
// against the given manifest, recorded adoption decisions, and resolved policy.
//
// policy must be the effective policy (profiles, facets and overrides joined), the one adopt
// and sync render the ruleset from. Built-in defaults plus overrides is not a stand-in: every
// shipped archetype requires signed commits, which the defaults do not, so a ruleset adopt
// wrote for such a profile would be reported as not matching the declared policy.
//
// A present ruleset must be the one the policy renders for the repository's required status
// checks (forge.ValidateRepositoryRuleset). The audit used to check only that the file existed,
// so "{}" and a ruleset with zero approvals and no signature rule were reported verified.
func AuditBranchProtectionWithPolicy(ctx context.Context, manifest *config.Manifest, rootDir string, policy *config.ResolvedPolicy) (string, error) {
	if err := auditInputsPresent(ctx, manifest, rootDir, policy); err != nil {
		return "", err
	}

	declined, err := ManifestArtifactDeclined(manifest, "branch-ruleset")
	if err != nil {
		return "", fmt.Errorf("[FAIL] Branch protection ruleset audit failed: %w", err)
	}
	if declined {
		return "[PASS] Branch protection ruleset declined by adoption.decline.", nil
	}

	if !policy.BranchProtection.EnforceLinearHistory && !policy.BranchProtection.RequireSignedCommits {
		return "[PASS] Branch protection ruleset not required by policy.", nil
	}
	if err := auditRulesetContent(ctx, manifest, rootDir, policy.BranchProtection); err != nil {
		return "", err
	}
	return fmt.Sprintf("[PASS] Branch protection & merge ruleset %s verified.", rulesetFile), nil
}

// auditInputsPresent refuses an audit that has nothing to audit against.
func auditInputsPresent(ctx context.Context, manifest *config.Manifest, rootDir string, policy *config.ResolvedPolicy) error {
	switch {
	case ctx == nil:
		return errors.New("[FAIL] Branch protection ruleset audit failed: context is required")
	case manifest == nil:
		return errors.New("[FAIL] Branch protection ruleset audit failed: manifest is required")
	case rootDir == "":
		return errors.New("[FAIL] Branch protection ruleset audit failed: repository root is required")
	case policy == nil:
		return errors.New("[FAIL] Branch protection ruleset audit failed: policy is required")
	}
	return nil
}

// auditRulesetContent reads the committed ruleset and compares it with the one the policy
// renders for the repository's default branch (forge.RepositoryDefaultBranch, manifest's
// declaration first) and its workflow-derived required status checks: the rendering adoption
// and flavor apply write.
func auditRulesetContent(ctx context.Context, manifest *config.Manifest, rootDir string, policy config.BranchProtectionPolicy) error {
	data, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(rootDir, filepath.FromSlash(rulesetFile)))
	if err != nil {
		return fmt.Errorf("[FAIL] Branch protection ruleset %s could not be read: %w", rulesetFile, err)
	}
	if !exists {
		return fmt.Errorf("[FAIL] Branch protection ruleset %s is missing while policy requires linear history or signed commits; run 'praetorctl sync' to reconcile", rulesetFile)
	}
	contexts, err := forge.RequiredStatusContexts(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("[FAIL] Branch protection ruleset audit failed: discover required status checks: %w", err)
	}
	branch, err := forge.RepositoryDefaultBranch(ctx, rootDir, manifest)
	if err != nil {
		return fmt.Errorf("[FAIL] Branch protection ruleset audit failed: %w", err)
	}
	if err := forge.ValidateRepositoryRuleset(data, branch, policy, contexts); err != nil {
		return fmt.Errorf("[FAIL] Branch protection ruleset %s does not match the declared policy: %w; "+
			"'praetorctl sync' writes the declared ruleset when the file is absent", rulesetFile, err)
	}
	return nil
}
