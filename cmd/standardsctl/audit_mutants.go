// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	cargoMutantsConfig     = ".cargo/mutants.toml"
	maxMutantsConfigBytes  = 1 << 20
	maxMutantsConfigLines  = 10000
	maxMutantsTrackedFiles = 1 << 20
)

// auditMutationExclusions checks each tracked cargo-mutants config with exclusions against its
// one live, categorized exception.
func auditMutationExclusions(ctx context.Context, manifest *config.Manifest, root string, today time.Time) error {
	listed, err := util.RunGitProbe(ctx, root, 16<<20, "ls-files", "-z")
	if err != nil {
		return fmt.Errorf("list tracked mutation-tool configurations: %w", err)
	}
	paths := strings.Split(strings.TrimSuffix(string(listed.Stdout), "\x00"), "\x00")
	if len(paths) > maxMutantsTrackedFiles {
		return fmt.Errorf("list tracked mutation-tool configurations: repository exceeds %d tracked files", maxMutantsTrackedFiles)
	}
	for index := 0; index < len(paths) && index < maxMutantsTrackedFiles; index++ {
		path := paths[index]
		tool, supported := mutationConfigTool(path)
		if path == "" || !supported {
			continue
		}
		count, err := mutationConfigExclusionCount(root, path, tool)
		if err != nil {
			return fmt.Errorf("[FAIL] Exceptions: inspect %s: %w", path, err)
		}
		if count == 0 {
			continue
		}
		if err := requireMutantsException(manifest, path, count, today); err != nil {
			return err
		}
	}
	return nil
}

func requireMutantsException(manifest *config.Manifest, path string, count int, today time.Time) error {
	if manifest != nil {
		for index := 0; index < len(manifest.Exceptions) && index < config.MaxExceptions; index++ {
			entry := manifest.Exceptions[index]
			if entry.Rule == config.ExceptionRuleMutants && entry.Path == path && entry.Category != "" && !entry.Expired(today) {
				return nil
			}
		}
	}
	return fmt.Errorf("[FAIL] Exceptions: %s has %d mutation exclusion(s) and no live categorized mutants exception; add rule mutants, path %s, category, entered, reason and expires to .standards.yaml", path, count, path)
}

// cargoMutantsFile holds the exclusion keys cargo-mutants reads from .cargo/mutants.toml. The
// file is parsed by the repository's TOML reader (util.DecodeTOML, go-toml/v2), which refuses a
// repeated key and a value of the wrong type; every other key is ignored.
type cargoMutantsFile struct {
	ExcludeRe    []string `toml:"exclude_re"`
	ExcludeGlobs []string `toml:"exclude_globs"`
}

func cargoMutantsExclusionCount(root, path string) (int, error) {
	data, err := util.ReadConfinedLimited(root, path, maxMutantsConfigBytes)
	if err != nil {
		return 0, err
	}
	return countCargoMutants(path, data)
}

// countCargoMutants counts the top-level exclude_re and exclude_globs entries of one
// .cargo/mutants.toml text; a key inside another table is not cargo-mutants' own.
func countCargoMutants(path string, data []byte) (int, error) {
	var cfg cargoMutantsFile
	if err := util.DecodeTOML(path, data, &cfg); err != nil {
		return 0, err
	}
	return len(cfg.ExcludeRe) + len(cfg.ExcludeGlobs), nil
}
