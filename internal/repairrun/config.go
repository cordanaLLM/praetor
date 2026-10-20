// Package repairrun produces bounded local repair candidates in isolated workspaces.
package repairrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/cordanaLLM/praetor/internal/router"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/dogfood"
	"github.com/cordanaLLM/praetor/internal/util"
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
	config            Config
	digest            string
	registerAuthority config.RegisterAuthority
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
	if err := confineConfigTargets(boundary, cfg); err != nil {
		return nil, err
	}
	authority, err := loadPinnedRegisterAuthority(ctx, cfg)
	if err != nil {
		return nil, err
	}
	cfg.RepairPolicy, err = dogfood.VerifyRepairPolicyAuthority(cfg.RepairPolicy, authority)
	if err != nil {
		return nil, err
	}
	if err := dogfood.ValidateRepairPolicy(ctx, cfg.RepairPolicy); err != nil {
		return nil, err
	}
	return &configuration{config: cfg, digest: bytesSHA(data), registerAuthority: authority}, nil
}

// confineConfigTargets checks that all configured paths fall within boundary.
func confineConfigTargets(boundary string, cfg Config) error {
	for _, target := range []string{cfg.SourceRoot, cfg.StateDir, cfg.RepairPolicy.RoutingConfig, cfg.RepairPolicy.UsagePath, cfg.Provider.TokenCommand} {
		if target != "" {
			if err := confine(boundary, target); err != nil {
				return err
			}
		}
	}
	return nil
}

func loadPinnedRegisterAuthority(ctx context.Context, cfg Config) (config.RegisterAuthority, error) {
	kind, err := runGit(ctx, cfg.SourceRoot, 128, "cat-file", "-t", cfg.SourceSHA)
	if err != nil || string(kind) != "commit\n" {
		return config.RegisterAuthority{}, errors.New("source_sha must identify an immutable commit")
	}
	data, found, err := pinnedBlob(ctx, cfg, ".standards.yaml", "pinned register manifest")
	if err != nil || !found {
		return config.AbsentRegisterAuthority(), err
	}
	authority, err := config.ParseRegisterAuthority(data, cfg.SourceSHA+":.standards.yaml")
	if err != nil || !authority.HasDeclaredTasks() {
		return authority, err
	}
	labels, err := pinnedTaskLabels(ctx, cfg)
	if err != nil {
		return config.RegisterAuthority{}, err
	}
	if err := authority.ValidateTaskLabels(labels); err != nil {
		return config.RegisterAuthority{}, fmt.Errorf("pinned register manifest: %w", err)
	}
	return authority, nil
}

// pinnedTaskLabels returns the target_tasks vocabulary of the source repository's own router at
// the pinned commit, or the router's built-in labels when it has none. Register rows are judged
// by that router, never by the repair router, which may declare only the repair task (#765).
func pinnedTaskLabels(ctx context.Context, cfg Config) ([]string, error) {
	data, found, err := pinnedBlob(ctx, cfg, router.DefaultConfigPath, "pinned routing config")
	if err != nil {
		return nil, err
	}
	if !found {
		return router.DefaultTaskLabels(), nil
	}
	routing, err := router.ParseRoutingConfig(data, cfg.SourceSHA+":"+router.DefaultConfigPath)
	if err != nil {
		return nil, err
	}
	return router.DeclaredTaskLabels(routing), nil
}

// pinnedBlob reads the regular file at path in the pinned source commit; found is false when the
// commit has no entry there.
func pinnedBlob(ctx context.Context, cfg Config, path, what string) (data []byte, found bool, err error) {
	entry, err := runGit(ctx, cfg.SourceRoot, 512, "ls-tree", "-z", cfg.SourceSHA, "--", path)
	if err != nil {
		return nil, false, errors.New(what + " lookup failed")
	}
	if len(entry) == 0 {
		return nil, false, nil
	}
	record, err := util.ParseGitTreeEntry(strings.TrimSuffix(string(entry), "\x00"), false)
	if err != nil || record.Path != path || !validBlob(record) {
		return nil, false, errors.New(what + " is not a regular file")
	}
	data, err = runGit(ctx, cfg.SourceRoot, contextopt.MaxSourceBytes, "cat-file", "blob", record.Object)
	if err != nil {
		return nil, false, errors.New(what + " read failed")
	}
	return data, true, nil
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

// validateAllowedFiles checks that allowed files are unique clean non-test Go source paths.
func validateAllowedFiles(files []string, seen map[string]bool) error {
	for _, path := range files {
		if !allowedSourcePath(path) || seen[path] {
			return errors.New("allowed files must be unique clean non-test Go source paths")
		}
		seen[path] = true
	}
	return nil
}

// validateTestPackages checks that test packages are unique explicit internal or cmd packages.
func validateTestPackages(pkgs []string, seen map[string]bool) error {
	for _, name := range pkgs {
		if !packageName.MatchString(name) || seen[name] {
			return errors.New("test packages must be unique explicit internal or cmd packages")
		}
		seen[name] = true
	}
	return nil
}

func validateConfigLists(c Config) error {
	if len(c.AllowedFiles) < 1 || len(c.AllowedFiles) > 8 || len(c.TestPackages) < 1 || len(c.TestPackages) > 4 {
		return errors.New("repair requires 1..8 files and 1..4 packages")
	}
	seen := map[string]bool{}
	if err := validateAllowedFiles(c.AllowedFiles, seen); err != nil {
		return err
	}
	return validateTestPackages(c.TestPackages, seen)
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
