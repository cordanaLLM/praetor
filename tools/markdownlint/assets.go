// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package markdownlint exposes the locked documentation-gate assets emitted by adoption.
package markdownlint

import (
	"embed"
	"fmt"
)

const (
	// Directory is the repository-relative home of the documentation gate.
	Directory = "tools/markdownlint"
	// WorkflowFile is the repository-relative hosted documentation gate.
	WorkflowFile = ".github/workflows/praetor-docs.yml"
	// StatusContext is the exact required check emitted by WorkflowFile.
	StatusContext = "Documentation Governance"
	// MaxAssets bounds all asset iteration.
	MaxAssets = 5
)

var assetNames = [...]string{
	"package.json",
	"package-lock.json",
	"markdownlint-cli2.yaml",
	"verify.mjs",
	"no-private-scratch-links.mjs",
}

//go:embed package.json package-lock.json markdownlint-cli2.yaml verify.mjs no-private-scratch-links.mjs
var assets embed.FS

// Names returns the complete deterministic asset inventory.
func Names() []string {
	names := make([]string, len(assetNames))
	copy(names, assetNames[:])
	return names
}

// Read returns one canonical asset without exposing mutable embedded storage.
func Read(name string) ([]byte, error) {
	known := false
	for index := 0; index < len(assetNames) && index < MaxAssets; index++ {
		known = known || assetNames[index] == name
	}
	if !known {
		return nil, fmt.Errorf("unknown markdownlint asset %q", name)
	}
	data, err := assets.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read embedded markdownlint asset %q: %w", name, err)
	}
	return append([]byte(nil), data...), nil
}
