// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/strictjson"
)

const (
	releaseOwnPublisherRule   = "release-own-publisher"
	maxReleasePackages        = 1024
	skipReleaseOwnNoConfig    = "[SKIP] " + releaseOwnPublisherRule + " not checked: no release-please configuration at the paths read."
	skipReleaseOwnNoPublisher = "[SKIP] " + releaseOwnPublisherRule + " not checked: no workflow under .github/workflows publishes a GitHub release."
)

// AuditOwnReleasePublisher requires release-please to leave release creation to an own publisher.
// Repositories without a release-please configuration or publishing workflow skip the check and say so.
func AuditOwnReleasePublisher(ctx context.Context, root string) (string, error) {
	if ctx == nil {
		return "", errors.New("[FAIL] release-own-publisher: the audit requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	configs, err := ownReleaseConfigs(ctx, root)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", releaseOwnPublisherRule, err)
	}
	if len(configs) == 0 {
		return skipReleaseOwnNoConfig, nil
	}
	publishers, err := forge.MeasureReleasePublishers(ctx, root)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", releaseOwnPublisherRule, err)
	}
	if len(publishers) == 0 {
		return skipReleaseOwnNoPublisher, nil
	}
	return judgeOwnReleaseConfigs(configs, publishers)
}

// ownReleaseConfigs reuses the release-e2e inventory and bounded, no-symlink snapshot reader.
// A manifest without a configuration still requires a configuration declaring the skip setting.
func ownReleaseConfigs(ctx context.Context, root string) (map[string][]byte, error) {
	configs := map[string][]byte{}
	manifest := ""
	for i := 0; i < len(releasePleaseConfigPaths); i++ {
		path := releasePleaseConfigPaths[i]
		data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(path))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if !exists {
			continue
		}
		if strings.HasSuffix(path, "manifest.json") {
			manifest = path
		} else {
			configs[path] = data
		}
	}
	if len(configs) == 0 && manifest != "" {
		configs[manifest] = nil
	}
	return configs, nil
}

// judgeOwnReleaseConfigs names each configuration and all publishing workflows in its verdict.
func judgeOwnReleaseConfigs(configs map[string][]byte, publishers []string) (string, error) {
	names := slices.Sorted(maps.Keys(configs))
	var lines []string
	for i := 0; i < len(names) && i < len(releasePleaseConfigPaths); i++ {
		name := names[i]
		if err := releasePackagesSkipGitHub(configs[name]); err != nil {
			fixPath := strings.Replace(name, ".release-please-manifest.json", "release-please-config.json", 1)
			return "", fmt.Errorf("[FAIL] %s: %s: %w; publishing workflow(s): %s; set \"skip-github-release\": true at the top level or on every package in %s (including package overrides)",
				releaseOwnPublisherRule, name, err, strings.Join(publishers, ", "), fixPath)
		}
		lines = append(lines, fmt.Sprintf("[PASS] %s: %s: every package resolves skip-github-release to true; publishing workflow(s): %s.",
			releaseOwnPublisherRule, name, strings.Join(publishers, ", ")))
	}
	return strings.Join(lines, "\n"), nil
}

// releasePackagesSkipGitHub resolves package settings before top-level defaults, as release-please does.
func releasePackagesSkipGitHub(data []byte) error {
	var config map[string]any
	if len(data) == 0 {
		return errors.New("release-please configuration missing; a version manifest cannot declare skip-github-release")
	}
	if err := strictjson.Decode(data, &config, strictjson.Options{
		MaxBytes: contextopt.MaxSourceBytes, MaxDepth: 64, Names: strictjson.ExactNames,
	}); err != nil {
		return fmt.Errorf("read release-please configuration: %w", err)
	}
	defaultSkip, err := releaseSkipValue(config, false)
	if err != nil {
		return err
	}
	if _, exists := config["packages"]; !exists {
		return requireReleaseSkip(config, defaultSkip, ".")
	}
	packages, ok := config["packages"].(map[string]any)
	if !ok || len(packages) == 0 || len(packages) > maxReleasePackages {
		return fmt.Errorf("packages must be an object with 1..%d entries", maxReleasePackages)
	}
	return releasePackageSkips(packages, defaultSkip)
}

// releasePackageSkips checks every package in stable order within the validated package bound.
func releasePackageSkips(packages map[string]any, defaultSkip bool) error {
	names := slices.Sorted(maps.Keys(packages))
	for i := 0; i < len(names) && i < maxReleasePackages; i++ {
		pkg, ok := packages[names[i]].(map[string]any)
		if !ok {
			return fmt.Errorf("package %q must be an object", names[i])
		}
		if err := requireReleaseSkip(pkg, defaultSkip, names[i]); err != nil {
			return err
		}
	}
	return nil
}

// requireReleaseSkip reports the first package whose effective setting permits release creation.
func requireReleaseSkip(pkg map[string]any, inherited bool, name string) error {
	skip, err := releaseSkipValue(pkg, inherited)
	if err != nil {
		return fmt.Errorf("package %q: %w", name, err)
	}
	if !skip {
		return fmt.Errorf("package %q does not resolve skip-github-release to true", name)
	}
	return nil
}

// releaseSkipValue implements release-please's nullish inheritance while refusing non-booleans.
func releaseSkipValue(config map[string]any, inherited bool) (bool, error) {
	value := config["skip-github-release"]
	if value == nil {
		return inherited, nil
	}
	skip, ok := value.(bool)
	if !ok {
		return false, errors.New("skip-github-release must be a boolean")
	}
	return skip, nil
}
