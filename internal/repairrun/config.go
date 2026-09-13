// Package repairrun produces bounded local repair candidates in isolated workspaces.
package repairrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/dogfood"
)

const maxConfigBytes = 64 << 10

var sourceSHA = regexp.MustCompile(`^[a-f0-9]{40}$`)
var packageName = regexp.MustCompile(`^\./(internal|cmd)/[a-z][a-z0-9_]*$`)
var sourceName = regexp.MustCompile(`^(internal|cmd)/[a-zA-Z0-9_./-]+\.go$`)

// Config binds a private policy to one immutable source and one provider.
type Config struct {
	Version        int                  `json:"version"`
	SourceRoot     string               `json:"source_root"`
	SourceSHA      string               `json:"source_sha"`
	StateDir       string               `json:"state_dir"`
	AllowedFiles   []string             `json:"allowed_files"`
	TestPackages   []string             `json:"test_packages"`
	TimeoutSeconds int                  `json:"timeout_seconds"`
	MaxPatchBytes  int                  `json:"max_patch_bytes"`
	RepairPolicy   dogfood.RepairPolicy `json:"repair_policy"`
	Provider       ProviderConfig       `json:"provider"`
}

type configuration struct {
	config Config
	digest string
}

func loadConfig(ctx context.Context, path, boundary string) (*configuration, error) {
	if err := confine(boundary, path); err != nil {
		return nil, err
	}
	data, err := readPrivate(ctx, path, maxConfigBytes)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := decodeConfigJSON(data, &cfg); err != nil {
		return nil, err
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	for _, target := range []string{cfg.SourceRoot, cfg.StateDir, cfg.RepairPolicy.RoutingConfig, cfg.RepairPolicy.UsagePath, cfg.Provider.TokenCommand} {
		if target != "" {
			if err := confine(boundary, target); err != nil {
				return nil, err
			}
		}
	}
	return &configuration{config: cfg, digest: bytesSHA(data)}, nil
}

func validateConfig(c Config) error {
	if c.Version != 1 || !sourceSHA.MatchString(c.SourceSHA) {
		return errors.New("repair execution requires version 1 and a full commit SHA")
	}
	if err := validateConfigPaths(c); err != nil {
		return err
	}
	if c.TimeoutSeconds < 30 || c.TimeoutSeconds > 300 || c.MaxPatchBytes < 1024 || c.MaxPatchBytes > 256<<10 {
		return errors.New("repair timeout must be 30..300 seconds and patch bound 1..256 KiB")
	}
	if err := validateConfigLists(c); err != nil {
		return err
	}
	return ValidateProviderConfig(c.Provider)
}

func validateConfigPaths(c Config) error {
	for _, path := range []string{c.SourceRoot, c.StateDir, c.RepairPolicy.RoutingConfig} {
		if !cleanAbsolute(path) {
			return errors.New("repair execution paths must be clean and absolute")
		}
	}
	if c.RepairPolicy.UsagePath != "" && !cleanAbsolute(c.RepairPolicy.UsagePath) {
		return errors.New("usage path must be clean and absolute")
	}
	return nil
}

func validateConfigLists(c Config) error {
	if len(c.AllowedFiles) < 1 || len(c.AllowedFiles) > 8 || len(c.TestPackages) < 1 || len(c.TestPackages) > 4 {
		return errors.New("repair requires 1..8 files and 1..4 packages")
	}
	seen := map[string]bool{}
	for _, path := range c.AllowedFiles {
		if !allowedSourcePath(path) || seen[path] {
			return errors.New("allowed files must be unique clean non-test Go source paths")
		}
		seen[path] = true
	}
	for _, name := range c.TestPackages {
		if !packageName.MatchString(name) || seen[name] {
			return errors.New("test packages must be unique explicit internal or cmd packages")
		}
		seen[name] = true
	}
	return nil
}

func allowedSourcePath(path string) bool {
	return sourceName.MatchString(path) && filepath.IsLocal(path) && filepath.ToSlash(filepath.Clean(path)) == path && !strings.HasSuffix(path, "_test.go")
}

func cleanAbsolute(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\x00\r\n")
}

func confine(root, path string) error {
	if !cleanAbsolute(path) {
		return errors.New("repair path must be clean and absolute")
	}
	if root == "" {
		return nil
	}
	if !cleanAbsolute(root) {
		return errors.New("repair boundary must be clean and absolute")
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return errors.New("repair path is outside the input root")
	}
	return nil
}

func bytesSHA(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
