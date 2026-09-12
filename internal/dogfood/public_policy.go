package dogfood

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

func validatePublicPolicy(policy *config.EffectivePolicy) error {
	if policy == nil {
		return errors.New("public verification requires an explicit resolved audit policy")
	}
	if len(policy.SHA256) != 64 {
		return errors.New("public policy requires a SHA-256 identity")
	}
	if _, err := hex.DecodeString(policy.SHA256); err != nil {
		return fmt.Errorf("invalid public policy identity: %w", err)
	}
	limit := policy.Policy.Complexity.MaxFuncLOC
	if limit <= 0 || limit > config.AuditMaxFuncLOC {
		return errors.New("public audit policy must preserve the positive 60-line ceiling")
	}
	return policy.VerifyDigest()
}

func matchPublicPolicy(planned, applied *config.EffectivePolicy) error {
	if err := validatePublicPolicy(planned); err != nil {
		return err
	}
	if err := validatePublicPolicy(applied); err != nil {
		return err
	}
	if planned.SHA256 != applied.SHA256 || planned.Policy.Complexity != applied.Policy.Complexity {
		return errors.New("effective public audit policy changed after planning")
	}
	return nil
}

func validatePublicAnchor(anchor publicPolicyAnchor) error {
	if err := validatePublicPolicy(anchor.Policy); err != nil {
		return err
	}
	return validatePublicScan(anchor.Scan)
}

func validatePublicScan(scan *hiss.ScanReport) error {
	if scan == nil {
		return errors.New("public verification requires an independent original scan")
	}
	if scan.Truncated {
		return hiss.ErrScanTruncated
	}
	if len(scan.Violations) > hiss.MaxInfractionsCap || scan.TotalInfractions != len(scan.Violations) {
		return errors.New("public scan count does not match its bounded entries")
	}
	return nil
}
