// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

var (
	goRequireRegex = regexp.MustCompile(`^\s*([a-zA-Z0-9.\-_/]+)\s+v([0-9a-zA-Z.\-_+]+)`)
	actionRegex    = regexp.MustCompile(`uses:\s*([a-zA-Z0-9\-_/]+)@([a-zA-Z0-9.\-_+]+)`)
)

// ScanDeclaredDependencies extracts all declared dependencies across manifests in repoPath.
func ScanDeclaredDependencies(ctx context.Context, repoPath string, includeTransitive bool) ([]PackageRef, error) {
	if ctx == nil {
		return nil, fmt.Errorf("docdistill: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("docdistill: context cancelled: %w", err)
	}

	var allRefs []PackageRef

	// 1. Scan Go modules
	goRefs, err := scanGoDependencies(repoPath, includeTransitive)
	if err == nil && len(goRefs) > 0 {
		allRefs = append(allRefs, goRefs...)
	}

	// 2. Scan Node packages
	nodeRefs, err := scanNodeDependencies(repoPath, includeTransitive)
	if err == nil && len(nodeRefs) > 0 {
		allRefs = append(allRefs, nodeRefs...)
	}

	// 3. Scan GitHub Actions
	actionRefs, err := scanWorkflowActions(repoPath)
	if err == nil && len(actionRefs) > 0 {
		allRefs = append(allRefs, actionRefs...)
	}

	return deduplicatePackageRefs(allRefs), nil
}

func scanGoDependencies(repoPath string, includeTransitive bool) ([]PackageRef, error) {
	goModPath := filepath.Join(repoPath, "go.mod")
	if !util.FileExists(goModPath) {
		return nil, nil
	}

	file, err := os.Open(goModPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open go.mod: %w", err)
	}
	defer file.Close()

	var refs []PackageRef
	scanner := bufio.NewScanner(file)
	inRequire := false

	for i := 0; i < 5000 && scanner.Scan(); i++ {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "require (") {
			inRequire = true
			continue
		}
		if inRequire && line == ")" {
			inRequire = false
			continue
		}

		if inRequire || strings.HasPrefix(line, "require ") {
			clean := strings.TrimPrefix(line, "require ")
			matches := goRequireRegex.FindStringSubmatch(clean)
			if len(matches) == 3 {
				pkgName := matches[1]
				ver := "v" + matches[2]
				isIndirect := strings.Contains(line, "// indirect")
				if isIndirect && !includeTransitive {
					continue
				}
				refs = append(refs, PackageRef{
					Name:       pkgName,
					Version:    ver,
					Kind:       KindGoModule,
					Manifest:   "go.mod",
					Direct:     !isIndirect,
					Repository: "https://" + pkgName,
				})
			}
		}
	}

	return refs, scanner.Err()
}

type packageJSONDeps struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func scanNodeDependencies(repoPath string, includeTransitive bool) ([]PackageRef, error) {
	pkgJSONPath := filepath.Join(repoPath, "package.json")
	if !util.FileExists(pkgJSONPath) {
		return nil, nil
	}

	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return nil, fmt.Errorf("failed reading package.json: %w", err)
	}

	var parsed packageJSONDeps
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("failed unmarshaling package.json: %w", err)
	}

	var refs []PackageRef
	addDeps := func(deps map[string]string, direct bool) {
		for pkg, ver := range deps {
			cleanVer := strings.TrimPrefix(ver, "^")
			cleanVer = strings.TrimPrefix(cleanVer, "~")
			refs = append(refs, PackageRef{
				Name:       pkg,
				Version:    cleanVer,
				Kind:       KindNodePackage,
				Manifest:   "package.json",
				Direct:     direct,
				Repository: "https://www.npmjs.com/package/" + pkg,
			})
		}
	}

	addDeps(parsed.Dependencies, true)
	if includeTransitive {
		addDeps(parsed.DevDependencies, false)
	}

	return refs, nil
}

func scanWorkflowActions(repoPath string) ([]PackageRef, error) {
	workflowDir := filepath.Join(repoPath, ".github", "workflows")
	if !util.DirExists(workflowDir) {
		return nil, nil
	}

	var refs []PackageRef
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		return nil, fmt.Errorf("failed reading workflow dir: %w", err)
	}

	for i := 0; i < 500 && i < len(entries); i++ {
		entry := entries[i]
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(entry.Name())
		if ext != ".yml" && ext != ".yaml" {
			continue
		}

		fPath := filepath.Join(workflowDir, entry.Name())
		content, err := os.ReadFile(fPath)
		if err != nil {
			continue
		}

		matches := actionRegex.FindAllStringSubmatch(string(content), 100)
		for _, m := range matches {
			if len(m) == 3 {
				actionName := m[1]
				ref := m[2]
				if strings.HasPrefix(actionName, ".") {
					continue
				}
				refs = append(refs, PackageRef{
					Name:       actionName,
					Version:    ref,
					Kind:       KindGitHubAction,
					Manifest:   filepath.Join(".github", "workflows", entry.Name()),
					Direct:     true,
					Repository: "https://github.com/" + actionName,
				})
			}
		}
	}

	return refs, nil
}

func deduplicatePackageRefs(refs []PackageRef) []PackageRef {
	seen := make(map[string]bool)
	var deduped []PackageRef

	for _, ref := range refs {
		key := fmt.Sprintf("%s:%s", ref.Name, ref.Version)
		if !seen[key] {
			seen[key] = true
			deduped = append(deduped, ref)
		}
	}
	return deduped
}
