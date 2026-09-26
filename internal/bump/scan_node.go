package bump

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/nodemanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

// errNoJSONObject is returned when a package manager's output carries no JSON object.
var errNoJSONObject = errors.New("bump: no JSON object in command output")

type pnpmOutdatedItem struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Wanted  string `json:"wanted"`
}

type packageJSONFormat struct {
	Name            string            `json:"name"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// DiscoverNodePackages finds all directories containing package.json: the root
// plus every workspace member.
//
// This used to return the workspace members *instead of* the root, so a pnpm
// monorepo's root toolchain — turbo, prettier, eslint, lefthook, commitlint —
// never reported drift (issue #97). Resolution now lives in
// internal/nodemanifest, shared with internal/docdistill, which had the
// mirror-image bug of reading only the root.
//
// Discovery errors, including a set truncated at
// nodemanifest.MaxWorkspaceDirs, are returned rather than turned into an empty
// set: an empty set reads as "no Node manifests", which is a different and
// false answer.
func DiscoverNodePackages(repoPath string) ([]string, error) {
	dirs, err := nodemanifest.DiscoverPackageDirs(repoPath)
	if err != nil {
		return dirs, fmt.Errorf("discover Node packages: %w", err)
	}
	return dirs, nil
}

// ScanNodeDependencies returns the dependency inventory of every Node package in repoPath:
// one entry per dependency a package.json declares, whether or not an upgrade exists for
// it. An entry whose TargetVersion differs from its CurrentVersion is an upgrade
// candidate; an up-to-date dependency carries TargetVersion == CurrentVersion.
func ScanNodeDependencies(ctx context.Context, repoPath string, opts ScanOptions) ([]UpgradeCandidate, error) {
	pkgDirs, err := DiscoverNodePackages(repoPath)
	if err != nil {
		return nil, err
	}
	var inventory []UpgradeCandidate
	for _, dirRel := range pkgDirs {
		found, err := scanNodePackage(ctx, repoPath, dirRel, opts)
		if err != nil {
			return nil, fmt.Errorf("scan Node package %s: %w", dirRel, err)
		}
		inventory = append(inventory, found...)
	}
	return inventory, nil
}

// scanNodePackage returns one package's inventory. The dependencies its package.json
// declares are the inventory; `pnpm outdated` lists only outdated packages, so it can
// supply upgrade targets but never the count of what was scanned.
func scanNodePackage(ctx context.Context, repoPath, dirRel string, opts ScanOptions) ([]UpgradeCandidate, error) {
	return manifestInventory(ctx,
		func() ([]UpgradeCandidate, error) { return scanPackageJSONStatic(ctx, repoPath, dirRel) },
		func() ([]UpgradeCandidate, error) {
			return scanNodePackageDir(ctx, filepath.Join(repoPath, dirRel), dirRel, opts)
		})
}

// extractJSONObject returns the outermost JSON object embedded in combined command
// output. Package managers interleave warnings on stderr with their JSON report, so the
// report is located by its first '{' and last '}' rather than parsed verbatim.
func extractJSONObject(out string) ([]byte, error) {
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end < start {
		return nil, errNoJSONObject
	}
	return []byte(out[start : end+1]), nil
}

func scanNodePackageDir(ctx context.Context, dir, dirRel string, opts ScanOptions) ([]UpgradeCandidate, error) {
	// pnpm exits non-zero when outdated packages exist, so the exit status alone is
	// not an error: only an empty report is.
	out, err := util.RunCommand(ctx, dir, "pnpm", "outdated", "--json")
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("pnpm outdated in %s: %w", dirRel, err)
	}

	raw, err := extractJSONObject(out)
	if err != nil {
		return nil, err
	}

	var outdated map[string]pnpmOutdatedItem
	if err := json.Unmarshal(raw, &outdated); err != nil {
		return nil, fmt.Errorf("parse pnpm outdated report for %s: %w", dirRel, err)
	}
	if len(outdated) > maxManifestDependencies {
		return nil, fmt.Errorf("pnpm outdated report for %s exceeds %d entries", dirRel, maxManifestDependencies)
	}

	var candidates []UpgradeCandidate
	for _, pkg := range slices.Sorted(maps.Keys(outdated)) {
		if cand, ok := nodeUpgradeCandidate(pkg, outdated[pkg], dirRel, opts); ok {
			candidates = append(candidates, cand)
		}
	}
	return candidates, nil
}

// nodeUpgradeCandidate converts one pnpm outdated entry into an upgrade candidate,
// reporting false when the package is current or filtered out by the channel policy.
func nodeUpgradeCandidate(pkg string, item pnpmOutdatedItem, dirRel string, opts ScanOptions) (UpgradeCandidate, bool) {
	if item.Latest == "" || item.Current == item.Latest {
		return UpgradeCandidate{}, false
	}
	if !upgradeAllowed(item.Latest, opts) {
		return UpgradeCandidate{}, false
	}
	return UpgradeCandidate{
		Package:        pkg,
		CurrentVersion: item.Current,
		TargetVersion:  item.Latest,
		Channel:        ClassifyChannel(item.Latest),
		ManifestType:   "package.json",
		ModuleDir:      dirRel,
	}, true
}

// scanPackageJSONStatic returns the dependencies dirRel's package.json declares, in name
// order, each with its declared version as both current and target. A package declared in
// both dependencies and devDependencies is one dependency, counted once.
func scanPackageJSONStatic(ctx context.Context, repoPath, dirRel string) ([]UpgradeCandidate, error) {
	name := filepath.Join(dirRel, "package.json")
	data, err := readManifest(ctx, repoPath, name)
	if err != nil {
		return nil, err
	}
	var pj packageJSONFormat
	if err := json.Unmarshal(data, &pj); err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	seen := make(map[string]bool)
	var declared []UpgradeCandidate
	for _, deps := range []map[string]string{pj.Dependencies, pj.DevDependencies} {
		if len(deps) > maxManifestDependencies {
			return nil, fmt.Errorf("%s declares more than %d dependencies in one section", name, maxManifestDependencies)
		}
		for _, pkg := range slices.Sorted(maps.Keys(deps)) {
			if !seen[pkg] {
				seen[pkg] = true
				declared = append(declared, declaredNodeDependency(pkg, deps[pkg], dirRel))
			}
		}
	}
	return declared, nil
}

// declaredNodeDependency is one declared dependency with no known upgrade. A single-version
// range contributes its version; any other spec (a workspace: or file: reference, a tag, a
// compound range) is carried verbatim.
func declaredNodeDependency(pkg, spec, dirRel string) UpgradeCandidate {
	version := spec
	if _, bare, ok := splitRangeOperator(spec); ok {
		version = bare
	}
	return UpgradeCandidate{
		Package:        pkg,
		CurrentVersion: version,
		TargetVersion:  version,
		Channel:        ClassifyChannel(version),
		ManifestType:   "package.json",
		ModuleDir:      dirRel,
	}
}
