package adopt

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
)

// AuditLabelTaxonomy is the label taxonomy gate the CLI and MCP audits share. A declined labels
// step passes with the decline named (#600); otherwise .config/labels.yaml must exist.
func AuditLabelTaxonomy(manifest *config.Manifest, rootDir string) (string, error) {
	verdict, err := AuditDecline(manifest, "labels")
	if err != nil {
		return "", fmt.Errorf("[FAIL] Label taxonomy audit failed: %w", err)
	}
	if verdict.Declined {
		return verdict.Line("Label taxonomy " + labelsFile), nil
	}
	if !fileExists(filepath.Join(rootDir, filepath.FromSlash(labelsFile))) {
		return "", errors.New("[FAIL] Required label taxonomy " + labelsFile + " is missing")
	}
	return "[PASS] Repository label taxonomy " + labelsFile + " verified.", nil
}

// AuditGitHookConfig is the hook configuration gate the CLI and MCP audits share, for a Git
// checkout. A declined git-hooks step passes with the decline named and declined set: the
// repository owns its hooks, so the CLI requires no hook activation either. Otherwise
// lefthook.yml must exist, and the CLI goes on to check that its hook is active.
func AuditGitHookConfig(manifest *config.Manifest, rootDir string) (line string, declined bool, err error) {
	verdict, err := AuditDecline(manifest, "git-hooks")
	if err != nil {
		return "", false, fmt.Errorf("[FAIL] Git hook audit failed: %w", err)
	}
	if verdict.Declined {
		return verdict.Line("Git hooks (" + lefthookFile + " and its activation)"), true, nil
	}
	if !fileExists(filepath.Join(rootDir, lefthookFile)) {
		return "", false, errors.New("[FAIL] " + lefthookFile + " configuration is missing from repository root")
	}
	return "[PASS] Git hook configuration " + lefthookFile + " verified.", false, nil
}
