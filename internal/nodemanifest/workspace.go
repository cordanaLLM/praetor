// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

// Package nodemanifest resolves which package.json files belong to a repository.
//
// It exists because two subsystems answered that question differently and both
// were wrong, in opposite directions:
//
//   - internal/docdistill read the root package.json and nothing else, so on any
//     workspace repository it saw none of the real dependencies (issue #96).
//   - internal/bump returned the workspace member directories *instead of* the
//     root, so the root toolchain never reported drift (issue #97).
//
// A repository's manifest set is root plus every workspace member. Resolving it
// in one place is what stops the two subsystems disagreeing about what a
// repository declares.
package nodemanifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// MaxWorkspaceDirs bounds the resolved set so a pathological glob cannot make
// discovery unbounded (HISS-02). A monorepo an order of magnitude larger than
// any we govern still fits well inside it.
const MaxWorkspaceDirs = 2000

type pnpmWorkspaceConfig struct {
	Packages []string `yaml:"packages"`
}

// workspacesField carries npm/yarn's `workspaces`, which is either an array of
// patterns or an object with a `packages` array. Both forms are in the wild, so
// both are accepted.
type workspacesField struct {
	patterns []string
}

func (w *workspacesField) UnmarshalJSON(data []byte) error {
	var asArray []string
	if err := json.Unmarshal(data, &asArray); err == nil {
		w.patterns = asArray
		return nil
	}
	// A malformed `workspaces` is not a reason to fail discovery: the root
	// manifest is still a real manifest, and reporting it beats reporting
	// nothing. The caller sees the root and no members.
	var asObject struct {
		Packages []string `json:"packages"`
	}
	if json.Unmarshal(data, &asObject) == nil {
		w.patterns = asObject.Packages
	}
	return nil
}

type rootManifest struct {
	Workspaces workspacesField `json:"workspaces"`
}

// DiscoverPackageDirs returns every repo-relative directory in repoPath that
// holds a package.json: the root, when it has one, plus each workspace member.
//
// The result is sorted and deduplicated, so a workspace pattern that matches the
// root — pnpm permits `packages: ['.']` — yields the root once rather than
// twice. An empty result means the repository declares no Node manifest at all.
func DiscoverPackageDirs(repoPath string) ([]string, error) {
	seen := make(map[string]struct{})
	var dirs []string

	add := func(dir string) bool {
		clean := filepath.ToSlash(filepath.Clean(dir))
		if clean == "" {
			clean = "."
		}
		if _, dup := seen[clean]; dup {
			return true
		}
		if len(dirs) >= MaxWorkspaceDirs {
			return false
		}
		seen[clean] = struct{}{}
		dirs = append(dirs, clean)
		return true
	}

	rootHasManifest := util.FileExists(filepath.Join(repoPath, "package.json"))
	if rootHasManifest {
		add(".")
	}

	patterns, err := workspacePatterns(repoPath, rootHasManifest)
	if err != nil {
		return nil, err
	}
	for _, pattern := range patterns {
		for _, member := range globWorkspacePackages(repoPath, pattern) {
			if !add(member) {
				// The bound is a ceiling, not an error: returning what was found
				// beats returning nothing, and the count is visible to callers.
				sort.Strings(dirs)
				return dirs, nil
			}
		}
	}

	sort.Strings(dirs)
	return dirs, nil
}

// workspacePatterns collects the member patterns a repository declares, from
// pnpm-workspace.yaml and from the root manifest's `workspaces` field. Both are
// read: a repository may carry pnpm's file while its manifest still declares
// `workspaces` for tools that only understand npm.
func workspacePatterns(repoPath string, rootHasManifest bool) ([]string, error) {
	var patterns []string

	if util.FileExists(filepath.Join(repoPath, "pnpm-workspace.yaml")) {
		data, err := readConfined(repoPath, "pnpm-workspace.yaml")
		if err != nil {
			return nil, fmt.Errorf("read pnpm-workspace.yaml: %w", err)
		}
		var cfg pnpmWorkspaceConfig
		// A malformed workspace file leaves the root discoverable rather than
		// failing the whole scan.
		if yaml.Unmarshal(data, &cfg) == nil {
			patterns = append(patterns, cfg.Packages...)
		}
	}

	if rootHasManifest {
		data, err := readConfined(repoPath, "package.json")
		if err != nil {
			return nil, fmt.Errorf("read package.json: %w", err)
		}
		var parsed rootManifest
		if json.Unmarshal(data, &parsed) == nil {
			patterns = append(patterns, parsed.Workspaces.patterns...)
		}
	}

	return patterns, nil
}

// globWorkspacePackages expands one workspace pattern to the repo-relative
// directories under it that carry a package.json.
//
// It returns no error: the only failure filepath.Glob reports is a malformed
// pattern, and a repository is not broken because one of its patterns is. A
// pattern that matches nothing and a pattern that cannot be parsed are the same
// thing to a caller deciding which manifests to read.
func globWorkspacePackages(repoPath, pattern string) []string {
	clean := strings.TrimPrefix(strings.TrimSpace(pattern), "./")
	if clean == "" || strings.HasPrefix(clean, "!") {
		// pnpm allows negated patterns. Treating one as a literal path would
		// glob for a directory named "!x", so it is skipped rather than guessed
		// at; excluding an already-matched member is a refinement, not a
		// correctness requirement, for a set used to decide what to read.
		return nil
	}
	if strings.Contains(clean, "..") {
		return nil
	}

	matches, globErr := filepath.Glob(filepath.Join(repoPath, filepath.FromSlash(clean)))
	if globErr != nil {
		return nil
	}

	dirs := make([]string, 0, len(matches))
	for _, match := range matches {
		info, statErr := os.Stat(match)
		if statErr != nil || !info.IsDir() {
			continue
		}
		if !util.FileExists(filepath.Join(match, "package.json")) {
			continue
		}
		rel, relErr := filepath.Rel(repoPath, match)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		dirs = append(dirs, filepath.ToSlash(rel))
	}
	return dirs
}

// readConfined reads rel below root after confining it with util.ConfinePath, so
// a workspace glob or a caller-supplied repository path can never read outside
// root.
func readConfined(root, rel string) ([]byte, error) {
	path, err := util.ConfinePath(root, rel)
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- path is confined to root by util.ConfinePath above.
	return os.ReadFile(path)
}
