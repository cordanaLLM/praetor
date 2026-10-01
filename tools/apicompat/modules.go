// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apicompat

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// maxModuleListingBytes bounds the go.mod listing TrackedModuleFiles reads (HISS-02): room
	// for tens of thousands of module paths, far past the gate's own bound on compared modules.
	maxModuleListingBytes = 1 << 22
	// maxModulePathElements is the gate's bound on the directory depth of a module it discovers.
	maxModulePathElements = 256
	// moduleListingPathspec selects every go.mod below the listed directory, its own included.
	moduleListingPathspec = ":(glob)**/go.mod"
)

// IsModuleFile reports whether name, a slash-separated path relative to the repository root, is a
// go.mod the gate discovers: one in no directory a ./... pattern skips (testdata, vendor, and
// names starting with "." or "_"). It is the gate's isModuleFile, which the standalone gate
// program cannot import from here; TestGate_Boundary_AdoptionDiscoversTheGatesModules keeps the
// two equal by running the gate.
func IsModuleFile(name string) bool {
	if path.Base(name) != "go.mod" {
		return false
	}
	elements := strings.Split(path.Dir(name), "/")
	if len(elements) > maxModulePathElements {
		return false
	}
	return !slices.ContainsFunc(elements, skippedDirectory)
}

// skippedDirectory reports whether the go command skips a directory of this name in a ./...
// pattern (go help packages).
func skippedDirectory(name string) bool {
	if name == "." {
		return false
	}
	return name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// TrackedModuleFiles returns, sorted, the go.mod files below repoPath that git's index tracks and
// the gate discovers (IsModuleFile), as slash paths relative to repoPath. A directory outside any
// git work tree, or one git answers is not a repository, tracks nothing and returns none. A git
// read that did not complete is an error, never an empty listing, so a caller that emits or
// audits the gate by it fails closed.
func TrackedModuleFiles(ctx context.Context, repoPath string) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("go module discovery requires a context")
	}
	inRepository, err := util.GitWorktreePresent(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("detect the git work tree of %s: %w", repoPath, err)
	}
	if !inRepository {
		return nil, nil
	}
	result, err := util.RunGitProbe(ctx, repoPath, maxModuleListingBytes,
		"ls-files", "--cached", "--deduplicate", "-z", "--", moduleListingPathspec)
	if util.GitAnsweredNotARepository(result, err) && ctx.Err() == nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list the go.mod files git tracks in %s: %w", repoPath, err)
	}
	records := strings.Split(string(result.Stdout), "\x00")
	modules := make([]string, 0, len(records))
	for index := 0; index < len(records); index++ {
		if records[index] != "" && IsModuleFile(records[index]) {
			modules = append(modules, records[index])
		}
	}
	slices.Sort(modules)
	return slices.Compact(modules), nil
}

// TracksModule reports whether git tracks a go.mod below repoPath that the gate discovers
// (TrackedModuleFiles): the repositories the api:public-contract facet gives the gate, since Go
// is the only language whose API the gate compares.
func TracksModule(ctx context.Context, repoPath string) (bool, error) {
	modules, err := TrackedModuleFiles(ctx, repoPath)
	if err != nil {
		return false, err
	}
	return len(modules) > 0, nil
}
