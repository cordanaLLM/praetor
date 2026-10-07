// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// CargoWarningsLints returns the level the [lints] tables give the warnings lint group in every
// crate of the root Cargo.toml's workspace (workspaceCrates): a crate's own [lints.rust]
// warnings, or with [lints] workspace = true the [workspace.lints.rust] warnings of the root.
// Level is "forbid" when every crate forbids warnings, "deny" when every crate denies or forbids
// them, and "" when a crate gives them neither, no crate is declared, or a manifest cannot be
// read; Detail says which. The build-warnings gate accepts a cargo lane run in the repository's
// root with such a level: cargo passes it to rustc as --deny=warnings before the flags of
// RUSTFLAGS and its other sources (measured with cargo 1.98.1: [lints.rust] warnings = "deny"
// fails a build with an unused variable, RUSTFLAGS="-W warnings" undoes it, and a member without
// [lints] workspace = true does not inherit [workspace.lints]).
func CargoWarningsLints(ctx context.Context, repoPath string) forge.CargoLints {
	data, err := util.ReadConfinedLimited(repoPath, "Cargo.toml", maxSettingBytes)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return forge.CargoLints{}
	case err != nil:
		return forge.CargoLints{Detail: fmt.Sprintf("the root Cargo.toml cannot be read for its [lints] (%v)", err)}
	}
	root := parseCargoManifest(string(data))
	crates, problem := workspaceCrates(ctx, repoPath, root)
	switch {
	case problem != "":
		return forge.CargoLints{Detail: "the [lints] of the workspace's crates cannot be read: " + problem}
	case len(crates) == 0:
		return forge.CargoLints{Detail: "the root Cargo.toml declares no crate whose [lints] could deny warnings"}
	}
	level := "forbid"
	for _, crate := range crates {
		switch crateWarnings(crate.manifest, root.workspaceWarnings) {
		case "forbid":
		case "deny":
			level = "deny"
		default:
			return forge.CargoLints{Detail: crate.name + " does not deny warnings in [lints.rust] " +
				"(or through [lints] workspace = true and [workspace.lints.rust])"}
		}
	}
	return forge.CargoLints{Level: level, Detail: "every crate of the root Cargo.toml " + lintVerbs[level] + " warnings in [lints]"}
}

// lintVerbs names what a level does to the warnings lint group.
var lintVerbs = map[string]string{"deny": "denies", "forbid": "forbids"}

// crateWarnings returns the level one crate's manifest gives the warnings lint group: its own, or
// workspaceWarnings when it inherits the workspace's lints.
func crateWarnings(manifest cargoManifest, workspaceWarnings string) string {
	if manifest.lintsInherit {
		return workspaceWarnings
	}
	return manifest.warnings
}
