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

func cargoMutantsExclusionCount(root, path string) (int, error) {
	data, err := util.ReadConfinedLimited(root, path, maxMutantsConfigBytes)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) > maxMutantsConfigLines {
		return 0, fmt.Errorf("has %d lines, maximum is %d", len(lines), maxMutantsConfigLines)
	}
	return countCargoMutantsKeys(lines)
}

func countCargoMutantsKeys(lines []string) (int, error) {
	section := ""
	total := 0
	seen := make(map[string]bool, 2)
	for index := 0; index < len(lines) && index < maxMutantsConfigLines; index++ {
		key, value, found, err := cargoMutantsLine(lines[index], &section, seen)
		if err != nil {
			return 0, err
		}
		if !found {
			continue
		}
		count, err := cargoMutantsArray(lines, &index, key, value)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func cargoMutantsLine(line string, section *string, seen map[string]bool) (string, string, bool, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false, nil
	}
	if strings.HasPrefix(line, "[") {
		*section = util.TOMLTableName(line)
		return "", "", false, nil
	}
	key, value, ok := mutationTOMLAssignment(line)
	if !ok || *section != "" || (key != "exclude_re" && key != "exclude_globs") {
		return "", "", false, nil
	}
	if seen[key] {
		return "", "", false, fmt.Errorf("TOML config repeats %s", key)
	}
	seen[key] = true
	return key, value, true, nil
}

func cargoMutantsArray(lines []string, index *int, key, value string) (int, error) {
	items, closed, valid := util.TOMLStringArray(value)
	for !closed && valid && *index+1 < len(lines) && *index+1 < maxMutantsConfigLines {
		*index++
		more, end, ok := util.TOMLStringArray("[" + strings.TrimSpace(lines[*index]))
		items = append(items, more...)
		closed, valid = end, ok
	}
	if !valid || !closed {
		return 0, fmt.Errorf("%s must be a closed array of strings", key)
	}
	return len(items), nil
}

func mutationTOMLAssignment(line string) (string, string, bool) {
	keys, rest, err := util.TOMLKeyPath(line)
	if err != nil || len(keys) != 1 {
		return "", "", false
	}
	assignment, found := strings.CutPrefix(rest, "=")
	if !found {
		return "", "", false
	}
	return keys[0], strings.TrimSpace(assignment), true
}
