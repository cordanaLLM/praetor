// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

const (
	// HostedGateDraftFail is hosted_gates.draft by default: a hosted gate fails a draft pull
	// request in a first step (internal/ghworkflow, HostedGateDraftStep, #815).
	HostedGateDraftFail = "fail"
	// HostedGateDraftSkip is the opt-in that skips a draft at the job level instead (#857). The
	// audit accepts it only where a proven aggregate keeps the required check failing on a
	// draft (internal/forge, DraftSkipFault).
	HostedGateDraftSkip = "skip"
)

// HostedGatesPolicy is the manifest's hosted_gates section: how the hosted gates Praetor manages
// (.github/workflows/praetor-api.yml and praetor-docs.yml) treat a draft pull request. It is
// repository-only and stays out of ResolvedPolicy, like Documentation.
type HostedGatesPolicy struct {
	// Draft is HostedGateDraftFail or HostedGateDraftSkip; empty means HostedGateDraftFail.
	Draft string `yaml:"draft,omitempty"`
}

// DraftSkip reports whether the section selects the job-level draft skip. It is safe on a nil
// section, which keeps the fail-closed default.
func (p *HostedGatesPolicy) DraftSkip() bool {
	return p != nil && p.Draft == HostedGateDraftSkip
}

// UnmarshalYAML decodes the section strictly: draft is the only key, and a string.
func (p *HostedGatesPolicy) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("hosted_gates must be a mapping")
	}
	var policy HostedGatesPolicy
	for i := 0; i+1 < len(node.Content) && i < 2*MaxHostedGatesKeys; i += 2 {
		if node.Content[i].Value != "draft" {
			return fmt.Errorf("hosted_gates.%s is not a known key; the only key is draft", node.Content[i].Value)
		}
		if policy.Draft != "" {
			return fmt.Errorf("hosted_gates.draft is declared twice")
		}
		value, err := stringScalar(node.Content[i+1], "hosted_gates.draft")
		if err != nil {
			return err
		}
		policy.Draft = value
	}
	*p = policy
	return nil
}

// MaxHostedGatesKeys bounds the keys read from the hosted_gates section (HISS-02).
const MaxHostedGatesKeys = 8

func validateManifestHostedGates(m *Manifest) error {
	if m == nil || m.HostedGates == nil {
		return nil
	}
	switch m.HostedGates.Draft {
	case "", HostedGateDraftFail, HostedGateDraftSkip:
		return nil
	}
	return fmt.Errorf("hosted_gates.draft %q must be %q (the default) or %q", m.HostedGates.Draft, HostedGateDraftFail, HostedGateDraftSkip)
}
