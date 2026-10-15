// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// MaxCargoRoots bounds the Cargo workspaces the hooks and the toolchain stages run a complete
	// Cargo toolchain for, as apicompat.MaxTrackedModules bounds the Go modules.
	MaxCargoRoots = 64
	// cargoManifestFile is the manifest of a crate or a workspace.
	cargoManifestFile = "Cargo.toml"
	// maxCargoListingBytes bounds the manifest listing TrackedCargoRoots reads (HISS-02).
	maxCargoListingBytes = 1 << 22
	// maxCargoManifestBytes bounds the manifest TrackedCargoRoots reads for its [workspace] table.
	maxCargoManifestBytes = 1 << 20
	// maxCargoPathElements bounds the directory depth of a manifest TrackedCargoRoots discovers.
	maxCargoPathElements = 256
)

// ErrCargoDiscoveryMissing reports a stage configuration built without the one Cargo discovery
// seam every Cargo-presence decision uses.
var ErrCargoDiscoveryMissing = errors.New("cargo workspace discovery is not configured")

// ErrTooManyCargoRoots reports a repository whose Cargo toolchain runs would exceed MaxCargoRoots.
var ErrTooManyCargoRoots = errors.New("too many tracked Cargo workspaces")

// CargoRoot is a directory cargo builds from: a workspace root, or a crate that belongs to no
// workspace. Dir is slash-separated and relative to the repository root, "." for the root itself.
type CargoRoot struct {
	Dir string
	// Locked reports whether git tracks a Cargo.lock in Dir: the toolchain stages hold every
	// Cargo command to it (--locked) and run only where it is committed.
	Locked bool
}

// Lockfile is the root's Cargo.lock, slash-separated and relative to the repository root.
func (r CargoRoot) Lockfile() string { return path.Join(r.Dir, CargoLockFile) }

// TrackedCargoRoots returns, sorted by directory, the Cargo roots below repoDir that git's index
// tracks (#1102). Every tracked Cargo.toml outside target, vendor, testdata and dot directories is
// a crate or a workspace; it is a root unless an ancestor's Cargo.toml has a [workspace] table, so
// a workspace whose members are crates counts once. A crate below a workspace that commits its own
// Cargo.lock is a root too: cargo writes a member's lock at its workspace root, so a lock of its
// own marks a crate the workspace excludes. A tracked Cargo.lock without a Cargo.toml beside it is
// a locked root as well, as the root-only check treated it, so cargo names the missing manifest
// instead of the stages skipping the lock. A directory outside any git work tree tracks nothing
// and returns none; a git or manifest read that did not complete is an error, never an empty
// listing, so a stage that runs by it fails closed.
func TrackedCargoRoots(ctx context.Context, repoDir string) ([]CargoRoot, error) {
	if ctx == nil {
		return nil, errors.New("cargo workspace discovery requires a context")
	}
	inRepository, err := util.GitWorktreePresent(ctx, repoDir)
	if err != nil {
		return nil, fmt.Errorf("detect the git work tree of %s: %w", repoDir, err)
	}
	if !inRepository {
		return nil, nil
	}
	result, err := util.RunGitProbe(ctx, repoDir, maxCargoListingBytes, "ls-files", "--cached", "--deduplicate", "-z",
		"--", ":(glob)**/"+cargoManifestFile, ":(glob)**/"+CargoLockFile)
	if util.GitAnsweredNotARepository(result, err) && ctx.Err() == nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list the Cargo manifests git tracks in %s: %w", repoDir, err)
	}
	manifests, locks := cargoListing(string(result.Stdout))
	roots := make([]CargoRoot, 0, len(manifests))
	workspaces := make(map[string]bool, len(manifests))
	for index := 0; index < len(manifests); index++ {
		dir := manifests[index]
		isWorkspace, readErr := cargoWorkspaceManifest(filepath.Join(repoDir, filepath.FromSlash(path.Join(dir, cargoManifestFile))))
		if readErr != nil {
			return nil, readErr
		}
		workspaces[dir] = isWorkspace
		if locks[dir] || !insideCargoWorkspace(dir, workspaces) {
			roots = append(roots, CargoRoot{Dir: dir, Locked: locks[dir]})
		}
		if len(roots) > MaxCargoRoots {
			return nil, fmt.Errorf("%w: found more than %d", ErrTooManyCargoRoots, MaxCargoRoots)
		}
	}
	for dir := range locks {
		if !slices.Contains(manifests, dir) {
			roots = append(roots, CargoRoot{Dir: dir, Locked: true})
		}
	}
	if len(roots) > MaxCargoRoots {
		return nil, fmt.Errorf("%w: found more than %d", ErrTooManyCargoRoots, MaxCargoRoots)
	}
	if len(roots) == 0 {
		return nil, nil
	}
	slices.SortFunc(roots, func(a, b CargoRoot) int { return strings.Compare(a.Dir, b.Dir) })
	return roots, nil
}

// cargoListing splits a NUL-separated ls-files listing into the sorted directories holding a
// discovered Cargo.toml and the set holding a discovered Cargo.lock. Sorting puts every ancestor
// before its descendants, which TrackedCargoRoots relies on.
func cargoListing(listing string) ([]string, map[string]bool) {
	records := strings.Split(listing, "\x00")
	manifests := make([]string, 0, len(records))
	locks := make(map[string]bool, len(records))
	for index := 0; index < len(records); index++ {
		name := records[index]
		if name == "" || !cargoDirectoryDiscovered(path.Dir(name)) {
			continue
		}
		switch path.Base(name) {
		case cargoManifestFile:
			manifests = append(manifests, path.Dir(name))
		case CargoLockFile:
			locks[path.Dir(name)] = true
		}
	}
	slices.SortFunc(manifests, func(a, b string) int {
		if depth := cargoDepth(a) - cargoDepth(b); depth != 0 {
			return depth
		}
		return strings.Compare(a, b)
	})
	return slices.Compact(manifests), locks
}

// cargoDepth orders "." before every other directory and a parent before its children.
func cargoDepth(dir string) int {
	if dir == "." {
		return 0
	}
	return strings.Count(dir, "/") + 1
}

// cargoDirectoryDiscovered reports whether a manifest in dir is one the hooks and stages run:
// none below a build output (target), vendored sources, test fixtures or a dot directory.
func cargoDirectoryDiscovered(dir string) bool {
	if dir == "." {
		return true
	}
	elements := strings.Split(dir, "/")
	if len(elements) > maxCargoPathElements {
		return false
	}
	return !slices.ContainsFunc(elements, func(name string) bool {
		return name == "target" || name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".")
	})
}

// insideCargoWorkspace reports whether a proper ancestor of dir is a recorded workspace.
func insideCargoWorkspace(dir string, workspaces map[string]bool) bool {
	for parent := dir; parent != "."; {
		parent = path.Dir(parent)
		if workspaces[parent] {
			return true
		}
	}
	return false
}

// cargoWorkspaceManifest reports whether the manifest at file has a [workspace] table, or a
// [workspace.*] table that implies one. A tracked manifest deleted from the working tree is no
// workspace; any other read failure is an error.
func cargoWorkspaceManifest(file string) (bool, error) {
	data, err := util.ReadFileLimited(file, maxCargoManifestBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", file, err)
	}
	lines := strings.Split(string(data), "\n")
	return slices.ContainsFunc(lines, func(line string) bool {
		line = strings.TrimSpace(line)
		return strings.HasPrefix(line, "[workspace]") || strings.HasPrefix(line, "[workspace.")
	}), nil
}

// lockedCargoRoots returns the discovered Cargo roots that commit a Cargo.lock: the ones the
// toolchain stages run cargo in. The discovery runs once per stage configuration.
func (c *stageConfig) lockedCargoRoots(ctx context.Context) ([]CargoRoot, error) {
	if !c.cargoDiscovered {
		if c.cargoDiscovery == nil || ctx == nil {
			return nil, ErrCargoDiscoveryMissing
		}
		roots, err := c.cargoDiscovery(ctx, c.repoDir)
		if err != nil {
			return nil, fmt.Errorf("discover tracked Cargo workspaces: %w", err)
		}
		c.cargoRoots, c.cargoDiscovered = roots, true
	}
	return lockedRoots(c.cargoRoots), nil
}

// lockedRoots keeps the roots of roots that commit a Cargo.lock.
func lockedRoots(roots []CargoRoot) []CargoRoot {
	locked := make([]CargoRoot, 0, len(roots))
	for index := 0; index < len(roots) && index < MaxCargoRoots; index++ {
		if roots[index].Locked {
			locked = append(locked, roots[index])
		}
	}
	return locked
}

// discoveredCargoLockfiles names the Cargo.lock files the stages already discovered, for a
// refusal written after they ran; none when no stage asked.
func (c *stageConfig) discoveredCargoLockfiles() []string {
	locked := lockedRoots(c.cargoRoots)
	names := make([]string, 0, len(locked))
	for index := 0; index < len(locked); index++ {
		names = append(names, locked[index].Lockfile())
	}
	return names
}

// runCargoRoots runs part once for each locked Cargo root, in that root's directory below base:
// the repository for prefetch and audit, the isolated worktree for the tests. A repository whose
// one root is its own top keeps the reason it had before discovery; otherwise each root's outcome
// is a "<dir>/Cargo.lock: outcome" clause, a failure names its Cargo.lock, and a part that did not
// run for one root leaves the whole part skipped, never passed.
func runCargoRoots(ctx context.Context, cfg *stageConfig, base string,
	part func(ctx context.Context, dir string) (string, error)) (string, error) {
	roots, err := cfg.lockedCargoRoots(ctx)
	if err != nil {
		return "", err
	}
	if len(roots) == 1 && roots[0].Dir == "." {
		return part(ctx, base)
	}
	clauses := make([]string, 0, len(roots))
	var skip error
	for index := 0; index < len(roots) && index < MaxCargoRoots; index++ {
		root := roots[index]
		msg, partErr := part(ctx, filepath.Join(base, filepath.FromSlash(root.Dir)))
		if isFailure(partErr) {
			return "", fmt.Errorf("%s: %w", root.Lockfile(), partErr)
		}
		if partErr != nil {
			skip, msg = partErr, partErr.Error()
		}
		clauses = append(clauses, root.Lockfile()+": "+msg)
	}
	reason := strings.Join(clauses, "; ")
	if skip != nil {
		return "", skipped(reason)
	}
	return reason, nil
}
