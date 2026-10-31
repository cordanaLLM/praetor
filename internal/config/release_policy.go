// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	// MaxReleaseE2EPaths bounds the paths sequence in release.e2e.paths (HISS-02).
	MaxReleaseE2EPaths = MaxDocumentationStyleExclusions
	// MaxReleaseE2ETests bounds the tests sequence in release.e2e.tests (HISS-02).
	MaxReleaseE2ETests = 256
	// MaxReleaseE2ETestIDLen bounds one declared end-to-end test identifier in bytes.
	MaxReleaseE2ETestIDLen = 128
	// MaxReleaseTestLevels bounds the number of entries in release.test_levels (HISS-02).
	MaxReleaseTestLevels = 32
	// MaxReleaseTestLevelFieldLen bounds pattern, job, and run strings in release.test_levels.
	MaxReleaseTestLevelFieldLen = 256
	// minReleaseCommitSHALen is the shortest git commit prefix accepted in release.e2e.since.
	minReleaseCommitSHALen = 7
	// maxReleaseCommitSHALen is the full SHA-1 hex digest length accepted in release.e2e.since.
	maxReleaseCommitSHALen = 40
)

var (
	releasePolicyKeys        = []string{"e2e", "test_levels"}
	releaseE2EPolicyKeys     = []string{"paths", "tests", "since"}
	releaseTestLevelSpecKeys = []string{"paths", "pattern", "job", "run"}
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
	// TestLevels binds release test levels to repository test paths and CI workflow steps (#1733).
	TestLevels map[string]ReleaseTestLevelSpec `yaml:"test_levels,omitempty"`
}

// ReleaseTestLevelSpec configures one release test level under release.test_levels.<level> (#1733).
type ReleaseTestLevelSpec struct {
	// Paths lists repository-relative slash globs matching test files for this level.
	Paths []string `yaml:"paths,omitempty"`
	// Pattern is an optional regular expression that at least one matched file's content must satisfy.
	Pattern string `yaml:"pattern,omitempty"`
	// Job is an optional GitHub Actions workflow job id or name that runs this level.
	Job string `yaml:"job,omitempty"`
	// Run is an optional command substring that a workflow step must run.
	Run string `yaml:"run,omitempty"`
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
	for i := 0; i+1 < len(value.Content) && i < 8; i += 2 {
		switch value.Content[i].Value {
		case "e2e":
			var e2e ReleaseE2EPolicy
			if err := e2e.UnmarshalYAML(value.Content[i+1]); err != nil {
				return err
			}
			p.E2E = &e2e
		case "test_levels":
			levels, err := unmarshalReleaseTestLevels(value.Content[i+1])
			if err != nil {
				return err
			}
			p.TestLevels = levels
		}
	}
	return nil
}

func unmarshalReleaseTestLevels(value *yaml.Node) (map[string]ReleaseTestLevelSpec, error) {
	if value == nil || value.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("release.test_levels must be a mapping")
	}
	if len(value.Content)/2 > MaxReleaseTestLevels {
		return nil, fmt.Errorf("release.test_levels has %d entries; the maximum is %d",
			len(value.Content)/2, MaxReleaseTestLevels)
	}
	levels := make(map[string]ReleaseTestLevelSpec, len(value.Content)/2)
	for i := 0; i+1 < len(value.Content) && i < MaxReleaseTestLevels*2; i += 2 {
		level := value.Content[i].Value
		specNode := value.Content[i+1]
		if _, exists := levels[level]; exists {
			return nil, fmt.Errorf("release.test_levels: duplicate key %q", level)
		}
		spec, err := unmarshalReleaseTestLevelSpec(level, specNode)
		if err != nil {
			return nil, err
		}
		levels[level] = spec
	}
	return levels, nil
}

func unmarshalReleaseTestLevelSpec(level string, value *yaml.Node) (ReleaseTestLevelSpec, error) {
	prefix := "release.test_levels." + level
	if value == nil || (value.Kind == yaml.ScalarNode && (value.ShortTag() == "!!null" || value.Value == "" || value.Value == "false" || value.Value == "none")) {
		return ReleaseTestLevelSpec{}, fmt.Errorf("%s cannot drop a required test level", prefix)
	}
	if err := requireKnownKeys(value, prefix, releaseTestLevelSpecKeys); err != nil {
		return ReleaseTestLevelSpec{}, err
	}
	var spec ReleaseTestLevelSpec
	for i := 0; i+1 < len(value.Content) && i < 16; i += 2 {
		if err := spec.unmarshalField(prefix, value.Content[i].Value, value.Content[i+1]); err != nil {
			return ReleaseTestLevelSpec{}, err
		}
	}
	return spec, nil
}

func (s *ReleaseTestLevelSpec) unmarshalField(prefix, key string, val *yaml.Node) error {
	var err error
	switch key {
	case "paths":
		s.Paths, err = stringSequence(val, prefix+".paths", MaxReleaseE2EPaths)
	case "pattern":
		s.Pattern, err = stringScalar(val, prefix+".pattern")
	case "job":
		s.Job, err = stringScalar(val, prefix+".job")
	case "run":
		s.Run, err = stringScalar(val, prefix+".run")
	}
	return err
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
	if err := m.Release.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.Release.ValidateForProfiles(ctx, "", m.Profiles)
}

// Validate checks a decoded ReleasePolicy against path, test-id, commit-SHA, and test_levels bounds.
func (p *ReleasePolicy) Validate() error {
	if p == nil {
		return nil
	}
	if p.E2E != nil {
		if err := p.E2E.Validate(); err != nil {
			return err
		}
	}
	return validateReleaseTestLevelsMap(p.TestLevels)
}

// ValidateForProfiles verifies that a declared release.test_levels map does not drop any level
// required by the given archetype profiles (#1733).
func (p *ReleasePolicy) ValidateForProfiles(ctx context.Context, root string, profiles []string) error {
	if p == nil || p.TestLevels == nil || len(profiles) == 0 {
		return nil
	}
	byProfile, err := ProfileReleaseTestLevels(ctx, root, profiles)
	if err != nil {
		return err
	}
	for i := 0; i < len(profiles) && i < maxLockEntries; i++ {
		profile := profiles[i]
		required := byProfile[profile]
		for j := 0; j < len(required) && j < maxPolicyNames; j++ {
			level := required[j]
			if _, ok := p.TestLevels[level]; !ok {
				return fmt.Errorf("release.test_levels cannot drop required test level %q for profile %q",
					level, profile)
			}
		}
	}
	return nil
}

func validateReleaseTestLevelsMap(levels map[string]ReleaseTestLevelSpec) error {
	if len(levels) == 0 {
		return nil
	}
	if len(levels) > MaxReleaseTestLevels {
		return fmt.Errorf("release.test_levels has %d entries; the maximum is %d",
			len(levels), MaxReleaseTestLevels)
	}
	names := make([]string, 0, len(levels))
	for name := range levels {
		names = append(names, name)
	}
	slices.Sort(names)
	for i := 0; i < len(names) && i < MaxReleaseTestLevels; i++ {
		name := names[i]
		if err := levels[name].Validate(name); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks a single ReleaseTestLevelSpec under release.test_levels.<level>.
func (s ReleaseTestLevelSpec) Validate(level string) error {
	if problem := releaseE2ETestIDProblem(level); problem != "" {
		return fmt.Errorf("release.test_levels key %q %s", level, problem)
	}
	prefix := "release.test_levels." + level
	if len(s.Paths) == 0 {
		return fmt.Errorf("%s.paths must declare at least one path glob", prefix)
	}
	if err := validateReleasePathGlobs(s.Paths, prefix+".paths"); err != nil {
		return err
	}
	if err := validateReleaseTestLevelPattern(prefix, s.Pattern); err != nil {
		return err
	}
	if err := validateReleaseTestLevelField(prefix+".job", s.Job); err != nil {
		return err
	}
	return validateReleaseTestLevelField(prefix+".run", s.Run)
}

func validateReleaseTestLevelPattern(prefix, pattern string) error {
	if pattern == "" {
		return nil
	}
	if len(pattern) > MaxReleaseTestLevelFieldLen {
		return fmt.Errorf("%s.pattern is %d bytes; the maximum is %d",
			prefix, len(pattern), MaxReleaseTestLevelFieldLen)
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("%s.pattern invalid regular expression: %w", prefix, err)
	}
	return nil
}

func validateReleaseTestLevelField(field, val string) error {
	if val == "" {
		return nil
	}
	if strings.TrimSpace(val) == "" {
		return fmt.Errorf("%s must not be blank", field)
	}
	if len(val) > MaxReleaseTestLevelFieldLen {
		return fmt.Errorf("%s is %d bytes; the maximum is %d",
			field, len(val), MaxReleaseTestLevelFieldLen)
	}
	return nil
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
	return validateReleasePathGlobs(paths, "release.e2e.paths")
}

func validateReleasePathGlobs(paths []string, field string) error {
	if len(paths) > MaxReleaseE2EPaths {
		return fmt.Errorf("%s has %d entries; the maximum is %d",
			field, len(paths), MaxReleaseE2EPaths)
	}
	seen := make(map[string]int, len(paths))
	for i := 0; i < len(paths) && i < MaxReleaseE2EPaths; i++ {
		pattern := paths[i]
		if problem := StyleExclusionProblem(pattern); problem != "" {
			return fmt.Errorf("%s[%d] %q %s", field, i, pattern, problem)
		}
		if first, dup := seen[pattern]; dup {
			return fmt.Errorf("%s[%d] duplicates %s[%d] (%q)",
				field, i, field, first, pattern)
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
