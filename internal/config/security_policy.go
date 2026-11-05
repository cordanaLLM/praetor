// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// DefaultGoVEXPath is the OpenVEX document the Go vulnerability gate (internal/govuln) reads when
// the manifest declares no security.go_vex.
const DefaultGoVEXPath = "security/vex/go.openvex.json"

// securityContactRegex validates that security.contact is an email, mailto: URI, or HTTPS URL.
var securityContactRegex = regexp.MustCompile(`^(?:(?:mailto:)?[a-zA-Z0-9.!#$%&'*+/=?^_{|}~-]+@[a-zA-Z0-9-]+(?:\.[a-zA-Z0-9-]+)+|https://[^\s<>]+)$`)

// SecurityPolicy is the manifest's security section: settings of the security gates. GoVEX names
// the OpenVEX document whose not_affected statements cover the advisories govulncheck finds
// present but not called (internal/govuln). Contact specifies an actionable reporting channel
// (email address, mailto:, or HTTPS destination) for repositories without a native private channel.
// It is repository-only, like Documentation, and stays out of ResolvedPolicy: the document records
// this repository's own analysis.
type SecurityPolicy struct {
	GoVEX   string `yaml:"go_vex,omitempty"`
	Contact string `yaml:"contact,omitempty"`
}

// GoVEXPath returns the declared OpenVEX document, or DefaultGoVEXPath when the section or the key
// is absent. It is safe on a nil policy.
func (p *SecurityPolicy) GoVEXPath() string {
	if p == nil || p.GoVEX == "" {
		return DefaultGoVEXPath
	}
	return p.GoVEX
}

// SecurityContact returns the declared security contact, or "" when unset. Safe on a nil policy.
func (p *SecurityPolicy) SecurityContact() string {
	if p == nil {
		return ""
	}
	return p.Contact
}

// ValidSecurityContact reports whether contact is a valid email, mailto: URI,
// or https:// URL with a non-empty hostname.
func ValidSecurityContact(contact string) bool {
	if !securityContactRegex.MatchString(contact) {
		return false
	}
	if strings.HasPrefix(contact, "https://") {
		u, err := url.Parse(contact)
		if err != nil || u.Hostname() == "" {
			return false
		}
	}
	return true
}

// validateManifestSecurity holds a declared security.go_vex to the repository path shape every
// other manifest path takes (ValidRepositoryPath) and validates any declared security.contact.
func validateManifestSecurity(m *Manifest) error {
	if m == nil || m.Security == nil {
		return nil
	}
	if m.Security.GoVEX != "" && !ValidRepositoryPath(m.Security.GoVEX) {
		return fmt.Errorf("security.go_vex %q must be a clean local forward-slash path of at most %d bytes",
			m.Security.GoVEX, maxRepositoryPath)
	}
	if m.Security.Contact != "" && !ValidSecurityContact(m.Security.Contact) {
		return fmt.Errorf("security.contact %q must be a valid email address, mailto: URI, or https:// URL",
			m.Security.Contact)
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
