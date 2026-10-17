// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"fmt"

	"github.com/cordanaLLM/praetor/internal/util"
)

type cargoManifest struct {
	hasPackage        bool
	edition           string
	inherits          bool
	workspaceEdition  string
	members, exclude  []string
	invalid           string
	warnings          string
	lintsInherit      bool
	workspaceWarnings string
}

type cargoManifestDocument struct {
	Package   *cargoPackageDocument   `toml:"package"`
	Workspace *cargoWorkspaceDocument `toml:"workspace"`
	Lints     cargoLintsDocument      `toml:"lints"`
}

type cargoPackageDocument struct {
	Edition any `toml:"edition"`
}

type cargoWorkspaceDocument struct {
	Members any                  `toml:"members"`
	Exclude any                  `toml:"exclude"`
	Package cargoPackageDocument `toml:"package"`
	Lints   cargoLintsDocument   `toml:"lints"`
}

type cargoLintsDocument struct {
	Workspace any `toml:"workspace"`
	Rust      struct {
		Warnings any `toml:"warnings"`
	} `toml:"rust"`
}

// parseCargoManifest reads the package, workspace and lints values from a Cargo.toml.
func parseCargoManifest(name string, data []byte) (cargoManifest, error) {
	var document cargoManifestDocument
	if err := util.DecodeTOML(name, data, &document); err != nil {
		return cargoManifest{}, err
	}
	manifest := cargoManifest{hasPackage: document.Package != nil}
	if document.Package != nil {
		manifest.edition, manifest.inherits = cargoEdition(document.Package.Edition)
	}
	manifest.warnings = cargoLintLevel(document.Lints.Rust.Warnings)
	manifest.lintsInherit = cargoBool(document.Lints.Workspace)
	if workspace := document.Workspace; workspace != nil {
		manifest.workspaceEdition, _ = cargoEdition(workspace.Package.Edition)
		manifest.workspaceWarnings = cargoLintLevel(workspace.Lints.Rust.Warnings)
		manifest.members, manifest.invalid = cargoStringList(workspace.Members, "workspace.members", name)
		if manifest.invalid == "" {
			manifest.exclude, manifest.invalid = cargoStringList(workspace.Exclude, "workspace.exclude", name)
		}
	}
	return manifest, nil
}

// cargoStringList reads a members or exclude value, retaining a useful shape error.
func cargoStringList(value any, key, name string) ([]string, string) {
	if value == nil {
		return nil, ""
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Sprintf("%s in %s is not an array of plain strings", key, name)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Sprintf("%s in %s is not an array of plain strings", key, name)
		}
		result = append(result, text)
	}
	return result, ""
}

// cargoEdition extracts a literal edition or Cargo's workspace inheritance table.
func cargoEdition(value any) (string, bool) {
	if edition, ok := value.(string); ok && rustEdition.MatchString(edition) {
		return edition, false
	}
	fields, ok := value.(map[string]any)
	return "", ok && cargoBool(fields["workspace"])
}

// cargoLintLevel reads a lint level string or the level field of an inline table.
func cargoLintLevel(value any) string {
	if level, ok := value.(string); ok {
		return level
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	level, isString := fields["level"].(string)
	if !isString {
		return ""
	}
	return level
}

// cargoBool reports whether value is the boolean true.
func cargoBool(value any) bool {
	result, isBool := value.(bool)
	return isBool && result
}
