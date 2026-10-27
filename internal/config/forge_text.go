// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"

	"github.com/cordanaLLM/praetor/internal/util"
)

// MaxOwnOrganizations bounds the additional forge owners a repository trusts.
const MaxOwnOrganizations = 64

// ForgeTextPolicy is repository-scoped; an absent list trusts only the repository owner.
type ForgeTextPolicy struct {
	OwnOrganizations []string `yaml:"own_organizations,omitempty"`
}

func validateManifestForgeText(m *Manifest) error {
	if m == nil || m.Forge == nil {
		return nil
	}
	return ValidateOwnOrganizations(m.Forge.OwnOrganizations)
}

// ValidateOwnOrganizations refuses oversized lists and invalid owners, without clamping.
func ValidateOwnOrganizations(owners []string) error {
	if len(owners) > MaxOwnOrganizations {
		return fmt.Errorf("forge.own_organizations exceeds %d entries", MaxOwnOrganizations)
	}
	for i := 0; i < len(owners) && i < MaxOwnOrganizations; i++ {
		if err := util.ValidateGitHubOwner(owners[i]); err != nil {
			return fmt.Errorf("forge.own_organizations[%d]: %w", i, err)
		}
	}
	return nil
}
