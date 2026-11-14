// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"errors"
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

// SecurityChannel names the private reporting channel a repository's SECURITY.md directs
// reporters to (security.channel). The audit checks the policy for that channel's concrete
// artifact, a link or an address, and never reads the policy's prose.
type SecurityChannel string

const (
	// SecurityChannelPrivateReporting is GitHub's private vulnerability reporting; the policy
	// links the repository's report form (/security/advisories/new).
	SecurityChannelPrivateReporting SecurityChannel = "private-reporting"
	// SecurityChannelConfidentialIssues is a GitLab confidential issue; the policy links the
	// new-issue form with issue[confidential]=true.
	SecurityChannelConfidentialIssues SecurityChannel = "confidential-issues"
	// SecurityChannelContact is the address in security.contact; the policy names it verbatim.
	SecurityChannelContact SecurityChannel = "contact"
)

// SecurityChannelKey is the manifest key that declares the private reporting channel.
const SecurityChannelKey = "security.channel"

// securityChannels lists every declarable channel in the order error messages name them.
var securityChannels = [...]SecurityChannel{
	SecurityChannelPrivateReporting, SecurityChannelConfidentialIssues, SecurityChannelContact,
}

// ErrSecurityChannelUndeclared marks a repository whose channel has no migration default:
// no security.channel, no security.contact, and a forge without a native private channel.
var ErrSecurityChannelUndeclared = errors.New(SecurityChannelKey + " required")

// SecurityPolicy is the manifest's security section: settings of the security gates. GoVEX names
// the OpenVEX document whose not_affected statements cover the advisories govulncheck finds
// present but not called (internal/govuln). Channel declares the private reporting channel
// SECURITY.md names (SecurityChannel), and Contact the address the contact channel uses (email
// address, mailto:, or HTTPS destination). It is repository-only, like Documentation, and stays
// out of ResolvedPolicy: the document records this repository's own analysis.
type SecurityPolicy struct {
	GoVEX   string          `yaml:"go_vex,omitempty"`
	Channel SecurityChannel `yaml:"channel,omitempty"`
	Contact string          `yaml:"contact,omitempty"`
}

// SecurityChannelChoice is the channel a repository's policy is checked against, and why:
// Declared when security.channel names it; otherwise Basis names the migration default's input.
type SecurityChannelChoice struct {
	Channel  SecurityChannel
	Declared bool
	Basis    string
}

// ResolveSecurityChannel returns the declared security.channel. An undeclared channel takes the
// migration default: contact when security.contact is set, private-reporting on GitHub and
// confidential-issues on GitLab. Any other forge, and an unresolved one, has no default, and the
// result wraps ErrSecurityChannelUndeclared. forge is the repository's resolved forge, or "".
// Safe on a nil policy.
func ResolveSecurityChannel(p *SecurityPolicy, forge Forge) (SecurityChannelChoice, error) {
	if p != nil && p.Channel != "" {
		return SecurityChannelChoice{Channel: p.Channel, Declared: true, Basis: SecurityChannelKey}, nil
	}
	if p.SecurityContact() != "" {
		return SecurityChannelChoice{Channel: SecurityChannelContact, Basis: "security.contact is set"}, nil
	}
	switch forge {
	case ForgeGitHub:
		return SecurityChannelChoice{Channel: SecurityChannelPrivateReporting, Basis: "forge github"}, nil
	case ForgeGitLab:
		return SecurityChannelChoice{Channel: SecurityChannelConfidentialIssues, Basis: "forge gitlab"}, nil
	}
	where := "an unresolved forge"
	if forge != "" {
		where = "forge " + string(forge)
	}
	return SecurityChannelChoice{}, fmt.Errorf("%w: %s has no native private reporting channel; declare %s %s with security.contact in %s",
		ErrSecurityChannelUndeclared, where, SecurityChannelKey, SecurityChannelContact, ManifestFileName)
}

// validSecurityChannel reports whether channel is one of securityChannels.
func validSecurityChannel(channel SecurityChannel) bool {
	for i := 0; i < len(securityChannels); i++ {
		if securityChannels[i] == channel {
			return true
		}
	}
	return false
}

// securityChannelChoices lists the declarable channels for error messages.
func securityChannelChoices() string {
	names := make([]string, 0, len(securityChannels))
	for i := 0; i < len(securityChannels); i++ {
		names = append(names, string(securityChannels[i]))
	}
	return strings.Join(names, ", ")
}

// SecurityChannelForge is the only forge a forge-native channel runs on, or "" for contact,
// which runs on any forge.
func SecurityChannelForge(channel SecurityChannel) Forge {
	switch channel {
	case SecurityChannelPrivateReporting:
		return ForgeGitHub
	case SecurityChannelConfidentialIssues:
		return ForgeGitLab
	}
	return ""
}

// validateSecurityChannel holds a declared security.channel to the allow-list, the contact
// channel to a declared security.contact, and a forge-native channel to a matching declared
// repository.forge.
func validateSecurityChannel(m *Manifest) error {
	channel := m.Security.Channel
	if channel == "" {
		return nil
	}
	if !validSecurityChannel(channel) {
		return fmt.Errorf("%s %q unsupported: one of %s", SecurityChannelKey, channel, securityChannelChoices())
	}
	if channel == SecurityChannelContact && m.Security.Contact == "" {
		return fmt.Errorf("%s %s requires security.contact", SecurityChannelKey, channel)
	}
	if want := SecurityChannelForge(channel); want != "" && m.Repository.Forge != "" && m.Repository.Forge != want {
		return fmt.Errorf("%s %s runs on forge %s, but %s is %s", SecurityChannelKey, channel, want, ForgeKey, m.Repository.Forge)
	}
	return nil
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
// other manifest path takes (ValidRepositoryPath) and validates any declared security.contact and
// security.channel (validateSecurityChannel).
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
	return validateSecurityChannel(m)
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
