package bump

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ApplyUpdate updates a single dependency in repoPath according to its candidate spec.
func ApplyUpdate(ctx context.Context, repoPath string, cand UpgradeCandidate) error {
	return applyUpdateInternal(ctx, repoPath, cand, true)
}

func applyUpdateInternal(ctx context.Context, repoPath string, cand UpgradeCandidate, tidy bool) error {
	if ctx == nil {
		return fmt.Errorf("update requires context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := util.ValidateExecArg(cand.Package); err != nil {
		return fmt.Errorf("invalid package: %w", err)
	}
	if err := util.ValidateExecArg(cand.TargetVersion); err != nil {
		return fmt.Errorf("invalid target version: %w", err)
	}
	targetDir, err := util.ConfinePath(repoPath, cand.ModuleDir)
	if err != nil {
		return fmt.Errorf("invalid module directory: %w", err)
	}

	switch cand.ManifestType {
	case "go.mod":
		return applyGoUpdate(ctx, targetDir, cand, tidy)
	case "package.json":
		return applyNodeUpdate(ctx, targetDir, cand)
	default:
		return fmt.Errorf("unsupported manifest type: %s", cand.ManifestType)
	}
}

func applyGoUpdate(ctx context.Context, targetDir string, cand UpgradeCandidate, tidy bool) error {
	targetSpec := fmt.Sprintf("%s@%s", cand.Package, cand.TargetVersion)
	if out, err := util.RunCommand(ctx, targetDir, "go", "get", targetSpec); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// If go get fails, try direct fallback to go.mod edit + go mod tidy
		if fbErr := fallbackGoModEdit(ctx, targetDir, cand); fbErr != nil {
			return fmt.Errorf("go get %s failed: %w (%s)", targetSpec, err, strings.TrimSpace(out))
		}
	}

	if tidy {
		if _, tidyErr := util.RunCommand(ctx, targetDir, "go", "mod", "tidy"); tidyErr != nil {
			return fmt.Errorf("go mod tidy in %s: %w", targetDir, tidyErr)
		}
	}
	return nil
}

func fallbackGoModEdit(ctx context.Context, targetDir string, cand UpgradeCandidate) error {
	goModPath := filepath.Join(targetDir, "go.mod")
	data, err := readManifest(ctx, targetDir, "go.mod")
	if err != nil {
		return err
	}
	content := string(data)
	oldPat := fmt.Sprintf("%s %s", cand.Package, cand.CurrentVersion)
	newPat := fmt.Sprintf("%s %s", cand.Package, cand.TargetVersion)
	if strings.Contains(content, oldPat) {
		content = strings.Replace(content, oldPat, newPat, 1)
		return writeManifest(ctx, targetDir, "go.mod", data, []byte(content))
	}
	return fmt.Errorf("pattern %s not found in %s", oldPat, goModPath)
}

func applyNodeUpdate(ctx context.Context, targetDir string, cand UpgradeCandidate) error {
	// Try pnpm update first if pnpm lockfile exists or pnpm is used
	pnpmLock := filepath.Join(targetDir, "pnpm-lock.yaml")
	if util.FileExists(pnpmLock) || util.FileExists(filepath.Join(targetDir, "..", "pnpm-lock.yaml")) {
		spec := fmt.Sprintf("%s@%s", cand.Package, cand.TargetVersion)
		cmdOut, err := util.RunCommand(ctx, targetDir, "pnpm", "update", spec)
		if err == nil {
			return nil
		}
		if len(cmdOut) == 0 || ctx.Err() != nil {
			return err
		}
	}

	return updatePackageManifest(ctx, targetDir, cand)
}

func updatePackageManifest(ctx context.Context, targetDir string, cand UpgradeCandidate) error {
	pkgFile := filepath.Join(targetDir, "package.json")
	data, err := readManifest(ctx, targetDir, "package.json")
	if err != nil {
		return err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	updated := false
	for _, sec := range []string{"dependencies", "devDependencies"} {
		if deps, ok := raw[sec].(map[string]interface{}); ok {
			if _, exists := deps[cand.Package]; exists {
				deps[cand.Package] = "^" + strings.TrimPrefix(cand.TargetVersion, "^")
				updated = true
			}
		}
	}

	if !updated {
		return fmt.Errorf("package %s not found in %s", cand.Package, pkgFile)
	}

	outData, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return writeManifest(ctx, targetDir, "package.json", data, append(outData, '\n'))
}

// UpdateAll batches updates for all given candidates across repoPath.
func UpdateAll(ctx context.Context, repoPath string, candidates []UpgradeCandidate) (int, error) {
	applied := 0
	var failures []error
	modulesToTidy := make(map[string]bool)

	for _, c := range candidates {
		if err := applyUpdateInternal(ctx, repoPath, c, false); err != nil {
			failures = append(failures, fmt.Errorf("update %s: %w", c.Package, err))
			continue
		}
		applied++
		if c.ManifestType == "go.mod" {
			targetDir := repoPath
			if c.ModuleDir != "" && c.ModuleDir != "." {
				targetDir = filepath.Join(repoPath, c.ModuleDir)
			}
			modulesToTidy[targetDir] = true
		}
	}

	// Final tidy pass for Go modules
	for dir := range modulesToTidy {
		if _, tidyErr := util.RunCommand(ctx, dir, "go", "mod", "tidy"); tidyErr != nil {
			failures = append(failures, fmt.Errorf("tidy %s: %w", dir, tidyErr))
			continue
		}
	}

	return applied, errors.Join(failures...)
}
