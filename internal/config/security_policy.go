// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/util"
)

// DefaultGoVEXPath is the OpenVEX document the Go vulnerability gate (internal/govuln) reads when
// the manifest declares no security.go_vex.
const DefaultGoVEXPath = "security/vex/go.openvex.json"

// SecurityPolicy is the manifest's security section: settings of the security gates. GoVEX names
// the OpenVEX document whose not_affected statements cover the advisories govulncheck finds
// present but not called (internal/govuln). It is repository-only, like Documentation, and stays
// out of ResolvedPolicy: the document records this repository's own analysis.
type SecurityPolicy struct {
	GoVEX string `yaml:"go_vex,omitempty"`
}

// GoVEXPath returns the declared OpenVEX document, or DefaultGoVEXPath when the section or the key
// is absent. It is safe on a nil policy.
func (p *SecurityPolicy) GoVEXPath() string {
	if p == nil || p.GoVEX == "" {
		return DefaultGoVEXPath
	}
	return p.GoVEX
}

// validateManifestSecurity holds a declared security.go_vex to the repository path shape every
// other manifest path takes (ValidRepositoryPath).
func validateManifestSecurity(m *Manifest) error {
	if m == nil || m.Security == nil || m.Security.GoVEX == "" {
		return nil
	}
	if !ValidRepositoryPath(m.Security.GoVEX) {
		return fmt.Errorf("security.go_vex %q must be a clean local forward-slash path of at most %d bytes",
			m.Security.GoVEX, maxRepositoryPath)
	}
	return nil
}

// RepositoryGoVEXPath returns the OpenVEX document the Go vulnerability gate reads for the
// repository at root, as the forward-slash path its manifest spells: security.go_vex, or
// DefaultGoVEXPath when root has no manifest or the manifest declares none. A manifest that does
// not load is an error, never the default: the gate cannot tell which document the repository
// meant.
func RepositoryGoVEXPath(root string) (string, error) {
	if root == "" {
		root = "."
	}
	manifestPath := filepath.Join(root, ManifestFileName)
	if !util.FileExists(manifestPath) {
		return DefaultGoVEXPath, nil
	}
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return "", err
	}
	return manifest.Security.GoVEXPath(), nil
}
