// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"time"

	"go.yaml.in/yaml/v3"
)

// MaxDeclaredVerifyTimeout is the maximum duration gate.declared_verify_timeout may declare (30 min).
// It also sets the ceiling for the race-detector test stage timeout (gating.MaxTestStageTimeout).
const MaxDeclaredVerifyTimeout = 30 * time.Minute

// GatePolicy is the manifest's gate section: settings of the anti-direct-merge gating pipeline.
// declared_verify_timeout sets the stage bound for the declared-verification stage. It is
// repository-only, like Documentation and Security, and stays out of ResolvedPolicy.
type GatePolicy struct {
	DeclaredVerifyTimeout string `yaml:"declared_verify_timeout,omitempty"`
}

// gateKeys is the closed key set of the gate section.
var gateKeys = []string{"declared_verify_timeout"}

// UnmarshalYAML checks that all keys are known before decoding GatePolicy.
func (p *GatePolicy) UnmarshalYAML(node *yaml.Node) error {
	if err := requireKnownKeys(node, "gate", gateKeys); err != nil {
		return err
	}
	type rawGatePolicy GatePolicy
	var decoded rawGatePolicy
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*p = GatePolicy(decoded)
	return nil
}

// DeclaredVerifyTimeoutDuration returns the declared verify-all timeout as a time.Duration,
// or 0 if unset. ValidateGate has already verified that if set, it is positive and <= MaxDeclaredVerifyTimeout.
func (p *GatePolicy) DeclaredVerifyTimeoutDuration() time.Duration {
	if p == nil || p.DeclaredVerifyTimeout == "" {
		return 0
	}
	d, err := time.ParseDuration(p.DeclaredVerifyTimeout)
	if err != nil {
		return 0
	}
	return d
}

// validateManifestGate validates the gate section of the manifest. A declared value above
// MaxDeclaredVerifyTimeout (30 min) or not positive is refused with an error naming the key,
// the value and the ceiling (no silent clamp).
func validateManifestGate(m *Manifest) error {
	if m == nil || m.Gate == nil || m.Gate.DeclaredVerifyTimeout == "" {
		return nil
	}
	parsed, err := time.ParseDuration(m.Gate.DeclaredVerifyTimeout)
	if err != nil {
		return fmt.Errorf("gate.declared_verify_timeout %q is not a duration (ceiling %s): %w",
			m.Gate.DeclaredVerifyTimeout, MaxDeclaredVerifyTimeout, err)
	}
	if parsed <= 0 {
		return fmt.Errorf("gate.declared_verify_timeout %s is not positive (ceiling %s)",
			m.Gate.DeclaredVerifyTimeout, MaxDeclaredVerifyTimeout)
	}
	if parsed > MaxDeclaredVerifyTimeout {
		return fmt.Errorf("gate.declared_verify_timeout %s exceeds the %s ceiling",
			m.Gate.DeclaredVerifyTimeout, MaxDeclaredVerifyTimeout)
	}
	return nil
}
