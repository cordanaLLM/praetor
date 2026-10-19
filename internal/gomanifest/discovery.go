// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package gomanifest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// MaxDiscoveredModules is the scalar upper bound (HISS-02) on the modules a single
// repository scan or bump discovers.
const MaxDiscoveredModules = 1024

// maxDiscoveryWalkEntries bounds how many entries walkGoModules visits (HISS-02).
const maxDiscoveryWalkEntries = 100000

// DiscoverGoModules finds directories containing go.mod, returning no partial result.
// Deprecated: use DiscoverGoModulesChecked to distinguish an empty tree from a read failure.
func DiscoverGoModules(ctx context.Context, repoPath string) []string {
	modules, err := DiscoverGoModulesChecked(ctx, repoPath)
	if err != nil {
		return nil
	}
	return modules
}

// DiscoverGoModulesChecked finds bounded modules and reports incomplete discovery.
func DiscoverGoModulesChecked(ctx context.Context, repoPath string) ([]string, error) {
	dirs, err := parseGoWork(ctx, filepath.Join(repoPath, "go.work"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	modules, err := existingGoModules(repoPath, dirs)
	if err != nil || len(modules) > 0 {
		return modules, err
	}
	rootModule, err := existingGoModules(repoPath, []string{"."})
	if err != nil || len(rootModule) > 0 {
		return rootModule, err
	}
	return walkGoModules(repoPath)
}

// ReadManifest reads a manifest file confined to root.
func ReadManifest(ctx context.Context, root, name string) ([]byte, error) {
	path, err := util.ConfinePath(root, name)
	if err != nil {
		return nil, err
	}
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", name, err)
	}
	return data, nil
}

func existingGoModules(repoPath string, dirs []string) ([]string, error) {
	if len(dirs) > MaxDiscoveredModules {
		return nil, fmt.Errorf("module discovery exceeds %d directories", MaxDiscoveredModules)
	}
	var modules []string
	for i := 0; i < len(dirs) && i < MaxDiscoveredModules; i++ {
		dir := dirs[i]
		path, err := util.ConfinePath(repoPath, filepath.Join(dir, "go.mod"))
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect module: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("module manifest must be regular: %s", path)
		}
		modules = append(modules, dir)
	}
	return modules, nil
}

type goModuleWalker struct {
	repoPath string
	modules  []string
	visited  int
}

func walkGoModules(repoPath string) ([]string, error) {
	walker := goModuleWalker{repoPath: repoPath}
	if err := filepath.WalkDir(repoPath, walker.visit); err != nil {
		return nil, fmt.Errorf("discover Go modules: %w", err)
	}
	return walker.modules, nil
}

func (walker *goModuleWalker) visit(path string, entry os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	walker.visited++
	if walker.visited > maxDiscoveryWalkEntries {
		return fmt.Errorf("module discovery exceeds %d entries", maxDiscoveryWalkEntries)
	}
	if !entry.IsDir() {
		return nil
	}
	rel, err := filepath.Rel(walker.repoPath, path)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	if skipModuleDirectory(rel, entry.Name()) {
		return filepath.SkipDir
	}
	found, err := existingGoModules(walker.repoPath, []string{rel})
	if err != nil {
		return err
	}
	walker.modules = append(walker.modules, found...)
	if len(walker.modules) > MaxDiscoveredModules {
		return fmt.Errorf("module discovery exceeds %d directories", MaxDiscoveredModules)
	}
	return nil
}

func skipModuleDirectory(rel, name string) bool {
	return strings.Count(rel, string(filepath.Separator)) > 2 || strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules"
}

func parseGoWork(ctx context.Context, path string) ([]string, error) {
	data, err := ReadManifest(ctx, filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return nil, err
	}
	var dirs []string
	lines := strings.Split(string(data), "\n")
	inUseBlock := false
	for i := 0; i < len(lines) && i < maxManifestLines; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "use (") {
			inUseBlock = true
			continue
		}
		if inUseBlock && trimmed == ")" {
			inUseBlock = false
			continue
		}
		if inUseBlock && trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			clean := strings.Trim(trimmed, `"' `)
			dirs = append(dirs, filepath.Clean(clean))
		} else if strings.HasPrefix(trimmed, "use ") {
			clean := strings.TrimPrefix(trimmed, "use ")
			clean = strings.Trim(clean, `"' `)
			dirs = append(dirs, filepath.Clean(clean))
		}
	}
	return dirs, nil
}
