// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// HarvestDocumentation retrieves raw documentation content for a package reference.
func HarvestDocumentation(ctx context.Context, ref PackageRef, offline bool) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("harvester: context cannot be nil")
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := util.ValidateExecArg(ref.Name); err != nil {
		return "", fmt.Errorf("invalid package name: %w", err)
	}
	switch ref.Kind {
	case KindGoModule:
		return harvestGoModule(ctx, ref, offline)
	case KindGitHubAction:
		return harvestGitHubAction(ctx, ref, offline)
	case KindNodePackage:
		return harvestNodePackage(ctx, ref, offline)
	default:
		return "", fmt.Errorf("unsupported documentation kind: %s", ref.Kind)
	}
}

func harvestGoModule(ctx context.Context, ref PackageRef, offline bool) (string, error) {
	out, commandErr := util.RunCommandBytes(ctx, "", "go", 1<<20, "doc", ref.Name)
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

func cachedGoDocumentation(ctx context.Context, ref PackageRef) (string, error) {
	goPath := os.Getenv("GOPATH")
	if goPath == "" {
		goPath = filepath.Join(os.Getenv("HOME"), "go")
	}
	var failures []error
	for _, cacheRoot := range filepath.SplitList(goPath) {
		moduleDir := fmt.Sprintf("%s@%s", ref.Name, ref.Version)
		for _, candidate := range []string{"README.md", "readme.md", "README", "doc.go"} {
			data, err := readDocumentationFile(ctx, filepath.Join(cacheRoot, "pkg", "mod"), filepath.Join(moduleDir, candidate))
			if err == nil && len(data) > 0 {
				return string(data), nil
			}
			if err != nil {
				failures = append(failures, err)
			}
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

func harvestNodePackage(ctx context.Context, ref PackageRef, _ bool) (string, error) {
	data, err := readDocumentationFile(ctx, "node_modules", filepath.Join(ref.Name, "README.md"))
	if err != nil {
		return "", fmt.Errorf("npm documentation unavailable for %s: %w", ref.Name, err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("npm documentation is empty for %s", ref.Name)
	}
	return string(data), nil
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
