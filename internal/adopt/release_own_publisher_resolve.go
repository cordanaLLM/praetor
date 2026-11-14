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

// releaseSettings holds the effective release-please boolean options.
type releaseSettings struct {
	skip, draft, force bool
}

// releasePackagesSkipGitHub checks whether all packages hand publication to the own publisher.
func releasePackagesSkipGitHub(data []byte) error {
	_, err := resolveOwnReleasePackages(data)
	return err
}

// resolveOwnReleasePackages validates package settings and returns the accepted forms used.
func resolveOwnReleasePackages(data []byte) (string, error) {
	var config map[string]any
	if len(data) == 0 {
		return "", errors.New("release-please configuration missing; a version manifest cannot declare skip-github-release")
	}
	if err := strictjson.Decode(data, &config, strictjson.Options{
		MaxBytes: contextopt.MaxSourceBytes, MaxDepth: 64, Names: strictjson.ExactNames,
	}); err != nil {
		return "", fmt.Errorf("read release-please configuration: %w", err)
	}
	defaults, err := resolveReleaseSettings(config, releaseSettings{})
	if err != nil {
		return "", err
	}
	if _, exists := config["packages"]; !exists {
		return releasePackageForms(map[string]any{".": config}, defaults)
	}
	packages, ok := config["packages"].(map[string]any)
	if !ok || len(packages) == 0 || len(packages) > maxReleasePackages {
		return "", fmt.Errorf("packages must be an object with 1..%d entries", maxReleasePackages)
	}
	return releasePackageForms(packages, defaults)
}

// releasePackageForms checks every package in stable order and describes its accepted forms.
func releasePackageForms(packages map[string]any, defaults releaseSettings) (string, error) {
	var skipUsed, draftUsed bool
	names := slices.Sorted(maps.Keys(packages))
	for i := 0; i < len(names) && i < maxReleasePackages; i++ {
		settings, err := requireOwnReleasePackage(packages[names[i]], defaults, names[i])
		if err != nil {
			return "", err
		}
		skipUsed = skipUsed || settings.skip
		draftUsed = draftUsed || (settings.draft && settings.force)
	}
	if skipUsed && draftUsed {
		return "skip-github-release and draft with force-tag-creation", nil
	}
	if skipUsed {
		return "skip-github-release", nil
	}
	return "draft with force-tag-creation", nil
}

// requireOwnReleasePackage validates one package and its effective publishing settings.
func requireOwnReleasePackage(value any, defaults releaseSettings, name string) (releaseSettings, error) {
	pkg, ok := value.(map[string]any)
	if !ok {
		return releaseSettings{}, fmt.Errorf("package %q must be an object", name)
	}
	settings, err := resolveReleaseSettings(pkg, defaults)
	if err != nil {
		return settings, fmt.Errorf("package %q: %w", name, err)
	}
	if !settings.skip && (!settings.draft || !settings.force) {
		if settings.draft {
			return settings, fmt.Errorf("package %q resolves draft true but force-tag-creation not true", name)
		}
		return settings, fmt.Errorf("package %q resolves neither skip-github-release nor draft with force-tag-creation to true", name)
	}
	return settings, nil
}

// resolveReleaseSettings resolves all options, including those unnecessary for the verdict.
func resolveReleaseSettings(config map[string]any, inherited releaseSettings) (releaseSettings, error) {
	var result releaseSettings
	var err error
	result.skip, err = releaseBooleanValue(config, "skip-github-release", inherited.skip)
	if err != nil {
		return result, err
	}
	result.draft, err = releaseBooleanValue(config, "draft", inherited.draft)
	if err != nil {
		return result, err
	}
	result.force, err = releaseBooleanValue(config, "force-tag-creation", inherited.force)
	return result, err
}

// releaseBooleanValue implements nullish inheritance while refusing non-booleans.
func releaseBooleanValue(config map[string]any, key string, inherited bool) (bool, error) {
	value := config[key]
	if value == nil {
		return inherited, nil
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return result, nil
}
