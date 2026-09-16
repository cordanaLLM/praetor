// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/nodemanifest"
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
	goRefs, err := scanGoDependencies(ctx, repoPath, includeTransitive)
	if err != nil {
		return nil, fmt.Errorf("scan Go dependencies: %w", err)
	}
	allRefs = append(allRefs, goRefs...)
	nodeRefs, err := scanNodeDependencies(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("scan Node dependencies: %w", err)
	}
	allRefs = append(allRefs, nodeRefs...)
	actionRefs, err := scanWorkflowActions(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("scan workflow actions: %w", err)
	}
	allRefs = append(allRefs, actionRefs...)
	return deduplicatePackageRefs(allRefs), nil
}

func readDocumentationFile(ctx context.Context, root, name string) ([]byte, error) {
	path, err := util.ConfinePath(root, name)
	if err != nil {
		return nil, err
	}
	return contextopt.ReadSnapshot(ctx, path)
}

func scanGoDependencies(ctx context.Context, repoPath string, includeTransitive bool) ([]PackageRef, error) {
	data, err := readDocumentationFile(ctx, repoPath, "go.mod")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var refs []PackageRef
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	inRequire := false
	for i := 0; i <= 5000 && scanner.Scan(); i++ {
		if i == 5000 {
			return nil, fmt.Errorf("go manifest exceeds 5000 lines")
		}
		line, required := gomanifest.RequirementLine(scanner.Text(), &inRequire)
		if !required {
			continue
		}
		ref, ok := goPackageRef(line, includeTransitive)
		if ok {
			refs = append(refs, ref)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return refs, nil
}

func goPackageRef(line string, includeTransitive bool) (PackageRef, bool) {
	matches := goRequireRegex.FindStringSubmatch(line)
	if len(matches) != 3 {
		return PackageRef{}, false
	}
	indirect := strings.Contains(line, "// indirect")
	if indirect && !includeTransitive {
		return PackageRef{}, false
	}
	return PackageRef{
		Name: matches[1], Version: "v" + matches[2], Kind: KindGoModule,
		Manifest: "go.mod", Direct: !indirect, Repository: "https://" + matches[1],
	}, true
}

type packageJSONDeps struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// scanNodeDependencies reads every package.json the repository declares — the
// root plus each workspace member — not just the root one.
//
// Both maps count as direct. npm's `devDependencies` are declared in the
// repository's own manifest, so they are direct by every definition the
// ecosystem uses; npm's genuinely transitive set lives in the lockfile, which is
// not read here. Gating devDependencies behind the transitive flag (issue #96)
// mirrored Go's `// indirect`, where the distinction is real, and hid every
// dependency of a repository whose npm deps are all development tooling.
func scanNodeDependencies(ctx context.Context, repoPath string) ([]PackageRef, error) {
	dirs, err := nodemanifest.DiscoverPackageDirs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("discover node packages: %w", err)
	}

	var refs []PackageRef
	for _, dir := range dirs {
		manifest := "package.json"
		if dir != "." {
			manifest = dir + "/package.json"
		}
		manifestRefs, readErr := scanOneNodeManifest(ctx, repoPath, manifest)
		if readErr != nil {
			return nil, readErr
		}
		refs = append(refs, manifestRefs...)
	}
	return refs, nil
}

// scanOneNodeManifest reads the dependency maps of a single package.json.
func scanOneNodeManifest(ctx context.Context, repoPath, manifest string) ([]PackageRef, error) {
	data, err := readDocumentationFile(ctx, repoPath, manifest)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", manifest, err)
	}

	var parsed packageJSONDeps
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("failed unmarshaling %s: %w", manifest, err)
	}

	refs := make([]PackageRef, 0, len(parsed.Dependencies)+len(parsed.DevDependencies))
	for _, deps := range []map[string]string{parsed.Dependencies, parsed.DevDependencies} {
		for pkg, ver := range deps {
			refs = append(refs, nodePackageRef(pkg, ver, manifest))
		}
	}
	return refs, nil
}

// nodePackageRef builds one reference, stripping the range prefix from a version.
func nodePackageRef(pkg, version, manifest string) PackageRef {
	cleanVer := strings.TrimPrefix(version, "^")
	cleanVer = strings.TrimPrefix(cleanVer, "~")
	return PackageRef{
		Name:       pkg,
		Version:    cleanVer,
		Kind:       KindNodePackage,
		Manifest:   manifest,
		Direct:     true,
		Repository: "https://www.npmjs.com/package/" + pkg,
	}
}

func scanWorkflowActions(ctx context.Context, repoPath string) ([]PackageRef, error) {
	workflowDir, err := util.ConfinePath(repoPath, filepath.Join(".github", "workflows"))
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(workflowDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read workflow directory: %w", err)
	}
	if len(entries) > 500 {
		return nil, fmt.Errorf("workflow directory exceeds 500 entries")
	}
	var refs []PackageRef
	for _, entry := range entries {
		ext := filepath.Ext(entry.Name())
		if entry.IsDir() || (ext != ".yml" && ext != ".yaml") {
			continue
		}
		manifest := filepath.Join(".github", "workflows", entry.Name())
		content, err := readDocumentationFile(ctx, repoPath, manifest)
		if err != nil {
			return nil, fmt.Errorf("read workflow %s: %w", entry.Name(), err)
		}
		actions, err := workflowPackageRefs(string(content), manifest)
		if err != nil {
			return nil, err
		}
		refs = append(refs, actions...)
	}
	return refs, nil
}

func workflowPackageRefs(content, manifest string) ([]PackageRef, error) {
	matches := actionRegex.FindAllStringSubmatch(content, 101)
	if len(matches) > 100 {
		return nil, fmt.Errorf("workflow %s exceeds 100 actions", manifest)
	}
	var refs []PackageRef
	for _, match := range matches {
		if len(match) != 3 || strings.HasPrefix(match[1], ".") {
			continue
		}
		refs = append(refs, PackageRef{
			Name: match[1], Version: match[2], Kind: KindGitHubAction,
			Manifest: manifest, Direct: true, Repository: "https://github.com/" + match[1],
		})
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
