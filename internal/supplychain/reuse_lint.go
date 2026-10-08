// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The REUSE lint gate runs one reuse release line everywhere: praetor's own Compliance workflow
// (.github/workflows/compliance.yml), the workflow and the pre-commit job adoption emits into a
// repository that declares its licensing the REUSE way (internal/adopt, reuse_gate.go and
// hooks.go), and the release bump compares workflows against (internal/bump, knownActionLatest).
// The pin is ReuseActionVersion alone: fsfe/reuse-action at that tag builds FROM
// fsfe/reuse:<major>, so its major is the reuse major the emitted hook requires locally.

const (
	// ReuseAction is the action that runs reuse lint in a workflow.
	ReuseAction = "fsfe/reuse-action"
	// ReuseActionVersion is the one tag every emitted or own workflow runs ReuseAction at.
	ReuseActionVersion = "v6"
	// ReuseActionCommit is the full commit SHA corresponding to ReuseActionVersion.
	ReuseActionCommit = "676e2d560c9a403aa252096d99fcab3e1132b0f5"
	// LicensesDir holds the full text of every license REUSE names, one <id>.txt each.
	LicensesDir = "LICENSES"
)

// ReuseActionRef is ReuseAction pinned at ReuseActionVersion, the form the hook job output uses.
func ReuseActionRef() string {
	return ReuseAction + "@" + ReuseActionVersion
}

// ReuseActionPinnedRef is ReuseAction pinned by full commit SHA with its release tag as a
// trailing comment, the digest-pinned form a workflow step uses (HISS-11).
func ReuseActionPinnedRef() string {
	return ReuseAction + "@" + ReuseActionCommit + "  # " + ReuseActionVersion
}

// ReuseMajor is the reuse release major ReuseActionVersion runs: its tag without the "v".
func ReuseMajor() string {
	return strings.TrimPrefix(ReuseActionVersion, "v")
}

// ReuseDeclared reports whether the repository at root declares its licensing the REUSE way:
// it carries ReuseFile, anything but a directory, or LICENSES/ as a directory. Neither is
// followed through a symlink. A path that exists but cannot be inspected is an error, never a
// repository without REUSE.
func ReuseDeclared(ctx context.Context, root string) (bool, error) {
	if ctx == nil {
		return false, errors.New("REUSE detection requires a context")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	for _, marker := range []struct {
		name string
		dir  bool
	}{{ReuseFile, false}, {LicensesDir, true}} {
		info, err := os.Lstat(filepath.Join(root, marker.name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("inspect %s: %w", marker.name, err)
		}
		if info.IsDir() == marker.dir && info.Mode()&os.ModeSymlink == 0 {
			return true, nil
		}
	}
	return false, nil
}
