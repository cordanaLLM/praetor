// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package config

import (
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/util"
)

// EfficiencyPolicy configures the efficiency ledger sources and model classifications (ADR-0030, #870).
type EfficiencyPolicy struct {
	Sources        EfficiencySources `yaml:"sources,omitempty"`
	FrontierModels []string          `yaml:"frontier_models,omitempty"`
	LocalModels    []string          `yaml:"local_models,omitempty"`
}

// EfficiencySources defines the paths and settings for optional ledger sources.
type EfficiencySources struct {
	Forge       ForgeSource       `yaml:"forge,omitempty"`
	Transcripts TranscriptsSource `yaml:"transcripts,omitempty"`
	SpendLog    SpendLogSource    `yaml:"spend_log,omitempty"`
}

// ForgeSource configures the forge records source.
type ForgeSource struct {
	Path string `yaml:"path,omitempty"`
}

// TranscriptsSource configures the agent session transcripts source directory.
type TranscriptsSource struct {
	Dir  string `yaml:"dir,omitempty"`
	Path string `yaml:"path,omitempty"`
}

// Directory returns the configured transcripts directory.
func (t TranscriptsSource) Directory() string {
	if t.Dir != "" {
		return t.Dir
	}
	return t.Path
}

// SpendLogSource configures the gateway spend-log export file source (JSON lines or CSV).
type SpendLogSource struct {
	Path string `yaml:"path,omitempty"`
}

// DefaultFrontierModels returns the documented default list of models/families/tiers classified as frontier.
func DefaultFrontierModels() []string {
	return []string{
		"claude-3-7-sonnet",
		"claude-3-5-sonnet",
		"claude-3-opus",
		"gpt-4",
		"gpt-4o",
		"gpt-4.5",
		"o1",
		"o3",
		"gemini-1.5-pro",
		"gemini-2.0-flash",
		"gemini-2.5-pro",
		"heavy-frontier",
		"grok-3",
		"deepseek-reasoner",
	}
}

// DefaultLocalModels returns the documented default list of models/classes classified as local or cluster.
func DefaultLocalModels() []string {
	return []string{
		"local",
		"cluster",
		"ollama",
		"vllm",
		"nano",
		"gpu-local",
	}
}

// EffectiveFrontierModels returns configured FrontierModels or DefaultFrontierModels if empty.
func (p *EfficiencyPolicy) EffectiveFrontierModels() []string {
	if p == nil || len(p.FrontierModels) == 0 {
		return DefaultFrontierModels()
	}
	return p.FrontierModels
}

// EffectiveLocalModels returns configured LocalModels or DefaultLocalModels if empty.
func (p *EfficiencyPolicy) EffectiveLocalModels() []string {
	if p == nil || len(p.LocalModels) == 0 {
		return DefaultLocalModels()
	}
	return p.LocalModels
}

// validateManifestEfficiency holds declared efficiency source paths to clean local repository path shape.
func validateEfficiencySourcePaths(sources EfficiencySources) error {
	if sources.Forge.Path != "" && !ValidRepositoryPath(sources.Forge.Path) {
		return fmt.Errorf("efficiency.sources.forge.path %q must be a clean local forward-slash path of at most %d bytes",
			sources.Forge.Path, maxRepositoryPath)
	}
	tDir := sources.Transcripts.Directory()
	if tDir != "" && !ValidRepositoryPath(tDir) {
		return fmt.Errorf("efficiency.sources.transcripts.dir %q must be a clean local forward-slash path of at most %d bytes",
			tDir, maxRepositoryPath)
	}
	if sources.SpendLog.Path != "" && !ValidRepositoryPath(sources.SpendLog.Path) {
		return fmt.Errorf("efficiency.sources.spend_log.path %q must be a clean local forward-slash path of at most %d bytes",
			sources.SpendLog.Path, maxRepositoryPath)
	}
	return nil
}

func validateManifestEfficiency(m *Manifest) error {
	if m == nil || m.Efficiency == nil {
		return nil
	}
	p := m.Efficiency
	if err := validateEfficiencySourcePaths(p.Sources); err != nil {
		return err
	}
	if len(p.FrontierModels) > 100 {
		return fmt.Errorf("efficiency.frontier_models exceeds 100 entries limit (got %d)", len(p.FrontierModels))
	}
	if len(p.LocalModels) > 100 {
		return fmt.Errorf("efficiency.local_models exceeds 100 entries limit (got %d)", len(p.LocalModels))
	}
	return nil
}

// RepositoryEfficiencyPolicy returns the efficiency policy declared by the manifest of the repository
// at root, or nil if none is declared.
func RepositoryEfficiencyPolicy(root string) (*EfficiencyPolicy, error) {
	if root == "" {
		root = "."
	}
	manifestPath := filepath.Join(root, ManifestFileName)
	if !util.FileExists(manifestPath) {
		return nil, nil
	}
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	return manifest.Efficiency, nil
}
