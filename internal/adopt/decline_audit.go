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
