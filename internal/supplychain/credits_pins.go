// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"errors"
	"fmt"
	"path"
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
		findings = append(findings, checkEntryCreditPins(entry, manifests)...)
	}
	return errors.Join(findings...)
}

// checkEntryCreditPins checks that manifest pins for a single credit entry agree with docs/credits.yaml.
func checkEntryCreditPins(entry CreditEntry, manifests map[string][]byte) []error {
	if entry.Detail == "" {
		return nil
	}
	npmPkgs := entryNPMPackages(entry.Packages)
	if len(npmPkgs) == 0 {
		return nil
	}
	var findings []error
	for _, rel := range entry.Paths {
		if !isManifestPath(rel) {
			continue
		}
		data, ok := manifests[rel]
		if !ok {
			continue
		}
		findings = append(findings, checkManifestPins(entry, rel, data, npmPkgs)...)
	}
	return findings
}

// checkManifestPins checks pinned package versions in a parsed manifest against the credit entry detail.
func checkManifestPins(entry CreditEntry, rel string, data []byte, npmPkgs []string) []error {
	manifest, err := nodemanifest.ParseManifest(data)
	if err != nil {
		return []error{fmt.Errorf("parse %s: %w", rel, err)}
	}
	var findings []error
	for _, group := range manifest.Groups() {
		for _, pkg := range npmPkgs {
			pinVersion, hasPin := group[pkg]
			if !hasPin {
				continue
			}
			if err := checkPackagePin(entry, rel, pkg, pinVersion, len(npmPkgs)); err != nil {
				findings = append(findings, err)
			}
		}
	}
	return findings
}

// checkPackagePin verifies that a single package pin in a manifest matches the credit entry record.
func checkPackagePin(entry CreditEntry, rel, pkg, pinVersion string, totalPkgs int) error {
	cleanPin := strings.TrimLeft(pinVersion, "^~=v")
	recorded := recordedPackageVersion(entry.Detail, pkg, entry.Name, totalPkgs)
	if recorded == "" && semverPattern.MatchString(entry.Detail) {
		return fmt.Errorf("%s: ambiguous version association for %s in %s detail",
			rel, pkg, AcknowledgementsList)
	}
	if recorded != "" && recorded != cleanPin {
		return fmt.Errorf("%s: %s pins version %s, but %s records %s",
			rel, pkg, cleanPin, AcknowledgementsList, recorded)
	}
	return nil
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
	return path.Base(rel) == "package.json" &&
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

// recordedPackageVersion resolves the expected package version string from a credits detail field.
func recordedPackageVersion(detail, pkg, entryName string, totalPkgs int) string {
	if detail == "" {
		return ""
	}
	allVersions := semverPattern.FindAllString(detail, -1)
	if len(allVersions) == 0 {
		return ""
	}
	if v := packageNamedVersion(detail, pkg); v != "" {
		return v
	}
	if v := entryLeadingVersion(detail, pkg, entryName); v != "" {
		return v
	}
	if totalPkgs == 1 || allVersionsEqual(allVersions) {
		return allVersions[0]
	}
	return ""
}

// packageNamedVersion extracts a version named by pkg or unscoped pkg from detail text.
func packageNamedVersion(detail, pkg string) string {
	if v := namedVersion(detail, pkg); v != "" {
		return v
	}
	unscoped := unscopedPackage(pkg)
	if unscoped != pkg {
		return namedVersion(detail, unscoped)
	}
	return ""
}

// entryLeadingVersion extracts a leading version when the package matches the credit entry name.
func entryLeadingVersion(detail, pkg, entryName string) string {
	if !matchesEntryName(pkg, entryName) {
		return ""
	}
	trimmed := strings.TrimSpace(detail)
	leading := semverPattern.FindString(trimmed)
	if leading != "" && strings.HasPrefix(trimmed, leading) {
		return leading
	}
	return ""
}

// matchesEntryName reports whether the package or its unscoped name equals the credit entry name.
func matchesEntryName(pkg, entryName string) bool {
	return strings.EqualFold(pkg, entryName) || strings.EqualFold(unscopedPackage(pkg), entryName)
}

// unscopedPackage returns the package name without its scope prefix.
func unscopedPackage(pkg string) string {
	if slash := strings.LastIndex(pkg, "/"); slash >= 0 {
		return pkg[slash+1:]
	}
	return pkg
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
