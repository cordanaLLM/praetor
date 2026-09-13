package builder

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"gopkg.in/yaml.v3"
)

const maxBuildTargets = 128

// BuildStatus describes whether a selected target has an executable backend.
type BuildStatus string

const (
	// BuildUnavailable identifies a known runtime whose backend is not implemented.
	BuildUnavailable BuildStatus = "unavailable"
	// BuildUnsupported identifies a runtime not recognized by the builder.
	BuildUnsupported BuildStatus = "unsupported"
)

var (
	// ErrBackendUnavailable means no compilation or optimization was executed.
	ErrBackendUnavailable = errors.New("build backend is not implemented; no compilation or optimization was executed")
	// ErrUnsupportedRuntime means the requested runtime is not recognized.
	ErrUnsupportedRuntime = errors.New("unsupported target runtime; no compilation or optimization was executed")
)

// TargetConfig defines build directives for an individual language or framework target.
type TargetConfig struct {
	Runtime      string            `json:"runtime" yaml:"runtime"` // "go", "svelte", "python", "rust", "native-gpu"
	Entrypoint   string            `json:"entrypoint" yaml:"entrypoint"`
	Options      map[string]string `json:"options,omitempty" yaml:"options,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

// BuildConfig represents the full .framework-build.yaml manifest.
type BuildConfig struct {
	Version   int                     `json:"version" yaml:"version"`
	Project   string                  `json:"project" yaml:"project"`
	OutputDir string                  `json:"output_dir" yaml:"output_dir"`
	Optimize  bool                    `json:"optimize" yaml:"optimize"`
	Targets   map[string]TargetConfig `json:"targets" yaml:"targets"`
}

// BuildResult captures the outcome of a universal build execution.
type BuildResult struct {
	Project    string        `json:"project"`
	Target     string        `json:"target"`
	Runtime    string        `json:"runtime"`
	Artifacts  []string      `json:"artifacts"`
	Duration   time.Duration `json:"duration"`
	Optimized  bool          `json:"optimized"`
	Success    bool          `json:"success"`
	OutputLogs string        `json:"output_logs"`
	Status     BuildStatus   `json:"status"`
	Reason     string        `json:"reason"`
}

// LoadBuildConfig parses the .framework-build.yaml file.
func LoadBuildConfig(path string) (*BuildConfig, error) {
	return LoadBuildConfigContext(context.Background(), path)
}

// LoadBuildConfigContext reads a bounded regular configuration under the caller deadline.
func LoadBuildConfigContext(ctx context.Context, path string) (*BuildConfig, error) {
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to read build config %s: %w", path, err)
	}
	var cfg BuildConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal build config: %w", err)
	}
	if cfg.Targets == nil {
		cfg.Targets = make(map[string]TargetConfig)
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "dist"
	}
	return &cfg, nil
}

// UniversalBuilder checks polyglot build requests. Execution backends are unavailable.
type UniversalBuilder struct{}

// NewUniversalBuilder initializes a builder without filesystem or process effects.
func NewUniversalBuilder() *UniversalBuilder {
	return &UniversalBuilder{}
}

// Build rejects unimplemented or unsupported backends without modifying inputs or
// the filesystem. Results describe rejected targets, never planned artifacts.
// At most 128 selected targets are inspected in lexical order; callers must check
// the returned error even when results are present. Duration measures inspection.
func (b *UniversalBuilder) Build(ctx context.Context, cfg *BuildConfig, targetName string) ([]BuildResult, error) {
	if ctx == nil {
		return nil, errors.New("context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, errors.New("build config cannot be nil")
	}

	targetsToBuild, err := resolveTargets(cfg, targetName)
	if err != nil {
		return nil, err
	}
	if len(targetsToBuild) > maxBuildTargets {
		return nil, fmt.Errorf("build selection exceeds %d targets", maxBuildTargets)
	}
	names := make([]string, 0, len(targetsToBuild))
	for name := range targetsToBuild {
		names = append(names, name)
	}
	sort.Strings(names)

	results := make([]BuildResult, 0, len(targetsToBuild))
	var failures []error
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		start := time.Now()
		res, bErr := unavailableTarget(cfg.Project, name, targetsToBuild[name].Runtime)
		failures = append(failures, fmt.Errorf("build target %s failed: %w", name, bErr))
		res.Duration = time.Since(start)
		results = append(results, res)
	}

	return results, errors.Join(failures...)
}

func resolveTargets(cfg *BuildConfig, targetName string) (map[string]TargetConfig, error) {
	if targetName == "" || targetName == "all" {
		if len(cfg.Targets) == 0 {
			return nil, errors.New("no targets defined in build configuration")
		}
		return cfg.Targets, nil
	}

	t, exists := cfg.Targets[targetName]
	if !exists {
		return nil, fmt.Errorf("target %s not found in configuration", targetName)
	}
	return map[string]TargetConfig{targetName: t}, nil
}

func unavailableTarget(project, name, runtime string) (BuildResult, error) {
	status, err := BuildUnsupported, ErrUnsupportedRuntime
	switch runtime {
	case "go", "svelte", "typescript", "python", "rust", "native-gpu", "native", "c", "cpp":
		status, err = BuildUnavailable, ErrBackendUnavailable
	}
	return BuildResult{
		Project: project, Target: name, Runtime: runtime, Status: status, Reason: err.Error(),
	}, fmt.Errorf("runtime %q: %w", runtime, err)
}
