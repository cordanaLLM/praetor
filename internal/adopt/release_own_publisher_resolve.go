// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/strictjson"
)

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
