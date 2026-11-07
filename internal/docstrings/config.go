// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docstrings

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

const (
	// DefaultThreshold is CodeRabbit's default docstring coverage percentage floor.
	DefaultThreshold = 80
	// DefaultMode is the default pre-merge check mode when .coderabbit.yaml is absent.
	DefaultMode    = "error"
	maxConfigBytes = 1 << 20
)

// Config holds the resolved docstring pre-merge check configuration.
type Config struct {
	Mode      string `json:"mode"`
	Threshold int    `json:"threshold"`
}

type codeRabbitYAML struct {
	Reviews struct {
		PreMergeChecks struct {
			Docstrings struct {
				Mode      string `yaml:"mode"`
				Threshold *int   `yaml:"threshold"`
			} `yaml:"docstrings"`
		} `yaml:"pre_merge_checks"`
	} `yaml:"reviews"`
}

// LoadConfig reads reviews.pre_merge_checks.docstrings from .coderabbit.yaml in repoDir,
// returning defaults when the file does not exist and failing closed on read or parse errors.
func LoadConfig(repoDir string) (Config, error) {
	if repoDir == "" {
		repoDir = "."
	}
	cfg := Config{
		Mode:      DefaultMode,
		Threshold: DefaultThreshold,
	}
	path := filepath.Join(repoDir, ".coderabbit.yaml")
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Config{}, fmt.Errorf("refusing symlink configuration %s", path)
	}
	if !info.Mode().IsRegular() {
		return Config{}, fmt.Errorf("configuration %s is not a regular file", path)
	}
	if info.Size() > maxConfigBytes {
		return Config{}, fmt.Errorf("configuration %s exceeds %d bytes", path, maxConfigBytes)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is joined to repoDir and verified as a regular file above
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	return parseConfigBytes(data, path)
}

// parseConfigBytes decodes and validates .coderabbit.yaml bytes.
func parseConfigBytes(data []byte, source string) (Config, error) {
	var raw codeRabbitYAML
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", source, err)
	}
	doc := raw.Reviews.PreMergeChecks.Docstrings
	mode := doc.Mode
	if mode == "" {
		mode = DefaultMode
	}
	if mode != "off" && mode != "warning" && mode != "error" {
		return Config{}, fmt.Errorf("%s: invalid reviews.pre_merge_checks.docstrings.mode %q (want off, warning, or error)", source, mode)
	}
	threshold := DefaultThreshold
	if doc.Threshold != nil {
		threshold = *doc.Threshold
		if threshold < 0 || threshold > 100 {
			return Config{}, fmt.Errorf("%s: invalid reviews.pre_merge_checks.docstrings.threshold %d (want 0..100)", source, threshold)
		}
	}
	return Config{
		Mode:      mode,
		Threshold: threshold,
	}, nil
}
