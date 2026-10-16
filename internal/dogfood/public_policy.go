package dogfood

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

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
		return fmt.Errorf("validate planned public policy: %w", err)
	}
	if err := validatePublicPolicy(applied); err != nil {
		return fmt.Errorf("validate applied public policy: %w", err)
	}
	if planned.Policy.Complexity != applied.Policy.Complexity {
		return errors.New("effective public audit policy changed after planning: Complexity")
	}
	equal, err := resolvedPoliciesEqual(planned.Policy, applied.Policy)
	if err != nil {
		return err
	}
	if equal {
		return nil
	}
	section, err := firstPublicPolicyDifference(planned.Policy, applied.Policy)
	if err != nil {
		return err
	}
	return fmt.Errorf("effective public audit policy changed after planning: %s", section)
}

func resolvedPoliciesEqual(planned, applied config.ResolvedPolicy) (bool, error) {
	plannedJSON, err := json.Marshal(planned)
	if err != nil {
		return false, fmt.Errorf("encode planned resolved policy: %w", err)
	}
	appliedJSON, err := json.Marshal(applied)
	if err != nil {
		return false, fmt.Errorf("encode applied resolved policy: %w", err)
	}
	return bytes.Equal(plannedJSON, appliedJSON), nil
}

// firstPublicPolicyDifference names the first top-level section of the resolved policy whose
// values differ. It walks the struct's fields by reflection rather than naming each control: a
// comparison reads every control without acting on any, so it must not count as a consumer in
// the archetype coverage manifest, and a field added to config.ResolvedPolicy is compared without
// an edit here.
func firstPublicPolicyDifference(planned, applied config.ResolvedPolicy) (string, error) {
	plannedValue, appliedValue := reflect.ValueOf(planned), reflect.ValueOf(applied)
	typ := plannedValue.Type()
	for i := 0; i < typ.NumField() && i < maxPublicPolicySections; i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		plannedJSON, err := json.Marshal(plannedValue.Field(i).Interface())
		if err != nil {
			return "", fmt.Errorf("encode planned public policy section %s: %w", field.Name, err)
		}
		appliedJSON, err := json.Marshal(appliedValue.Field(i).Interface())
		if err != nil {
			return "", fmt.Errorf("encode applied public policy section %s: %w", field.Name, err)
		}
		if !bytes.Equal(plannedJSON, appliedJSON) {
			return field.Name, nil
		}
	}
	return "", errors.New("resolved public policies differ without a top-level section difference")
}

// maxPublicPolicySections bounds the walk over config.ResolvedPolicy's fields (HISS-02).
const maxPublicPolicySections = 64

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
