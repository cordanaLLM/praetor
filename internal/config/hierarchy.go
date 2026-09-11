package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// RunnerSpec defines the runner environment and execution constraints for a platform target.
type RunnerSpec struct {
	Type      string   `yaml:"type" json:"type"`
	RunsOn    []string `yaml:"runs_on" json:"runs_on"`
	Ephemeral bool     `yaml:"ephemeral" json:"ephemeral"`
}

// RunnerPolicy configures the default runner and platform routing table.
type RunnerPolicy struct {
	Default string                `yaml:"default" json:"default"`
	Routing map[string]RunnerSpec `yaml:"routing" json:"routing"`
}

// FleetConfig represents global governance configurations across all organizations.
type FleetConfig struct {
	Runners RunnerPolicy `yaml:"runners"`
}

// OrgConfig represents organization-level overrides.
type OrgConfig struct {
	Runners RunnerPolicy `yaml:"runners"`
}

// DefaultRunnerPolicy returns safe defaults for GitHub and ARC runners.
func DefaultRunnerPolicy() RunnerPolicy {
	return RunnerPolicy{
		Default: "arc-runner-set-linux-amd64",
		Routing: map[string]RunnerSpec{
			"darwin/arm64": {Type: "github-hosted", RunsOn: []string{"macos-14"}, Ephemeral: true},
			"darwin/amd64": {Type: "github-hosted", RunsOn: []string{"macos-13"}, Ephemeral: true},
			"linux/amd64":  {Type: "self-hosted-arc", RunsOn: []string{"arc-runner-set-linux-amd64"}, Ephemeral: true},
			"linux/arm64":  {Type: "self-hosted-arc", RunsOn: []string{"arc-runner-set-linux-arm64"}, Ephemeral: true},
			"linux/gpu":    {Type: "self-hosted-arc", RunsOn: []string{"arc-runner-set-gpu-xpu"}, Ephemeral: true},
		},
	}
}

// LoadCascadingRunnerConfig merges fleet, org, and repository-level runner configurations.
func LoadCascadingRunnerConfig(rootDir, orgName string) (*RunnerPolicy, error) {
	merged := DefaultRunnerPolicy()

	fleetPath := filepath.Join(rootDir, ".config", "fleet.yaml")
	if err := mergeFleetConfig(fleetPath, &merged); err != nil {
		return nil, fmt.Errorf("failed merging fleet config: %w", err)
	}

	if orgName != "" {
		orgPath := filepath.Join(rootDir, ".config", "orgs", orgName+".yaml")
		if err := mergeOrgConfig(orgPath, &merged); err != nil {
			return nil, fmt.Errorf("failed merging org config: %w", err)
		}
	}

	repoPath := filepath.Join(rootDir, ".standards.yaml")
	if err := mergeRepoConfig(repoPath, &merged); err != nil {
		return nil, fmt.Errorf("failed merging repo config: %w", err)
	}

	return &merged, nil
}

func mergeFleetConfig(path string, target *RunnerPolicy) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var fc FleetConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return err
	}
	mergePolicies(target, &fc.Runners)
	return nil
}

func mergeOrgConfig(path string, target *RunnerPolicy) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var oc OrgConfig
	if err := yaml.Unmarshal(data, &oc); err != nil {
		return err
	}
	mergePolicies(target, &oc.Runners)
	return nil
}

func mergeRepoConfig(path string, target *RunnerPolicy) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var raw struct {
		Runners RunnerPolicy `yaml:"runners"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	mergePolicies(target, &raw.Runners)
	return nil
}

func mergePolicies(dst *RunnerPolicy, src *RunnerPolicy) {
	if src == nil {
		return
	}
	if src.Default != "" {
		dst.Default = src.Default
	}
	if dst.Routing == nil {
		dst.Routing = make(map[string]RunnerSpec)
	}
	for k, v := range src.Routing {
		dst.Routing[k] = v
	}
}
