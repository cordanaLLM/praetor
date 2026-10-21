// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"fmt"
	"strings"
)

// MaxRacePackages bounds the explicit pre-push package handoff and its signed representation.
const MaxRacePackages = 20000

// ParseRacePackages parses the explicit gate scope: none, ./..., or exact import paths.
// A nil slice is reserved for an omitted flag; an empty nonnil slice explicitly runs no Go tests.
func ParseRacePackages(raw string) ([]string, error) {
	if raw == "none" {
		return []string{}, nil
	}
	if raw == "./..." {
		return []string{raw}, nil
	}
	if raw == "" || len(raw) > 1<<20 {
		return nil, errors.New("race packages must be none, ./..., or a bounded comma-separated import-path list")
	}
	packages := strings.Split(raw, ",")
	if len(packages) > MaxRacePackages {
		return nil, errors.New("race packages exceed 20000 entries")
	}
	seen := make(map[string]bool, len(packages))
	for i := 0; i < len(packages) && i < MaxRacePackages; i++ {
		pkg := packages[i]
		if !validRacePackage(pkg) || seen[pkg] {
			return nil, fmt.Errorf("invalid or duplicate race package %q", pkg)
		}
		seen[pkg] = true
	}
	return packages, nil
}

func validRacePackage(pkg string) bool {
	if pkg == "none" || pkg == "" || strings.HasPrefix(pkg, "/") || strings.HasPrefix(pkg, "-") {
		return false
	}
	parts := strings.Split(pkg, "/")
	for i := 0; i < len(parts) && i < 4096; i++ {
		if invalidRaceSegment(parts[i]) {
			return false
		}
	}
	return len(parts) <= 4096 && strings.IndexFunc(pkg, invalidRacePackageRune) < 0
}

func invalidRaceSegment(segment string) bool {
	return segment == "" || segment == "." || segment == ".." || strings.Contains(segment, "...")
}

func invalidRacePackageRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	default:
		return !strings.ContainsRune("-._~/", r)
	}
}

// FormatRacePackages is the canonical signed scope, including an explicit empty selection.
func FormatRacePackages(packages []string) string {
	if len(packages) == 0 {
		return "none"
	}
	return strings.Join(packages, ",")
}
