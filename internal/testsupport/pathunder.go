// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// PathUnder reports whether path lies at or below root once both are resolved through their
// deepest existing ancestor (util.ResolveExistingAncestor), and the relative path it compared.
// Resolving only what exists is what keeps a path that is not created yet comparable on a host
// whose temporary directory is reached through a symlink, as macOS's /var is through
// /private/var: resolving the root alone, and falling back to the unresolved path for the
// rest, compares two spellings of one place.
func PathUnder(root, path string) (string, bool, error) {
	resolvedRoot, err := util.ResolveExistingAncestor(root)
	if err != nil {
		return "", false, err
	}
	resolvedPath, err := util.ResolveExistingAncestor(path)
	if err != nil {
		return "", false, err
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil {
		return "", false, err
	}
	return rel, rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}
