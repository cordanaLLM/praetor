// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

// toolchainTreeNames are the directories a build toolchain writes inside a checkout, next to the
// repository's own source. Zig 0.16 writes three: zig-pkg/ holds a copy of every package
// `zig build` or `zig fetch` fetched, each with its own build.zig and build.zig.zon;
// .zig-cache/ is the project-local cache; zig-out/ is the default install prefix
// (`zig build --help`: `--prefix`), where installed C headers land. A walker that enters them
// counts third-party packages and build output as the repository's own projects and sources, and
// spends its entry bound on them.
var toolchainTreeNames = map[string]struct{}{
	"zig-pkg":    {},
	"zig-out":    {},
	".zig-cache": {},
}

// IsToolchainTreeDir reports whether a directory base name is one of the toolchain trees every
// repository walker skips: the needs discovery walk, adopt's verification planner, the HISS
// scanner, dedupe's non-Git walker and the editor's language scan share this one list (HISS-19),
// beside their own dependency and build-output names. Matching is exact; a caller that folds case
// lowers the name first.
func IsToolchainTreeDir(name string) bool {
	_, ok := toolchainTreeNames[name]
	return ok
}
