// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/semver"
)

// VerifyPluginVersion validates the shipped manifest without consulting Git or the running
// binary. A nonempty expected version additionally binds it to the release tag's version.
// Repositories without a plugin have nothing to verify.
func VerifyPluginVersion(ctx context.Context, rootDir, expected string) error {
	if expected == "" && !shipsPlugin(rootDir) {
		return nil
	}
	_, version, err := readPluginVersion(ctx, rootDir)
	if err != nil {
		return err
	}
	if expected != "" && version != expected {
		return fmt.Errorf("%s: version %q differs from release version %q", PluginManifestRel, version, expected)
	}
	return nil
}

// readPluginVersion reads through the compiler's confined, bounded reader and shares the
// repository's SemVer parser. The marketplace requires a version without a Git tag prefix.
func readPluginVersion(ctx context.Context, rootDir string) (map[string]jsontext.Value, string, error) {
	data, err := readConfinedText(ctx, rootDir, PluginManifestRel)
	if err != nil {
		return nil, "", err
	}
	var manifest map[string]jsontext.Value
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, "", fmt.Errorf("%s: %w", PluginManifestRel, err)
	}
	var version string
	if err := json.Unmarshal(manifest["version"], &version); err != nil {
		return nil, "", fmt.Errorf("%s: version required as a SemVer string: %w", PluginManifestRel, err)
	}
	if _, ok := semver.Parse(version); !ok || strings.TrimSpace(version) != version || strings.HasPrefix(version, "v") {
		return nil, "", fmt.Errorf("%s: malformed SemVer version %q", PluginManifestRel, version)
	}
	return manifest, version, nil
}

// StampPluginReleaseVersion takes its version from GoReleaser's tag-derived .Version, the
// same source as main.version. It accepts the matching development line or an exact rerun;
// a different release line fails before any write. Unrelated manifest fields survive.
func StampPluginReleaseVersion(ctx context.Context, rootDir, releaseVersion string) error {
	manifest, current, err := readPluginVersion(ctx, rootDir)
	if err != nil {
		return err
	}
	parsed, ok := semver.Parse(releaseVersion)
	if !ok || strings.TrimSpace(releaseVersion) != releaseVersion || strings.HasPrefix(releaseVersion, "v") {
		return fmt.Errorf("malformed release SemVer version %q", releaseVersion)
	}
	development := fmt.Sprintf("%d.%d.%d-dev", parsed.Major, parsed.Minor, parsed.Patch)
	if current != releaseVersion && current != development {
		return fmt.Errorf("%s: version %q disagrees with release %q (expected %s)", PluginManifestRel, current, releaseVersion, development)
	}
	version, err := json.Marshal(releaseVersion)
	if err != nil {
		return fmt.Errorf("encode plugin release version: %w", err)
	}
	manifest["version"] = version
	data, err := json.Marshal(manifest, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return fmt.Errorf("encode plugin manifest: %w", err)
	}
	if err := writeConfinedText(ctx, rootDir, PluginManifestRel, append(data, '\n')); err != nil {
		return fmt.Errorf("write plugin manifest: %w", err)
	}
	return VerifyPluginVersion(ctx, rootDir, releaseVersion)
}
