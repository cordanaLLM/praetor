// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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

	switch ref.Kind {
	case KindGoModule:
		return harvestGoModule(ctx, ref, offline)
	case KindGitHubAction:
		return harvestGitHubAction(ctx, ref, offline)
	case KindNodePackage:
		return harvestNodePackage(ctx, ref, offline)
	default:
		return fmt.Sprintf("# %s\nVersion: %s\nManifest: %s\n", ref.Name, ref.Version, ref.Manifest), nil
	}
}

func harvestGoModule(ctx context.Context, ref PackageRef, offline bool) (string, error) {
	// 1. Try local `go doc` first (authoritative, instant, offline)
	cmd := exec.CommandContext(ctx, "go", "doc", ref.Name)
	out, err := cmd.Output()
	if err == nil && len(out) > 50 {
		return string(out), nil
	}

	// 2. Check local Go module cache
	goPath := os.Getenv("GOPATH")
	if goPath == "" {
		goPath = filepath.Join(os.Getenv("HOME"), "go")
	}
	modCacheDir := filepath.Join(goPath, "pkg", "mod", fmt.Sprintf("%s@%s", ref.Name, ref.Version))
	readmeCandidates := []string{"README.md", "readme.md", "README", "doc.go"}
	for _, candidate := range readmeCandidates {
		target := filepath.Join(modCacheDir, candidate)
		if util.FileExists(target) {
			data, readErr := os.ReadFile(target)
			if readErr == nil && len(data) > 0 {
				return string(data), nil
			}
		}
	}

	// 3. If online and on github.com, fetch raw README from GitHub
	if !offline && strings.HasPrefix(ref.Name, "github.com/") {
		parts := strings.Split(ref.Name, "/")
		if len(parts) >= 3 {
			owner := parts[1]
			repo := parts[2]
			remoteURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/README.md", owner, repo, ref.Version)
			content, netErr := fetchURLWithTimeout(ctx, remoteURL)
			if netErr == nil && len(content) > 0 {
				return content, nil
			}
		}
	}

	// Fallback synthesized stub
	return fmt.Sprintf("# %s\n\nGo Module: `%s`\nVersion: `%s`\nDirect: %t\n", ref.Name, ref.Name, ref.Version, ref.Direct), nil
}

func harvestGitHubAction(ctx context.Context, ref PackageRef, offline bool) (string, error) {
	parts := strings.Split(ref.Name, "/")
	if len(parts) < 2 {
		return fmt.Sprintf("# Action: %s\nVersion: %s\n", ref.Name, ref.Version), nil
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
		if len(combined) > 50 {
			return combined, nil
		}
	}

	return fmt.Sprintf("# GitHub Action: %s@%s\nRepository: https://github.com/%s/%s\n", ref.Name, ref.Version, owner, repo), nil
}

func harvestNodePackage(ctx context.Context, ref PackageRef, offline bool) (string, error) {
	// Look in node_modules if present
	localReadme := filepath.Join("node_modules", ref.Name, "README.md")
	if util.FileExists(localReadme) {
		data, err := os.ReadFile(localReadme)
		if err == nil && len(data) > 0 {
			return string(data), nil
		}
	}

	return fmt.Sprintf("# npm Package: %s\nVersion: %s\n", ref.Name, ref.Version), nil
}

func fetchURLWithTimeout(ctx context.Context, url string) (string, error) {
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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http status %d", resp.StatusCode)
	}

	lr := io.LimitReader(resp.Body, 256*1024) // Cap at 256KB
	body, err := io.ReadAll(lr)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
