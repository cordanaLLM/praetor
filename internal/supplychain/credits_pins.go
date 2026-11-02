// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/nodemanifest"
)

var semverPattern = regexp.MustCompile(`\b\d+\.\d+(?:\.\d+)?(?:-[0-9A-Za-z.-]+)?\b`)

// CheckCreditPins checks that every pinned package in manifests named by a credit entry
// agrees with the version recorded in docs/credits.yaml (detail).
func CheckCreditPins(credits Credits, manifests map[string][]byte) error {
	var findings []error
	for _, entry := range credits.Entries {
		if entry.Detail == "" {
			continue
		}
		npmPkgs := entryNPMPackages(entry.Packages)
		if len(npmPkgs) == 0 {
			continue
		}
		for _, rel := range entry.Paths {
			if !isManifestPath(rel) {
				continue
			}
			data, ok := manifests[rel]
			if !ok {
				continue
			}
			manifest, err := nodemanifest.ParseManifest(data)
			if err != nil {
				findings = append(findings, fmt.Errorf("parse %s: %w", rel, err))
				continue
			}
			pinned := manifestPinnedDeps(manifest)
			for _, pkg := range npmPkgs {
				pinVersion, hasPin := pinned[pkg]
				if !hasPin {
					continue
				}
				cleanPin := strings.TrimLeft(pinVersion, "^~=v")
				recorded := recordedPackageVersion(entry.Detail, pkg, entry.Name, len(npmPkgs))
				if recorded != "" && recorded != cleanPin {
					findings = append(findings, fmt.Errorf("%s: %s pins version %s, but %s records %s",
						rel, pkg, cleanPin, AcknowledgementsList, recorded))
				}
			}
		}
	}
	return errors.Join(findings...)
}

// ReadCreditManifests reads every package.json path named by the credits list below root.
func ReadCreditManifests(ctx context.Context, root string, credits Credits) (map[string][]byte, error) {
	manifests := make(map[string][]byte)
	for _, entry := range credits.Entries {
		for _, rel := range entry.Paths {
			if !isManifestPath(rel) || manifests[rel] != nil {
				continue
			}
			data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, rel)
			if err != nil {
				return nil, fmt.Errorf("read %s, which %s names: %w", rel, AcknowledgementsList, err)
			}
			if exists {
				manifests[rel] = data
			}
		}
	}
	return manifests, nil
}

// isManifestPath reports whether rel is a non-third-party package.json file path.
func isManifestPath(rel string) bool {
	return strings.HasSuffix(rel, "package.json") &&
		!strings.HasPrefix(rel, "third_party/") &&
		!strings.Contains(rel, "/third_party/")
}

// entryNPMPackages extracts package names from entries with an npm prefix.
func entryNPMPackages(packages []string) []string {
	var npmPkgs []string
	for _, pkg := range packages {
		if strings.HasPrefix(pkg, "npm:") {
			npmPkgs = append(npmPkgs, strings.TrimPrefix(pkg, "npm:"))
		}
	}
	return npmPkgs
}

// manifestPinnedDeps returns a flattened map of package names to pinned versions across all manifest dependency groups.
func manifestPinnedDeps(manifest nodemanifest.Manifest) map[string]string {
	deps := make(map[string]string)
	for _, group := range manifest.Groups() {
		for pkg, ver := range group {
			deps[pkg] = ver
		}
	}
	return deps
}

// recordedPackageVersion resolves the expected package version string from a credits detail field.
func recordedPackageVersion(detail, pkg, entryName string, totalPkgs int) string {
	if detail == "" {
		return ""
	}
	allVersions := semverPattern.FindAllString(detail, -1)
	if len(allVersions) == 0 {
		return ""
	}
	if v := namedVersion(detail, pkg); v != "" {
		return v
	}
	unscoped := pkg
	if slash := strings.LastIndex(pkg, "/"); slash >= 0 {
		unscoped = pkg[slash+1:]
		if v := namedVersion(detail, unscoped); v != "" {
			return v
		}
	}
	if strings.EqualFold(pkg, entryName) || strings.EqualFold(unscoped, entryName) {
		leading := semverPattern.FindString(strings.TrimSpace(detail))
		if strings.HasPrefix(strings.TrimSpace(detail), leading) && leading != "" {
			return leading
		}
	}
	if totalPkgs == 1 || allVersionsEqual(allVersions) {
		return allVersions[0]
	}
	return ""
}

// namedVersion extracts a version string explicitly associated with a given package name from detail text.
func namedVersion(detail, name string) string {
	pattern := `(?:^|[\s,;(` + "`" + `])` + regexp.QuoteMeta(name) + `[\s@:]+(\d+\.\d+(?:\.\d+)?(?:-[0-9A-Za-z.-]+)?)`
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(detail)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// allVersionsEqual reports whether all version strings in the slice are identical.
func allVersionsEqual(versions []string) bool {
	for i := 1; i < len(versions); i++ {
		if versions[i] != versions[0] {
			return false
		}
	}
	return true
}
