package dogfood

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"gopkg.in/yaml.v3"
)

// PublicVerification exposes the bounded checks independently of their verdict.
type PublicVerification struct {
	LockVerified    bool                    `json:"lock_verified"`
	ContextVerified bool                    `json:"context_verified"`
	Scan            *hiss.ScanReport        `json:"scan,omitempty"`
	Ratchet         *baseline.RatchetResult `json:"ratchet,omitempty"`
}

func applyPublicAttempt(ctx context.Context, opts adopt.AdoptOptions, original publicTree, before *hiss.ScanReport, number int) PublicAttempt {
	attempt := PublicAttempt{Number: number}
	var err error
	attempt.Adoption, err = adopt.Adopt(ctx, opts)
	if err := publicAdoptionError(attempt.Adoption, err); err != nil {
		attempt.Error = err.Error()
		return attempt
	}
	after, err := snapshotPublicTree(ctx, opts.Path)
	if err != nil {
		attempt.Error = err.Error()
		return attempt
	}
	attempt.TreeDigest = after.digest()
	attempt.ChangedFiles = after.changed(original)
	attempt.Verification, err = verifyPublicCheckout(ctx, opts.Path, before, attempt.ChangedFiles)
	if err != nil {
		attempt.Error = err.Error()
	}
	return attempt
}

func verifyPublicCheckout(ctx context.Context, dir string, before *hiss.ScanReport, changed []string) (*PublicVerification, error) {
	result := &PublicVerification{}
	manifest, err := publicManifest(ctx, dir)
	if err != nil {
		return result, err
	}
	if _, err := config.ValidateLockfile(ctx, dir, manifest); err != nil {
		return result, fmt.Errorf("verify lock: %w", err)
	}
	result.LockVerified = true
	if err := compiler.NewTranspiler().Verify(filepath.Join(dir, "AGENTS.md"), dir); err != nil {
		return result, fmt.Errorf("verify context: %w", err)
	}
	result.ContextVerified = true
	result.Scan, err = scanPublicTree(ctx, dir)
	if err != nil {
		return result, err
	}
	base := &baseline.Baseline{Version: 1, Infractions: publicInfractions(before), TotalInfractions: before.TotalInfractions}
	result.Ratchet = baseline.EvaluateRatchet(base, publicInfractions(result.Scan), changed)
	if !result.Ratchet.Passed {
		return result, errors.New("HISS ratchet failed: new or touched-file infractions")
	}
	return result, nil
}

func scanPublicTree(ctx context.Context, dir string) (*hiss.ScanReport, error) {
	report, err := hiss.Scan(ctx, dir, hiss.ScanOptions{})
	if err != nil {
		return report, err
	}
	if report.Truncated {
		return report, hiss.ErrScanTruncated
	}
	return report, nil
}

func publicInfractions(scan *hiss.ScanReport) []baseline.Infraction {
	infractions := hiss.ConvertToBaseline(scan.Violations)
	for i := 0; i < len(infractions) && i < 10000; i++ {
		inf := &infractions[i]
		inf.Fingerprint = fmt.Sprintf("%s:%d:%s", inf.FilePath, inf.LineNumber, inf.RuleID)
	}
	return infractions
}

func publicManifest(ctx context.Context, dir string) (result *config.Manifest, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	data, err := readPublicFile(root, ".standards.yaml", 1<<20)
	if err != nil {
		return nil, err
	}
	var manifest config.Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}
