// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"fmt"

	"github.com/cordanaLLM/praetor/internal/util"
)

type cargoManifest struct {
	// hasPackage reports a [package] table: the manifest describes a crate.
	hasPackage bool
	// hasLibraryTarget and hasBinaryTarget record explicit lib and bin target tables.
	hasLibraryTarget bool
	hasBinaryTarget  bool
	// unpublished records a [package] that is never published (publish = false or []), and
	// publishInherits one that takes publish from [workspace.package] (publish.workspace = true).
	unpublished, publishInherits bool
	// workspaceUnpublished records publish = false or [] in [workspace.package].
	workspaceUnpublished bool
	// edition is the well-formed edition (rustEdition) [package] declares, "" for none.
	edition string
	// inherits reports that [package] takes the edition [workspace.package] declares, as
	// edition.workspace = true or edition = { workspace = true }.
	inherits bool
	// workspaceEdition is the well-formed edition [workspace.package] declares, "" for none.
	workspaceEdition string
	// members and exclude are the path lists [workspace] declares.
	members, exclude []string
	// invalid says why a members or exclude value could not be read, "" when both could.
	invalid string
	// warnings is the level [lints.rust] gives the warnings lint group, "" for none, and
	// lintsInherit reports [lints] workspace = true, which takes [workspace.lints] instead.
	warnings     string
	lintsInherit bool
	// workspaceWarnings is the level [workspace.lints.rust] gives the warnings lint group.
	workspaceWarnings string
}

type cargoManifestDocument struct {
	Package   *cargoPackageDocument   `toml:"package"`
	Workspace *cargoWorkspaceDocument `toml:"workspace"`
	Lints     cargoLintsDocument      `toml:"lints"`
	Lib       any                     `toml:"lib"`
	Bin       any                     `toml:"bin"`
}

type cargoPackageDocument struct {
	Edition any `toml:"edition"`
	Publish any `toml:"publish"`
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
	manifest := cargoManifest{
		hasPackage: document.Package != nil, hasLibraryTarget: document.Lib != nil,
		hasBinaryTarget: document.Bin != nil,
	}
	if document.Package != nil {
		manifest.edition, manifest.inherits = cargoEdition(document.Package.Edition)
		manifest.unpublished, manifest.publishInherits = cargoPublish(document.Package.Publish)
	}
	manifest.warnings = cargoLintLevel(document.Lints.Rust.Warnings)
	manifest.lintsInherit = cargoBool(document.Lints.Workspace)
	if workspace := document.Workspace; workspace != nil {
		manifest.workspaceEdition, _ = cargoEdition(workspace.Package.Edition)
		manifest.workspaceUnpublished, _ = cargoPublish(workspace.Package.Publish)
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

// cargoPublish reads a publish value: whether it forbids publishing (false, or an empty registry
// list), and whether it is Cargo's workspace inheritance table. A missing value publishes.
func cargoPublish(value any) (unpublished, inherits bool) {
	switch publish := value.(type) {
	case bool:
		return !publish, false
	case []any:
		return len(publish) == 0, false
	case map[string]any:
		return false, cargoBool(publish["workspace"])
	default:
		return false, false
	}
}

// unpublishedIn reports whether the crate is never published, by its own publish value or by the
// one it inherits from workspace's [workspace.package].
func (m cargoManifest) unpublishedIn(workspace cargoManifest) bool {
	return m.unpublished || (m.publishInherits && workspace.workspaceUnpublished)
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
