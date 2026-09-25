package adopt

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// AuditBranchProtection evaluates branch protection ruleset requirements for rootDir
// against the declared manifest and its recorded adoption decisions.
func AuditBranchProtection(manifest *config.Manifest, rootDir string) (string, error) {
	if manifest == nil {
		return "", errors.New("[FAIL] Branch protection ruleset audit failed: manifest is required")
	}
	policy := config.DefaultPolicy()
	policy.ApplyOverrides(manifest.Overrides)
	return AuditBranchProtectionWithPolicy(manifest, rootDir, policy)
}

// AuditBranchProtectionWithPolicy evaluates branch protection ruleset requirements for rootDir
// against the given manifest, recorded adoption decisions, and resolved policy.
func AuditBranchProtectionWithPolicy(manifest *config.Manifest, rootDir string, policy *config.ResolvedPolicy) (string, error) {
	if manifest == nil {
		return "", errors.New("[FAIL] Branch protection ruleset audit failed: manifest is required")
	}
	if rootDir == "" {
		return "", errors.New("[FAIL] Branch protection ruleset audit failed: repository root is required")
	}
	if policy == nil {
		return "", errors.New("[FAIL] Branch protection ruleset audit failed: policy is required")
	}

	var declines []string
	if manifest.Adoption != nil {
		declines = manifest.Adoption.Decline
	}
	declined, err := ArtifactDeclined(declines, "branch-ruleset")
	if err != nil {
		return "", fmt.Errorf("[FAIL] Branch protection ruleset audit failed: %w", err)
	}
	if declined {
		return "[PASS] Branch protection ruleset declined by adoption.decline.", nil
	}

	if !policy.BranchProtection.EnforceLinearHistory && !policy.BranchProtection.RequireSignedCommits {
		return "[PASS] Branch protection ruleset not required by policy.", nil
	}

	rulesetPath := filepath.Join(rootDir, filepath.FromSlash(rulesetFile))
	if !util.FileExists(rulesetPath) {
		return "", fmt.Errorf("[FAIL] Branch protection ruleset %s is missing while policy requires linear history or signed commits; run 'praetorctl sync' to reconcile", rulesetFile)
	}
	return fmt.Sprintf("[PASS] Branch protection & merge ruleset %s verified.", rulesetFile), nil
}
