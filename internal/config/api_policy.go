// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"regexp"
	"strings"
)

// Bounds of the api section (HISS-02).
const (
	// MaxAPISystemPackages bounds the Debian packages the API compatibility workflow installs.
	MaxAPISystemPackages = 64
	// MaxAPISystemPackageBytes bounds one package name, so that its line in the rendered workflow
	// stays inside the 80 columns yamllint's default line-length rule allows.
	MaxAPISystemPackageBytes = 64
)

// ExceptionRuleAPICompatibility is the rule of the Go API compatibility gate
// (tools/apicompat/gate): an entry names the go.mod of one module whose packages cannot be built
// on the hosted runner even with api.system_packages installed. While the entry holds, the gate
// reports the module as not compared, with the entry's reason and expiry, and goes on; once it
// expired, the build failure fails the gate as before.
const ExceptionRuleAPICompatibility = "api-compatibility"

// debianPackageName is the Debian Policy name grammar (section 5.6.7): lower case letters, digits
// and the characters + - . only, at least two characters, opening with a letter or a digit.
var debianPackageName = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+$`)

// APIPolicy is the manifest's api section. SystemPackages lists the Debian packages the locked
// API compatibility workflow installs with apt-get on its Linux runner before it builds the
// repository's Go modules, for modules whose packages need a C library or header at build time.
// An absent or empty list renders the workflow without the step. It is repository-only, like
// Documentation, and stays out of ResolvedPolicy.
type APIPolicy struct {
	SystemPackages []string `yaml:"system_packages,omitempty"`
}

// ValidDebianPackageName reports whether name follows the Debian package-name grammar and the
// MaxAPISystemPackageBytes bound.
func ValidDebianPackageName(name string) bool {
	return len(name) <= MaxAPISystemPackageBytes && debianPackageName.MatchString(name)
}

// Packages returns the declared packages; it is safe on a nil policy.
func (p *APIPolicy) Packages() []string {
	if p == nil {
		return nil
	}
	return p.SystemPackages
}

// validateManifestAPI refuses more than MaxAPISystemPackages packages, a name outside the Debian
// package-name grammar and a repeated name, naming api.system_packages and the offending entry.
func validateManifestAPI(m *Manifest) error {
	packages := m.API.Packages()
	if len(packages) > MaxAPISystemPackages {
		return fmt.Errorf("api.system_packages lists %d packages; maximum is %d", len(packages), MaxAPISystemPackages)
	}
	seen := make(map[string]bool, len(packages))
	for index := 0; index < len(packages) && index < MaxAPISystemPackages; index++ {
		name := packages[index]
		if !ValidDebianPackageName(name) {
			return fmt.Errorf("api.system_packages[%d] %q is not a Debian package name (lower case letters, digits and + - . only, "+
				"2 to %d characters, opening with a letter or digit)", index, name, MaxAPISystemPackageBytes)
		}
		if seen[name] {
			return fmt.Errorf("api.system_packages[%d] repeats %q", index, name)
		}
		seen[name] = true
	}
	return nil
}

// apiModuleTargetProblem requires an entry of ExceptionRuleAPICompatibility to name the go.mod of
// one module by path, and a reason the workflow can carry: the reason reaches the workflow's
// environment, where an expression would be evaluated.
func (e Exception) apiModuleTargetProblem() string {
	if e.Rule != ExceptionRuleAPICompatibility {
		return ""
	}
	if e.Path == "" || (e.Path != "go.mod" && !strings.HasSuffix(e.Path, "/go.mod")) {
		return "rule " + ExceptionRuleAPICompatibility + " must name the go.mod of one module by path, such as hw/udev/go.mod"
	}
	if !apiPathSafe(e.Path) {
		return "path of rule " + ExceptionRuleAPICompatibility + " must be a repository path of letters, digits and . _ - / only: the path is written into a workflow"
	}
	if strings.Contains(e.Reason, "${{") {
		return "reason of rule " + ExceptionRuleAPICompatibility + " must not contain \"${{\": the reason is written into a workflow"
	}
	return ""
}

// apiPathSafe reports whether path is a repository path made only of ASCII letters, digits and
// . _ - / : the grammar the rendered APICOMPAT_EXCEPTIONS line carries without an expression
// ("${{"), a quote, a space or a shell metacharacter.
func apiPathSafe(path string) bool {
	if !ValidRepositoryPath(path) {
		return false
	}
	for _, char := range path {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
		case char == '.', char == '_', char == '-', char == '/':
		default:
			return false
		}
	}
	return true
}
