// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package bump

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/semver"
	"github.com/cordanaLLM/praetor/internal/util"
)

var workflowActionRegex = regexp.MustCompile(`uses:\s*([a-zA-Z0-9\-_/]+)@([a-zA-Z0-9.\-_+]+)`)

// Known canonical latest versions for standard CI actions: the newest upstream release
// major, or the exact tag for actions consumed by tag. The deprecation warning names its
// upgrade target from this map, so a bump moves in one place.
var knownActionLatest = map[string]string{
	"actions/checkout":                  "v7",
	"actions/cache":                     "v6",
	"actions/setup-go":                  "v7",
	"actions/setup-node":                "v7",
	"actions/setup-python":              "v7",
	"actions/upload-artifact":           "v7",
	"actions/download-artifact":         "v8",
	"actions/upload-pages-artifact":     "v5",
	"actions/deploy-pages":              "v5",
	"actions/configure-pages":           "v6",
	"fsfe/reuse-action":                 "v6",
	"goreleaser/goreleaser-action":      "v7",
	"sigstore/cosign-installer":         "v4.1.2",
	"anchore/sbom-action/download-syft": "v0.24.2",
	"docker/setup-buildx-action":        "v4",
	"docker/login-action":               "v4",
	"azure/setup-helm":                  "v5",
}

const (
	node12Deprecated = "Node.js 12 runtime deprecated"
	node16Deprecated = "Node.js 16 runtime deprecated"
	node20Deprecated = "Node.js 20 runtime deprecated"
	artifactV1Sunset = "Artifact v1 deprecated"
	artifactV3Sunset = "Artifact v3 sunset"
)

// Deprecated action versions known to target obsolete runtimes. From v3 on, each entry is
// the runtime that major's action.yml declares under runs.using; buildActionCandidate
// appends the upgrade target from knownActionLatest.
var deprecatedActionVersions = map[string]map[string]string{
	"actions/checkout": {
		"v1": node12Deprecated,
		"v2": node16Deprecated,
		"v3": node16Deprecated,
		"v4": node20Deprecated,
	},
	"actions/cache": {
		"v3": node16Deprecated,
		"v4": node20Deprecated,
	},
	"actions/setup-go": {
		"v1": node12Deprecated,
		"v2": node16Deprecated,
		"v3": node16Deprecated,
		"v4": node16Deprecated,
		"v5": node20Deprecated,
	},
	"actions/setup-node": {
		"v3": node16Deprecated,
		"v4": node20Deprecated,
	},
	"actions/setup-python": {
		"v1": node12Deprecated,
		"v2": node16Deprecated,
		"v3": node16Deprecated,
		"v4": node16Deprecated,
		"v5": node20Deprecated,
	},
	"actions/upload-artifact": {
		"v1": artifactV1Sunset,
		"v2": node16Deprecated,
		"v3": artifactV3Sunset,
		"v4": node20Deprecated,
		"v5": node20Deprecated,
	},
	"actions/download-artifact": {
		"v1": artifactV1Sunset,
		"v2": node16Deprecated,
		"v3": artifactV3Sunset,
		"v4": node20Deprecated,
		"v5": node20Deprecated,
		"v6": node20Deprecated,
	},
	"actions/deploy-pages": {
		"v1": node16Deprecated,
		"v2": node16Deprecated,
		"v3": node20Deprecated,
		"v4": node20Deprecated,
	},
	"actions/configure-pages": {
		"v4": node20Deprecated,
		"v5": node20Deprecated,
	},
}

// ScanWorkflowActions inspects all workflow YAML files in repoPath/.github/workflows/.
func ScanWorkflowActions(ctx context.Context, repoPath string) ([]ActionCandidate, []DeprecationWarning, error) {
	workflowDir, err := util.ConfinePath(repoPath, filepath.Join(".github", "workflows"))
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(workflowDir)
	if util.DirectoryAbsent(workflowDir, err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}

	var candidates []ActionCandidate
	var deprecations []DeprecationWarning
	seen := make(map[string]bool)

	for i := 0; i < 500 && i < len(entries); i++ {
		entry := entries[i]
		if entry.IsDir() || (filepath.Ext(entry.Name()) != ".yml" && filepath.Ext(entry.Name()) != ".yaml") {
			continue
		}
		cands, deps, err := parseWorkflowFile(ctx, repoPath, entry.Name(), seen)
		if err != nil {
			return nil, nil, err
		}
		candidates = append(candidates, cands...)
		deprecations = append(deprecations, deps...)
	}
	return candidates, deprecations, nil
}

func parseWorkflowFile(ctx context.Context, repoPath, fileName string, seen map[string]bool) ([]ActionCandidate, []DeprecationWarning, error) {
	content, err := readManifest(ctx, repoPath, filepath.Join(".github", "workflows", fileName))
	if err != nil {
		return nil, nil, fmt.Errorf("read workflow %s: %w", fileName, err)
	}
	released, err := withPinnedReleases(string(content))
	if err != nil {
		return nil, nil, fmt.Errorf("read workflow %s: %w", fileName, err)
	}
	// A commented-out `uses:` line is an example or a disabled step, not a pin in use.
	live, err := util.StripHashComments(released)
	if err != nil {
		return nil, nil, fmt.Errorf("read workflow %s: %w", fileName, err)
	}
	matches := workflowActionRegex.FindAllStringSubmatch(live, 100)
	var candidates []ActionCandidate
	var deprecations []DeprecationWarning

	for _, m := range matches {
		if len(m) != 3 || strings.HasPrefix(m[1], ".") {
			continue
		}
		actName, curVer := m[1], m[2]
		key := actName + "@" + curVer + ":" + fileName
		if seen[key] {
			continue
		}
		seen[key] = true

		cand, dep := buildActionCandidate(actName, curVer, fileName)
		candidates = append(candidates, cand)
		if dep != nil {
			deprecations = append(deprecations, *dep)
		}
	}
	return candidates, deprecations, nil
}

// maxWorkflowLines bounds the lines withPinnedReleases walks in one workflow (HISS-02).
const maxWorkflowLines = 100000

// withPinnedReleases rewrites every uses: reference pinned by full commit SHA with its
// release as a trailing comment (util.ParsePinnedAction) to action@release. The comment
// names the release the SHA stands for, and Renovate and Dependabot keep the two together.
// Stripping comments first would leave the bare SHA, which no release tag equals, so a
// current SHA pin would read as drift and a SHA pin of a deprecated major would go unflagged.
// A commented-out step keeps its leading "#" and is stripped as before.
func withPinnedReleases(content string) (string, error) {
	lines, uses, err := util.ScanActionUses(content, maxWorkflowLines)
	if err != nil {
		return "", err
	}
	for _, use := range uses {
		if use.Pinned {
			lines[use.Line] = strings.Replace(lines[use.Line], use.Ref, use.Pin.Action+"@"+use.Pin.Release, 1)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func buildActionCandidate(actName, curVer, fileName string) (ActionCandidate, *DeprecationWarning) {
	latestVer, hasLatest := knownActionLatest[actName]
	if !hasLatest {
		latestVer = curVer
	}
	isDeprecated := false
	warningMsg := ""
	var dep *DeprecationWarning
	if depMap, ok := deprecatedActionVersions[actName]; ok {
		if reason, found := depMap[curVer]; found {
			msg := reason
			if hasLatest {
				msg += "; upgrade to " + latestVer
			}
			isDeprecated = true
			warningMsg = msg
			dep = &DeprecationWarning{
				Component: actName + "@" + curVer,
				Kind:      "runner-runtime-deprecated",
				Details:   msg + " in " + fileName,
			}
		}
	}
	return ActionCandidate{
		WorkflowFile:   fileName,
		Action:         actName,
		CurrentVersion: curVer,
		LatestVersion:  latestVer,
		UpToDate:       ActionPinCurrent(curVer, latestVer),
		Deprecated:     isDeprecated,
		Warning:        warningMsg,
	}, dep
}

// ActionPinCurrent reports whether an action pinned at current is at or ahead of latest.
//
// The registry names some actions by a moving major tag ("v4") and others by an exact
// release ("v4.1.2"), so a raw string comparison called every exact pin of a major-tag
// action drift (BUG-425). Both tags are compared at the precision of the less precise
// one: "v4.1.2" is current against "v4", "v4" (which follows every v4.x release) against
// "v4.1.2", and "v3.8.1" is behind "v4.1.2". A pin ahead of the registry is current; the
// registry lagging is not the workflow's drift. A pin that is not a version tag, such as a
// commit SHA or a branch, is current only when it equals latest.
func ActionPinCurrent(current, latest string) bool {
	cur, curPrecision, curOK := semver.ParseTag(current)
	lat, latPrecision, latOK := semver.ParseTag(latest)
	if !curOK || !latOK {
		return current == latest
	}
	precision := min(curPrecision, latPrecision)
	return semver.Compare(cur.Truncate(precision), lat.Truncate(precision)) >= 0
}
