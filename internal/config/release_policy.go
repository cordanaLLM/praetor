// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	// MaxReleaseE2EPaths bounds the paths sequence in release.e2e.paths (HISS-02).
	MaxReleaseE2EPaths = MaxDocumentationStyleExclusions
	// MaxReleaseE2ETests bounds the tests sequence in release.e2e.tests (HISS-02).
	MaxReleaseE2ETests = 256
	// MaxReleaseE2ETestIDLen bounds one declared end-to-end test identifier in bytes.
	MaxReleaseE2ETestIDLen = 128
	// minReleaseCommitSHALen is the shortest git commit prefix accepted in release.e2e.since.
	minReleaseCommitSHALen = 7
	// maxReleaseCommitSHALen is the full SHA-1 hex digest length accepted in release.e2e.since.
	maxReleaseCommitSHALen = 40
)

var (
	releasePolicyKeys    = []string{"e2e"}
	releaseE2EPolicyKeys = []string{"paths", "tests", "since"}
)

// defaultReleaseE2EPaths is the built-in end-to-end test path glob list used when
// release.e2e.paths is omitted or empty (order engine-release-feature-e2e-gate-20261027).
var defaultReleaseE2EPaths = []string{
	"e2e/**",
	"tests/e2e/**",
	"test/e2e/**",
	"**/*_e2e_test.go",
	"**/*.e2e.ts",
	"**/*.e2e.js",
	"playwright/**",
	"cypress/**",
}

// DefaultReleaseE2EPaths returns a copy of the built-in end-to-end test path globs.
func DefaultReleaseE2EPaths() []string {
	out := make([]string, len(defaultReleaseE2EPaths))
	copy(out, defaultReleaseE2EPaths)
	return out
}

// ReleasePolicy configures release governance checks in .standards.yaml
// (order engine-release-feature-e2e-gate-20261027).
type ReleasePolicy struct {
	// E2E configures the release end-to-end evidence check for feat and fix commits.
	E2E *ReleaseE2EPolicy `yaml:"e2e,omitempty"`
}

// ReleaseE2EPolicy configures the release end-to-end test evidence gate under release.e2e
// (order engine-release-feature-e2e-gate-20261027).
type ReleaseE2EPolicy struct {
	// Paths lists repository-relative slash globs that count as end-to-end test paths.
	// When empty, DefaultReleaseE2EPaths applies.
	Paths []string `yaml:"paths,omitempty"`
	// Tests lists declared end-to-end test identifiers accepted in E2E-Test commit trailers.
	Tests []string `yaml:"tests,omitempty"`
	// Since is the baseline commit SHA; commits reachable from Since are exempt.
	Since string `yaml:"since,omitempty"`
}

// EffectivePaths returns the configured end-to-end test path globs, or DefaultReleaseE2EPaths
// when none are configured.
func (p *ReleaseE2EPolicy) EffectivePaths() []string {
	if p == nil || len(p.Paths) == 0 {
		return DefaultReleaseE2EPaths()
	}
	out := make([]string, len(p.Paths))
	copy(out, p.Paths)
	return out
}

// UnmarshalYAML decodes release with strict key and type checks.
func (p *ReleasePolicy) UnmarshalYAML(value *yaml.Node) error {
	if err := requireKnownKeys(value, "release", releasePolicyKeys); err != nil {
		return err
	}
	for i := 0; i+1 < len(value.Content) && i < 4; i += 2 {
		if value.Content[i].Value == "e2e" {
			var e2e ReleaseE2EPolicy
			if err := e2e.UnmarshalYAML(value.Content[i+1]); err != nil {
				return err
			}
			p.E2E = &e2e
		}
	}
	return nil
}

// UnmarshalYAML decodes release.e2e with strict key and type checks.
func (p *ReleaseE2EPolicy) UnmarshalYAML(value *yaml.Node) error {
	if err := requireKnownKeys(value, "release.e2e", releaseE2EPolicyKeys); err != nil {
		return err
	}
	for i := 0; i+1 < len(value.Content) && i < 8; i += 2 {
		key, val := value.Content[i].Value, value.Content[i+1]
		if err := p.unmarshalField(key, val); err != nil {
			return err
		}
	}
	return nil
}

func (p *ReleaseE2EPolicy) unmarshalField(key string, val *yaml.Node) error {
	var err error
	switch key {
	case "paths":
		p.Paths, err = stringSequence(val, "release.e2e.paths", MaxReleaseE2EPaths)
	case "tests":
		p.Tests, err = stringSequence(val, "release.e2e.tests", MaxReleaseE2ETests)
	case "since":
		p.Since, err = stringScalar(val, "release.e2e.since")
	}
	return err
}

func validateManifestRelease(m *Manifest) error {
	if m == nil || m.Release == nil {
		return nil
	}
	return m.Release.Validate()
}

// Validate checks a decoded ReleasePolicy against path, test-id, and commit-SHA bounds.
func (p *ReleasePolicy) Validate() error {
	if p == nil || p.E2E == nil {
		return nil
	}
	return p.E2E.Validate()
}

// Validate checks a decoded ReleaseE2EPolicy against path, test-id, and commit-SHA bounds.
func (p *ReleaseE2EPolicy) Validate() error {
	if p == nil {
		return nil
	}
	if err := validateReleaseE2EPaths(p.Paths); err != nil {
		return err
	}
	if err := validateReleaseE2ETests(p.Tests); err != nil {
		return err
	}
	return validateReleaseE2ESince(p.Since)
}

func validateReleaseE2EPaths(paths []string) error {
	if len(paths) > MaxReleaseE2EPaths {
		return fmt.Errorf("release.e2e.paths has %d entries; the maximum is %d",
			len(paths), MaxReleaseE2EPaths)
	}
	seen := make(map[string]int, len(paths))
	for i := 0; i < len(paths) && i < MaxReleaseE2EPaths; i++ {
		pattern := paths[i]
		if problem := StyleExclusionProblem(pattern); problem != "" {
			return fmt.Errorf("release.e2e.paths[%d] %q %s", i, pattern, problem)
		}
		if first, dup := seen[pattern]; dup {
			return fmt.Errorf("release.e2e.paths[%d] duplicates release.e2e.paths[%d] (%q)",
				i, first, pattern)
		}
		seen[pattern] = i
	}
	return nil
}

func validateReleaseE2ETests(tests []string) error {
	if len(tests) > MaxReleaseE2ETests {
		return fmt.Errorf("release.e2e.tests has %d entries; the maximum is %d",
			len(tests), MaxReleaseE2ETests)
	}
	seen := make(map[string]int, len(tests))
	for i := 0; i < len(tests) && i < MaxReleaseE2ETests; i++ {
		id := tests[i]
		if problem := releaseE2ETestIDProblem(id); problem != "" {
			return fmt.Errorf("release.e2e.tests[%d] %q %s", i, id, problem)
		}
		if first, dup := seen[id]; dup {
			return fmt.Errorf("release.e2e.tests[%d] duplicates release.e2e.tests[%d] (%q)",
				i, first, id)
		}
		seen[id] = i
	}
	return nil
}

func releaseE2ETestIDProblem(id string) string {
	if id == "" || strings.TrimSpace(id) == "" {
		return "must not be empty"
	}
	if len(id) > MaxReleaseE2ETestIDLen {
		return fmt.Sprintf("is %d bytes; the maximum is %d", len(id), MaxReleaseE2ETestIDLen)
	}
	if strings.TrimSpace(id) != id {
		return "must not have leading or trailing whitespace"
	}
	for i := 0; i < len(id) && i < MaxReleaseE2ETestIDLen; i++ {
		if !validReleaseTestIDByte(id[i]) {
			return "must contain only ASCII letters, digits, '.', '_', '-', '/', or ':'"
		}
	}
	return ""
}

func validReleaseTestIDByte(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '.' || c == '_' || c == '-' || c == '/' || c == ':'
}

func validateReleaseE2ESince(since string) error {
	if since == "" {
		return nil
	}
	if len(since) < minReleaseCommitSHALen || len(since) > maxReleaseCommitSHALen {
		return fmt.Errorf("release.e2e.since %q must be a %d..%d character hex commit SHA",
			since, minReleaseCommitSHALen, maxReleaseCommitSHALen)
	}
	for i := 0; i < len(since) && i < maxReleaseCommitSHALen; i++ {
		if !isHexByte(since[i]) {
			return fmt.Errorf("release.e2e.since %q must contain only hexadecimal characters", since)
		}
	}
	return nil
}

func isHexByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
