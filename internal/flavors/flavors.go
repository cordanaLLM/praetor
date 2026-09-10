package flavors

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Flavor defines a release track and its tag convention.
type Flavor struct {
	Description     string `yaml:"description"`
	SourceRef       string `yaml:"source_ref"`
	TagPattern      string `yaml:"tag_pattern"`
	UpdateFrequency string `yaml:"update_frequency"`
	Stability       string `yaml:"stability"`
}

// Config represents .config/flavors.yaml.
type Config struct {
	Version int               `yaml:"version"`
	Flavors map[string]Flavor `yaml:"flavors"`
}

// TagTransition represents a proposed or applied moving tag update.
type TagTransition struct {
	FlavorName string
	CurrentRef string
	TargetRef  string
	Action     string // "update", "noop", "create"
}

// LoadConfig reads and parses .config/flavors.yaml.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read flavors config at %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse flavors config at %s: %w", path, err)
	}

	return &cfg, nil
}

// PlanTransitions computes moving tag updates for all declared flavors.
func PlanTransitions(cfg *Config, currentTags map[string]string, latestCommit string, latestVersion string) []TagTransition {
	var transitions []TagTransition

	for name := range cfg.Flavors {
		target := latestVersion
		if name == "bleeding" {
			target = fmt.Sprintf("v%s-bleeding.%s", latestVersion, latestCommit)
		} else if name == "edge" {
			target = fmt.Sprintf("v%s-rc.1", latestVersion)
		}

		current, exists := currentTags[name]
		action := "update"
		if !exists {
			action = "create"
		} else if current == target {
			action = "noop"
		}

		transitions = append(transitions, TagTransition{
			FlavorName: name,
			CurrentRef: current,
			TargetRef:  target,
			Action:     action,
		})
	}

	return transitions
}
