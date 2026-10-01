package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Bounds of the devcontainer.freshness block (#338). `praetorctl devcontainer freshness`
// reports how far the committed .devcontainer bundle is behind the working tree and fails only
// past one of these bounds; the scheduled refresh workflow
// (.github/workflows/devcontainer-refresh.yml) regenerates the bundle well inside them.
const (
	// DefaultFreshnessMaxCommits is the commit-count bound when max_commits is unset: about
	// three weeks of this repository's landing rate in September 2026, 166 commits a week.
	DefaultFreshnessMaxCommits = 500
	// FreshnessMaxCommitsCeiling is the largest max_commits a repository may declare.
	FreshnessMaxCommitsCeiling = 100000
	// DefaultFreshnessMaxAgeDays is the age bound when max_age_days is unset: three runs of
	// the weekly refresh workflow.
	DefaultFreshnessMaxAgeDays = 21
	// FreshnessMaxAgeDaysCeiling is the largest max_age_days a repository may declare.
	FreshnessMaxAgeDaysCeiling = 3650
)

// DevContainerPolicy is the manifest's devcontainer section: settings `praetorctl devcontainer`
// reads beside the profiles and facets the configuration is synthesized from. It is
// repository-only, like Documentation, and stays out of ResolvedPolicy.
type DevContainerPolicy struct {
	Freshness *DevContainerFreshness `yaml:"freshness,omitempty"`
}

// DevContainerFreshness bounds how far the committed bundle may fall behind the working tree:
// MaxCommits commits since the bundle last changed, and MaxAgeDays days since that commit. A
// nil field keeps its default.
type DevContainerFreshness struct {
	MaxCommits *int `yaml:"max_commits,omitempty"`
	MaxAgeDays *int `yaml:"max_age_days,omitempty"`
}

// UnmarshalYAML decodes the block strictly: known keys once each, written as integers. It is
// the rule the register block applies to its integer fields (registerIntFields).
func (f *DevContainerFreshness) UnmarshalYAML(node *yaml.Node) error {
	var maxCommits, maxAgeDays int
	present, err := registerIntFields(node, "devcontainer.freshness", map[string]*int{
		"max_commits": &maxCommits, "max_age_days": &maxAgeDays,
	})
	if err != nil {
		return err
	}
	var freshness DevContainerFreshness
	if present["max_commits"] {
		freshness.MaxCommits = &maxCommits
	}
	if present["max_age_days"] {
		freshness.MaxAgeDays = &maxAgeDays
	}
	*f = freshness
	return nil
}

// FreshnessBounds returns the declared commit and age bounds, each defaulted when unset. A nil
// policy, the manifest without a devcontainer section, returns both defaults.
func (p *DevContainerPolicy) FreshnessBounds() (maxCommits, maxAgeDays int) {
	maxCommits, maxAgeDays = DefaultFreshnessMaxCommits, DefaultFreshnessMaxAgeDays
	if p == nil || p.Freshness == nil {
		return maxCommits, maxAgeDays
	}
	if p.Freshness.MaxCommits != nil {
		maxCommits = *p.Freshness.MaxCommits
	}
	if p.Freshness.MaxAgeDays != nil {
		maxAgeDays = *p.Freshness.MaxAgeDays
	}
	return maxCommits, maxAgeDays
}

func validateManifestDevContainer(m *Manifest) error {
	if m == nil || m.DevContainer == nil || m.DevContainer.Freshness == nil {
		return nil
	}
	freshness := m.DevContainer.Freshness
	if err := validateFreshnessBound("max_commits", freshness.MaxCommits, FreshnessMaxCommitsCeiling); err != nil {
		return err
	}
	return validateFreshnessBound("max_age_days", freshness.MaxAgeDays, FreshnessMaxAgeDaysCeiling)
}

// validateFreshnessBound admits an unset bound or one from 1 to ceiling. Zero is refused rather
// than read as "no bound": a bound that can never pass is a configuration error, and switching
// the check off is a Makefile change, not a manifest value.
func validateFreshnessBound(key string, value *int, ceiling int) error {
	if value == nil || (*value >= 1 && *value <= ceiling) {
		return nil
	}
	return fmt.Errorf("devcontainer.freshness.%s must be an integer from 1 to %d; got %d", key, ceiling, *value)
}
