// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// judgeOwnReleaseConfigs names each configuration and all publishing workflows in its verdict.
func judgeOwnReleaseConfigs(configs map[string][]byte, publishers []string) (string, error) {
	names := slices.Sorted(maps.Keys(configs))
	var lines []string
	for i := 0; i < len(names) && i < len(releasePleaseConfigPaths); i++ {
		name := names[i]
		form, err := resolveOwnReleasePackages(configs[name])
		if err != nil {
			fixPath := strings.Replace(name, ".release-please-manifest.json", "release-please-config.json", 1)
			return "", fmt.Errorf("[FAIL] %s: %s: %w; publishing workflow(s): %s; set \"skip-github-release\": true at the top level or on every package in %s (including package overrides)",
				releaseOwnPublisherRule, name, err, strings.Join(publishers, ", "), fixPath)
		}
		lines = append(lines, fmt.Sprintf("[PASS] %s: %s: every package hands release creation to the own publisher (%s); publishing workflow(s): %s.",
			releaseOwnPublisherRule, name, form, strings.Join(publishers, ", ")))
	}
	return strings.Join(lines, "\n"), nil
}
