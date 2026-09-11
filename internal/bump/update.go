package bump

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ApplyUpdate updates a single dependency in repoPath according to its candidate spec.
func ApplyUpdate(ctx context.Context, repoPath string, cand UpgradeCandidate) error {
	return applyUpdateInternal(ctx, repoPath, cand, true)
}

func applyUpdateInternal(ctx context.Context, repoPath string, cand UpgradeCandidate, tidy bool) error {
	targetDir := repoPath
	if cand.ModuleDir != "" && cand.ModuleDir != "." {
		targetDir = filepath.Join(repoPath, cand.ModuleDir)
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
	cmd := exec.CommandContext(ctx, "go", "get", targetSpec)
	cmd.Dir = targetDir
	if out, err := cmd.CombinedOutput(); err != nil {
		// If go get fails, try direct fallback to go.mod edit + go mod tidy
		if fbErr := fallbackGoModEdit(targetDir, cand); fbErr != nil {
			return fmt.Errorf("go get %s failed: %w (%s)", targetSpec, err, strings.TrimSpace(string(out)))
		}
	}

	if tidy {
		tidyCmd := exec.CommandContext(ctx, "go", "mod", "tidy")
		tidyCmd.Dir = targetDir
		if tidyErr := tidyCmd.Run(); tidyErr != nil {
			return fmt.Errorf("go mod tidy in %s: %w", targetDir, tidyErr)
		}
	}
	return nil
}

func fallbackGoModEdit(targetDir string, cand UpgradeCandidate) error {
	goModPath := filepath.Join(targetDir, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return err
	}
	content := string(data)
	oldPat := fmt.Sprintf("%s %s", cand.Package, cand.CurrentVersion)
	newPat := fmt.Sprintf("%s %s", cand.Package, cand.TargetVersion)
	if strings.Contains(content, oldPat) {
		content = strings.Replace(content, oldPat, newPat, 1)
		return os.WriteFile(goModPath, []byte(content), 0644)
	}
	return fmt.Errorf("pattern %s not found in %s", oldPat, goModPath)
}

func applyNodeUpdate(ctx context.Context, targetDir string, cand UpgradeCandidate) error {
	// Try pnpm update first if pnpm lockfile exists or pnpm is used
	pnpmLock := filepath.Join(targetDir, "pnpm-lock.yaml")
	if util.FileExists(pnpmLock) || util.FileExists(filepath.Join(targetDir, "..", "pnpm-lock.yaml")) {
		spec := fmt.Sprintf("%s@%s", cand.Package, cand.TargetVersion)
		cmd := exec.CommandContext(ctx, "pnpm", "update", spec)
		cmd.Dir = targetDir
		cmdOut, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		if len(cmdOut) == 0 {
			return err
		}
	}

	// Fallback to direct package.json update
	pkgFile := filepath.Join(targetDir, "package.json")
	data, err := os.ReadFile(pkgFile)
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
	return os.WriteFile(pkgFile, append(outData, '\n'), 0644)
}

// UpdateAll batches updates for all given candidates across repoPath.
func UpdateAll(ctx context.Context, repoPath string, candidates []UpgradeCandidate) (int, error) {
	applied := 0
	modulesToTidy := make(map[string]bool)

	for _, c := range candidates {
		if err := applyUpdateInternal(ctx, repoPath, c, false); err != nil {
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
		cmd := exec.CommandContext(ctx, "go", "mod", "tidy")
		cmd.Dir = dir
		if tidyErr := cmd.Run(); tidyErr != nil {
			continue
		}
	}

	return applied, nil
}
