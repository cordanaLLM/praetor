// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// VerificationPolicy is the manifest's verification section: the bounds of the repository
// discovery walk (internal/adopt's verification planner), recorded once for every run that reads
// the repository's languages and build markers. Adoption, `praetorctl paperclip harness` and the
// Paperclip gate of `praetorctl audit` read it through internal/adopt.ResolveVerificationLimits,
// so the hooks and CI jobs that run the audit without flags walk a large repository as far as an
// adoption run does (#321, #535). A run's --verification-max-* flag still overrides the bound it
// names. A zero field is a bound the section does not declare, which keeps the default; a
// declared value must lie in 1..its ceiling (util.DiscoveryEntriesCeiling, DiscoveryFilesCeiling
// and DiscoveryDepthCeiling), the range the flags accept. A declared value below the default
// lowers the bound the way the flag does: the walk then stops earlier and fails naming the
// bound and the value reached, never silently.
type VerificationPolicy struct {
	MaxEntries int `yaml:"max_entries,omitempty"`
	MaxFiles   int `yaml:"max_files,omitempty"`
	MaxDepth   int `yaml:"max_depth,omitempty"`
}

// verificationBound is one key of the verification section with the ceiling it may reach.
type verificationBound struct {
	key     string
	ceiling int
}

// verificationBounds lists the verification section's keys in the order they are checked.
var verificationBounds = [...]verificationBound{
	{"max_entries", util.DiscoveryEntriesCeiling},
	{"max_files", util.DiscoveryFilesCeiling},
	{"max_depth", util.DiscoveryDepthCeiling},
}

// UnmarshalYAML decodes the section strictly at the source boundary, so every manifest decoder
// (DecodeManifest included, which skips the policy validations) refuses it alike: known keys
// once each, integers written as integers, and each declared value within 1..its ceiling.
func (p *VerificationPolicy) UnmarshalYAML(node *yaml.Node) error {
	var policy VerificationPolicy
	targets := map[string]*int{"max_entries": &policy.MaxEntries, "max_files": &policy.MaxFiles, "max_depth": &policy.MaxDepth}
	present, err := registerIntFields(node, "verification", targets)
	if err != nil {
		return err
	}
	for _, bound := range verificationBounds {
		if !present[bound.key] {
			continue
		}
		if err := validateDeclaredBound("verification", bound.key, targets[bound.key], 1, bound.ceiling); err != nil {
			return err
		}
	}
	*p = policy
	return nil
}

// DeclaredVerification returns the manifest's verification section, nil for a nil manifest or
// one that declares none.
func (m *Manifest) DeclaredVerification() *VerificationPolicy {
	if m == nil {
		return nil
	}
	return m.Verification
}
