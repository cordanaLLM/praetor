package config

import (
	"context"
	"errors"
	"fmt"
	"github.com/cordanaLLM/praetor/internal/contextopt"
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
	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDuration)
	defer cancel()
	return LoadCascadingRunnerConfigContext(ctx, rootDir, orgName)
}

func LoadCascadingRunnerConfigContext(ctx context.Context, rootDir, orgName string) (*RunnerPolicy, error) {
	if orgName != "" && (!filepath.IsLocal(orgName) || filepath.Base(orgName) != orgName || orgName == ".") {
		return nil, errors.New("organization must be one local path component")
	}
	merged := DefaultRunnerPolicy()

	fleetPath := filepath.Join(rootDir, ".config", "fleet.yaml")
	if err := mergeRunnerConfig(ctx, fleetPath, &merged); err != nil {
		return nil, fmt.Errorf("failed merging fleet config: %w", err)
	}

	if orgName != "" {
		orgPath := filepath.Join(rootDir, ".config", "orgs", orgName+".yaml")
		if err := mergeRunnerConfig(ctx, orgPath, &merged); err != nil {
			return nil, fmt.Errorf("failed merging org config: %w", err)
		}
	}

	repoPath := filepath.Join(rootDir, ".standards.yaml")
	if err := mergeRunnerConfig(ctx, repoPath, &merged); err != nil {
		return nil, fmt.Errorf("failed merging repo config: %w", err)
	}

	return &merged, nil
}

func mergeRunnerConfig(ctx context.Context, path string, target *RunnerPolicy) error {
	data, err := contextopt.ReadSnapshot(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var config struct {
		Runners RunnerPolicy `yaml:"runners"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return err
	}
	mergePolicies(target, &config.Runners)
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
