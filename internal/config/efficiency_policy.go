// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package config

import (
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/util"
)

// EfficiencyPolicy configures the efficiency ledger sources and model classifications (#870).
type EfficiencyPolicy struct {
	Sources        EfficiencySources `yaml:"sources,omitempty"`
	FrontierModels []string          `yaml:"frontier_models,omitempty"`
	// FrontierClasses and LightClasses name router classes (model groups), matched before models.
	FrontierClasses []string `yaml:"frontier_classes,omitempty"`
	LightClasses    []string `yaml:"light_classes,omitempty"`
	LocalModels     []string `yaml:"local_models,omitempty"`
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

// DefaultFrontierModels returns the default list of model family prefixes classified as frontier.
// Each entry is a family prefix of a current provider model ID; cheap tiers (see
// CheapTierMarkers) never match, so "gpt-5" covers gpt-5 but not gpt-5-mini or gpt-5-nano, and
// "gemini-3" covers the pro models but not the flash and flash-lite ones.
func DefaultFrontierModels() []string {
	return []string{
		"claude-opus-",
		"claude-sonnet-",
		"claude-fable-",
		"gpt-5",
		"gpt-6",
		"gemini-3",
		"gemini-2.5-pro",
	}
}

// DefaultFrontierClasses returns the router classes (model groups) classified as frontier. The
// class is matched before the model family list.
func DefaultFrontierClasses() []string {
	return []string{"reasoning", "coding"}
}

// DefaultLightClasses returns the router classes classified as cheap: never frontier, whatever
// model serves them.
func DefaultLightClasses() []string {
	return []string{"light"}
}

// CheapTierMarkers returns the name tokens that mark a cheap tier. A model whose ID contains
// one of them as a hyphen- or dot-separated token is never frontier.
func CheapTierMarkers() []string {
	return []string{"mini", "nano", "flash", "lite", "haiku"}
}

// DefaultLocalModels returns the documented default list of models/classes classified as local or cluster.
func DefaultLocalModels() []string {
	return []string{
		"local",
		"cluster",
		"ollama",
		"vllm",
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

// EffectiveFrontierClasses returns configured FrontierClasses or DefaultFrontierClasses if empty.
func (p *EfficiencyPolicy) EffectiveFrontierClasses() []string {
	if p == nil || len(p.FrontierClasses) == 0 {
		return DefaultFrontierClasses()
	}
	return p.FrontierClasses
}

// EffectiveLightClasses returns configured LightClasses or DefaultLightClasses if empty.
func (p *EfficiencyPolicy) EffectiveLightClasses() []string {
	if p == nil || len(p.LightClasses) == 0 {
		return DefaultLightClasses()
	}
	return p.LightClasses
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
	if sources.Transcripts.Dir != "" && sources.Transcripts.Path != "" && sources.Transcripts.Dir != sources.Transcripts.Path {
		return fmt.Errorf("efficiency.sources.transcripts sets both dir (%q) and path (%q); set one", sources.Transcripts.Dir, sources.Transcripts.Path)
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
	if len(p.FrontierClasses) > 100 || len(p.LightClasses) > 100 {
		return fmt.Errorf("efficiency.frontier_classes and efficiency.light_classes are limited to 100 entries each")
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
