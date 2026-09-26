// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

// HarvestDocumentation retrieves raw documentation content for a package
// reference declared by the repository at repoPath.
//
// Every local lookup is anchored to repoPath, never to the process's working
// directory: `go doc` runs in the repository, so the repository's own module
// graph selects the version, and Node READMEs are read from the repository's
// node_modules.
func HarvestDocumentation(ctx context.Context, repoPath string, ref PackageRef, offline bool) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("harvester: context cannot be nil")
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(repoPath) == "" {
		return "", fmt.Errorf("harvester: repository path cannot be empty")
	}
	if err := util.ValidateExecArg(ref.Name); err != nil {
		return "", fmt.Errorf("invalid package name: %w", err)
	}
	switch ref.Kind {
	case KindGoModule:
		return harvestGoModule(ctx, repoPath, ref, offline)
	case KindGitHubAction:
		return harvestGitHubAction(ctx, ref, offline)
	case KindNodePackage:
		return harvestNodePackage(ctx, repoPath, ref)
	default:
		return "", fmt.Errorf("unsupported documentation kind: %s", ref.Kind)
	}
}

// goDocTarget names what `go doc` documents. Online, the declared version is
// pinned as package@version, which the go command resolves through the module
// proxy. Offline that lookup is not available — `go doc pkg@version` fails
// under GOPROXY=off while it loads deprecation data — so the bare path is used
// and the repository's module graph, from which ref.Version was read, selects
// the version.
func goDocTarget(ref PackageRef, offline bool) string {
	if offline || ref.Version == "" {
		return ref.Name
	}
	return ref.Name + "@" + ref.Version
}

func harvestGoModule(ctx context.Context, repoPath string, ref PackageRef, offline bool) (string, error) {
	if ref.Version != "" {
		if err := util.ValidateExecArg(ref.Version); err != nil {
			return "", fmt.Errorf("invalid module version: %w", err)
		}
	}
	out, commandErr := util.RunCommandBytes(ctx, repoPath, "go", 1<<20, "doc", goDocTarget(ref, offline))
	if commandErr == nil && len(out.Stdout) > 50 {
		return string(out.Stdout), nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	content, cacheErr := cachedGoDocumentation(ctx, ref)
	if cacheErr == nil && content != "" {
		return content, nil
	}
	if !offline && strings.HasPrefix(ref.Name, "github.com/") {
		content, err := githubGoDocumentation(ctx, ref)
		if err == nil {
			return content, nil
		}
		return "", fmt.Errorf("go documentation unavailable: %w", errors.Join(commandErr, cacheErr, err))
	}
	return "", fmt.Errorf("go documentation unavailable for %s@%s: %w", ref.Name, ref.Version, errors.Join(commandErr, cacheErr))
}

// cachedGoDocumentation reads the module's README or doc.go from the module
// cache, under the case-escaped directory the go command extracts it to.
func cachedGoDocumentation(ctx context.Context, ref PackageRef) (string, error) {
	cacheRoot, ok := gomanifest.ModuleCacheRoot()
	if !ok {
		return "", errors.New("module cache location unresolved: GOMODCACHE, GOPATH and the home directory are all unset")
	}
	moduleDir, err := gomanifest.ModuleCacheDir(ref.Name, ref.Version)
	if err != nil {
		return "", err
	}
	var failures []error
	for _, candidate := range []string{"README.md", "readme.md", "README", "doc.go"} {
		data, err := readDocumentationFile(ctx, cacheRoot, filepath.Join(moduleDir, candidate))
		if err == nil && len(data) > 0 {
			return string(data), nil
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return "", errors.Join(failures...)
}

func githubGoDocumentation(ctx context.Context, ref PackageRef) (string, error) {
	parts := strings.Split(ref.Name, "/")
	if len(parts) < 3 {
		return "", fmt.Errorf("invalid GitHub module %s", ref.Name)
	}
	remoteURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/README.md", parts[1], parts[2], ref.Version)
	return fetchURLWithTimeout(ctx, remoteURL)
}

func harvestGitHubAction(ctx context.Context, ref PackageRef, offline bool) (string, error) {
	parts := strings.Split(ref.Name, "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid action name %s", ref.Name)
	}
	owner := parts[0]
	repo := parts[1]

	if !offline {
		// Fetch action.yml or action.yaml first for exact input/output interface
		actionURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/action.yml", owner, repo, ref.Version)
		actionYAML, err := fetchURLWithTimeout(ctx, actionURL)
		if err != nil {
			actionURL = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/action.yaml", owner, repo, ref.Version)
			actionYAML, err = fetchURLWithTimeout(ctx, actionURL)
			if err != nil {
				actionYAML = ""
			}
		}

		readmeURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/README.md", owner, repo, ref.Version)
		readmeMD, err := fetchURLWithTimeout(ctx, readmeURL)
		if err != nil {
			readmeMD = ""
		}

		combined := fmt.Sprintf("# GitHub Action: %s@%s\n\n", ref.Name, ref.Version)
		if actionYAML != "" {
			combined += fmt.Sprintf("## action.yml Interface:\n```yaml\n%s\n```\n\n", actionYAML)
		}
		if readmeMD != "" {
			combined += fmt.Sprintf("## Documentation:\n%s\n", readmeMD)
		}
		if actionYAML != "" || readmeMD != "" {
			return combined, nil
		}
	}

	return "", fmt.Errorf("action documentation unavailable for %s@%s", ref.Name, ref.Version)
}

// harvestNodePackage reads the package README from the repository's
// node_modules: first beside the manifest that declared it, where pnpm links a
// workspace member's dependencies, then at the repository root, where npm and
// yarn hoist them. pnpm's isolated linker makes each node_modules/<pkg> a
// symlink into node_modules/.pnpm, so the README is read through
// readLinkedDocumentationFile, which follows links that stay inside repoPath.
func harvestNodePackage(ctx context.Context, repoPath string, ref PackageRef) (string, error) {
	var failures []error
	for _, dir := range nodeModuleDirs(ref.Manifest) {
		data, err := readLinkedDocumentationFile(ctx, repoPath, filepath.Join(dir, "node_modules", ref.Name, "README.md"))
		if err == nil && len(data) > 0 {
			return string(data), nil
		}
		if err == nil {
			err = fmt.Errorf("npm documentation is empty for %s", ref.Name)
		}
		failures = append(failures, err)
	}
	return "", fmt.Errorf("npm documentation unavailable for %s: %w", ref.Name, errors.Join(failures...))
}

// readLinkedDocumentationFile reads root/name after resolving the symlinks on
// the way to it. readDocumentationFile's snapshot refuses a symlinked directory
// or file, which is right for repository content but refuses every package a
// linker placed with a symlink. util.ConfinePath first rejects a name whose
// resolved target leaves root; the resolved path is then read, relative to the
// resolved root, through readDocumentationFile, so it is confined again and no
// further link is followed.
func readLinkedDocumentationFile(ctx context.Context, root, name string) ([]byte, error) {
	linked, err := util.ConfinePath(root, name)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(linked)
	if err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil {
		return nil, fmt.Errorf("resolve %s under %s: %w", name, root, err)
	}
	return readDocumentationFile(ctx, resolvedRoot, rel)
}

// nodeModuleDirs returns the repo-relative directories whose node_modules may
// hold a package declared in manifest: the manifest's own directory, then the
// repository root.
func nodeModuleDirs(manifest string) []string {
	dir := filepath.Dir(filepath.FromSlash(manifest))
	if manifest == "" || dir == "." {
		return []string{"."}
	}
	return []string{dir, "."}
}

func fetchURLWithTimeout(ctx context.Context, url string) (content string, resultErr error) {
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Praetor-DocDistiller/1.0")

	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, resp.Body.Close()) }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http status %d", resp.StatusCode)
	}

	lr := io.LimitReader(resp.Body, 256*1024+1) // Cap at 256KB
	body, err := io.ReadAll(lr)
	if err != nil {
		return "", err
	}
	if len(body) > 256*1024 {
		return "", fmt.Errorf("documentation response exceeds 256 KiB")
	}
	if len(body) == 0 {
		return "", fmt.Errorf("documentation response is empty")
	}
	return string(body), nil
}
