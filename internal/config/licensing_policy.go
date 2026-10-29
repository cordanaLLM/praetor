// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"strings"
)

// Bounds for licensing configuration (HISS-02, order engine-fr-rule-checks-20261027).
const (
	MaxCopyrightHolderBytes = 256
)

// LicensingPolicy holds the licensing section of .standards.yaml (order engine-fr-rule-checks-20261027).
type LicensingPolicy struct {
	CopyrightHolder string `yaml:"copyright_holder,omitempty"`
}

// DeclaredCopyrightHolder returns the trimmed declared copyright holder, or "" if none.
func (p *LicensingPolicy) DeclaredCopyrightHolder() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.CopyrightHolder)
}

// DeclaredCopyrightHolder returns the manifest's declared licensing.copyright_holder.
func (m *Manifest) DeclaredCopyrightHolder() string {
	if m == nil || m.Licensing == nil {
		return ""
	}
	return m.Licensing.DeclaredCopyrightHolder()
}

// validateManifestLicensing validates the licensing section of the manifest.
func validateManifestLicensing(m *Manifest) error {
	if m == nil || m.Licensing == nil {
		return nil
	}
	holder := m.Licensing.CopyrightHolder
	if len(holder) > MaxCopyrightHolderBytes {
		return fmt.Errorf("licensing.copyright_holder exceeds maximum length of %d bytes", MaxCopyrightHolderBytes)
	}
	if strings.ContainsAny(holder, "\r\n") {
		return fmt.Errorf("licensing.copyright_holder must not contain newlines")
	}
	return nil
}
