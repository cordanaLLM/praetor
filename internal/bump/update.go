package bump

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/semver"
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

// errGoModFallbackEdit marks an update whose `go get` failed and whose requirement was then
// rewritten in go.mod directly. The manifest names the target version, but no go command
// resolved it, so the update is reported as failed with the go get error attached rather
// than as applied.
var errGoModFallbackEdit = errors.New("go get failed; go.mod requirement rewritten directly")

// errFallbackRefused reports a go.mod fallback edit refused before anything was written,
// because the edit could not name exactly one require line and one module version to write.
var errFallbackRefused = errors.New("go.mod fallback edit refused")

func applyGoUpdate(ctx context.Context, targetDir string, cand UpgradeCandidate, tidy bool) error {
	targetSpec := fmt.Sprintf("%s@%s", cand.Package, cand.TargetVersion)
	out, err := util.RunCommand(ctx, targetDir, "go", "get", targetSpec)
	if err == nil {
		return tidyGoModule(ctx, targetDir, tidy)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	getErr := fmt.Errorf("go get %s failed: %w (%s)", targetSpec, err, out)
	if fbErr := fallbackGoModEdit(ctx, targetDir, cand); fbErr != nil {
		return errors.Join(getErr, fbErr)
	}
	// The text edit is a fallback, not a success: the go get error stays in the result so
	// the caller never reports an update that no go command resolved as applied.
	return errors.Join(fmt.Errorf("%w: %w", errGoModFallbackEdit, getErr), tidyGoModule(ctx, targetDir, tidy))
}

// tidyGoModule runs go mod tidy in targetDir when tidy is set.
func tidyGoModule(ctx context.Context, targetDir string, tidy bool) error {
	if !tidy {
		return nil
	}
	if _, err := util.RunCommand(ctx, targetDir, "go", "mod", "tidy"); err != nil {
		return fmt.Errorf("go mod tidy in %s: %w", targetDir, err)
	}
	return nil
}

// fallbackGoModEdit rewrites the version on the one require line that names cand.Package at
// cand.CurrentVersion, and no other byte of go.mod. It is refused, with go.mod untouched,
// when the current version is unknown, when the target is not a module version, and when no
// require line or more than one names the package. Comment, exclude and replace lines never
// match: require lines are identified by gomanifest.RequirementLine, the parser the scanners
// and CurrentGoModVersion share.
func fallbackGoModEdit(ctx context.Context, targetDir string, cand UpgradeCandidate) error {
	if cand.CurrentVersion == "" {
		return fmt.Errorf("%w: current version of %s is unknown", errFallbackRefused, cand.Package)
	}
	if !isModuleVersion(cand.TargetVersion) {
		return fmt.Errorf("%w: target %q is not a module version", errFallbackRefused, cand.TargetVersion)
	}
	data, err := readManifest(ctx, targetDir, "go.mod")
	if err != nil {
		return err
	}
	edited, err := rewriteRequirement(string(data), cand.Package, cand.CurrentVersion, cand.TargetVersion)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Join(targetDir, "go.mod"), err)
	}
	return writeManifest(ctx, targetDir, "go.mod", data, []byte(edited))
}

// isModuleVersion reports whether version can stand as the version token of a go.mod require
// line: a "v"-prefixed SemVer with no surrounding space. A go get query such as "latest" or
// a branch name is valid on the command line but not in the manifest.
func isModuleVersion(version string) bool {
	_, ok := semver.Parse(version)
	return ok && strings.HasPrefix(version, "v") && strings.TrimSpace(version) == version
}

// rewriteRequirement returns manifest with the version token of pkg's single require line
// changed from current to target. Every other line, and the rest of that line (indentation,
// the require keyword, alignment, a trailing comment, the line ending), is kept byte for byte.
func rewriteRequirement(manifest, pkg, current, target string) (string, error) {
	lines := strings.SplitAfter(manifest, "\n")
	count := len(lines)
	if lines[count-1] == "" {
		count--
	}
	if count > MaxManifestLines {
		return "", fmt.Errorf("%w: manifest exceeds %d lines", errFallbackRefused, MaxManifestLines)
	}
	index, err := requirementLineIndex(lines, pkg, current)
	if err != nil {
		return "", err
	}
	line := lines[index]
	start := requirementVersionOffset(line, pkg)
	if start < 0 || !strings.HasPrefix(line[start:], current) {
		return "", fmt.Errorf("%w: cannot locate %s %s on line %d", errFallbackRefused, pkg, current, index+1)
	}
	lines[index] = line[:start] + target + line[start+len(current):]
	return strings.Join(lines, ""), nil
}

// requirementLineIndex returns the index of the only require line naming pkg, and refuses
// when there is none, more than one, or when that line requires a version other than current.
func requirementLineIndex(lines []string, pkg, current string) (int, error) {
	index, version := -1, ""
	inRequire := false
	for i := 0; i < len(lines) && i < MaxManifestLines; i++ {
		found, ok := requiredModuleVersion(lines[i], &inRequire, pkg)
		if !ok {
			continue
		}
		if index >= 0 {
			return -1, fmt.Errorf("%w: %s is required on lines %d and %d", errFallbackRefused, pkg, index+1, i+1)
		}
		index, version = i, found
	}
	if index < 0 {
		return -1, fmt.Errorf("%w: no require line names %s", errFallbackRefused, pkg)
	}
	if version != current {
		return -1, fmt.Errorf("%w: %s is required at %s, not %s", errFallbackRefused, pkg, version, current)
	}
	return index, nil
}

// requirementVersionOffset returns the byte offset of the version token on a line that
// requiredModuleVersion accepted for pkg, or -1. It walks the line the way
// gomanifest.RequirementLine reads it: leading space, an optional "require " keyword, space,
// the module path, space, then the version.
func requirementVersionOffset(line, pkg string) int {
	body := strings.TrimLeftFunc(line, unicode.IsSpace)
	body = strings.TrimPrefix(body, "require ")
	body = strings.TrimLeftFunc(body, unicode.IsSpace)
	rest, found := strings.CutPrefix(body, pkg)
	if !found {
		return -1
	}
	version := strings.TrimLeftFunc(rest, unicode.IsSpace)
	if len(version) == len(rest) {
		return -1
	}
	return len(line) - len(version)
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
		err := applyUpdateInternal(ctx, repoPath, c, false)
		if err != nil {
			failures = append(failures, fmt.Errorf("update %s: %w", c.Package, err))
		} else {
			applied++
		}
		// A fallback edit is a failed update that still changed go.mod, so the module is
		// tidied like an applied one: tidy either reconciles go.sum or reports why not.
		if c.ManifestType == "go.mod" && (err == nil || errors.Is(err, errGoModFallbackEdit)) {
			targetDir := repoPath
			if c.ModuleDir != "" && c.ModuleDir != "." {
				targetDir = filepath.Join(repoPath, c.ModuleDir)
			}
			modulesToTidy[targetDir] = true
		}
	}

	// Final tidy pass for Go modules
	for dir := range modulesToTidy {
		if tidyErr := tidyGoModule(ctx, dir, true); tidyErr != nil {
			failures = append(failures, tidyErr)
		}
	}

	return applied, errors.Join(failures...)
}
