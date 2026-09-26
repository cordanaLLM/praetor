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
//
// The Darwin labels name macOS 26 explicitly rather than macos-latest, because
// a floating label silently retargets a repository's whole Darwin tier the day
// GitHub promotes the next image.
//
// Evidence, actions/runner-images README (main, read 2026-09-19), the same table
// GitHub's own runner documentation mirrors:
//
//	macOS 26        x64    macos-latest-large, macos-26-intel, macos-26-large
//	macOS 26 Arm64  arm64  macos-latest, macos-26, macos-26-xlarge
//	macOS 14        both   carries a deprecated badge
//	macOS 13        -      absent from the table entirely
//
// Corroborated by actionlint v1.7.12, whose built-in label table accepts macos-26
// and macos-26-intel and rejects nothing about them. The pair was not in the
// availability evidence this change was handed, which stopped at macos-15-intel;
// it is cited here so the next reader does not have to re-derive it.
//
// ADR-0006 still records the previous pair and is Accepted, so it is not edited
// here; a superseding ADR is the vehicle for restating the tier.
func DefaultRunnerPolicy() RunnerPolicy {
	return RunnerPolicy{
		Default: "arc-runner-set-linux-amd64",
		Routing: map[string]RunnerSpec{
			"darwin/arm64": {Type: "github-hosted", RunsOn: []string{"macos-26"}, Ephemeral: true},
			"darwin/amd64": {Type: "github-hosted", RunsOn: []string{"macos-26-intel"}, Ephemeral: true},
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
