package dogfood

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

// PublicVerification exposes the bounded checks independently of their verdict.
type PublicVerification struct {
	PolicySHA256     string                  `json:"policy_sha256,omitempty"`
	MaxFuncLOC       int                     `json:"max_func_loc,omitempty"`
	BaselineSHA256   string                  `json:"baseline_sha256,omitempty"`
	BaselineVerified bool                    `json:"baseline_verified,omitempty"`
	LockVerified     bool                    `json:"lock_verified"`
	ContextVerified  bool                    `json:"context_verified"`
	Scan             *hiss.ScanReport        `json:"scan,omitempty"`
	Ratchet          *baseline.RatchetResult `json:"ratchet,omitempty"`
}

type publicPolicyAnchor struct {
	snapshotLimits *SnapshotLimits
	Policy         *config.EffectivePolicy
	Scan           *hiss.ScanReport
}

func applyPublicAttempt(ctx context.Context, opts adopt.AdoptOptions, original publicTree, anchor publicPolicyAnchor, number int) PublicAttempt {
	attempt := PublicAttempt{Number: number}
	var err error
	attempt.Adoption, err = adopt.Adopt(ctx, opts)
	if err := publicAdoptionError(attempt.Adoption, err); err != nil {
		attempt.Error = err.Error()
		return attempt
	}
	if err := matchPublicPolicy(anchor.Policy, attempt.Adoption.EffectivePolicy); err != nil {
		attempt.Error = err.Error()
		return attempt
	}
	after, snapshot, err := snapshotTreeWithLimits(ctx, opts.Path, false, anchor.snapshotLimits)
	attempt.Snapshot = &snapshot
	if err != nil {
		attempt.Error = err.Error()
		return attempt
	}
	attempt.TreeDigest = after.digest()
	attempt.ChangedFiles = after.changed(original)
	attempt.Verification, err = verifyPublicCheckout(ctx, opts.Path, anchor, attempt.ChangedFiles)
	if err != nil {
		attempt.Error = err.Error()
	}
	return attempt
}

func verifyPublicCheckout(ctx context.Context, dir string, anchor publicPolicyAnchor, changed []string) (*PublicVerification, error) {
	result := &PublicVerification{}
	if err := validatePublicAnchor(anchor); err != nil {
		return result, err
	}
	policy, err := config.LoadEffectivePolicyContext(ctx, config.EffectiveOptions{Root: dir, Audit: true})
	if err != nil {
		return result, fmt.Errorf("verify effective policy: %w", err)
	}
	if err := matchPublicPolicy(anchor.Policy, policy); err != nil {
		return result, err
	}
	result.LockVerified = true
	result.PolicySHA256, result.MaxFuncLOC = policy.SHA256, policy.Policy.Complexity.MaxFuncLOC
	result.BaselineSHA256, err = verifyPublicBaseline(ctx, dir, anchor.Scan)
	if err != nil {
		return result, err
	}
	result.BaselineVerified = true
	if err := compiler.NewTranspiler().VerifyContext(ctx, filepath.Join(dir, "AGENTS.md"), dir); err != nil {
		return result, fmt.Errorf("verify context: %w", err)
	}
	result.ContextVerified = true
	result.Scan, err = scanPublicTree(ctx, dir, anchor.Policy)
	if err != nil {
		return result, err
	}
	base := &baseline.Baseline{Version: 1, Infractions: publicInfractions(anchor.Scan), TotalInfractions: anchor.Scan.TotalInfractions}
	result.Ratchet = baseline.EvaluateRatchet(base, publicInfractions(result.Scan), changed)
	if !result.Ratchet.Passed {
		return result, errors.New("HISS ratchet failed: new or touched-file infractions")
	}
	return result, nil
}

func scanPublicTree(ctx context.Context, dir string, policy *config.EffectivePolicy) (*hiss.ScanReport, error) {
	if err := validatePublicPolicy(policy); err != nil {
		return nil, err
	}
	report, err := hiss.Scan(ctx, dir, hiss.ScanOptions{MaxFuncLOC: policy.Policy.Complexity.MaxFuncLOC})
	if err != nil {
		return report, err
	}
	if err := validatePublicScan(report); err != nil {
		return report, err
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
