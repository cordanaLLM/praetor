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
	lines, pins, err := releasedLines(string(content))
	if err != nil {
		return nil, nil, fmt.Errorf("read workflow %s: %w", fileName, err)
	}
	// A commented-out `uses:` line is an example or a disabled step, not a pin in use.
	live, err := util.StripHashComments(strings.Join(lines, "\n"))
	if err != nil {
		return nil, nil, fmt.Errorf("read workflow %s: %w", fileName, err)
	}
	var candidates []ActionCandidate
	var deprecations []DeprecationWarning

	for _, ref := range liveActionRefs(live) {
		pin := pins[ref.line]
		if pin.Action != ref.action {
			pin = util.PinnedAction{}
		}
		key := ref.action + "@" + ref.version + "@" + pin.SHA + ":" + fileName
		if seen[key] {
			continue
		}
		seen[key] = true

		cand, dep := buildActionCandidate(ref.action, ref.version, fileName)
		cand.Line = ref.line + 1
		if pin.SHA != "" {
			markSHAPin(&cand, pin)
		}
		candidates = append(candidates, cand)
		if dep != nil {
			deprecations = append(deprecations, *dep)
		}
	}
	return candidates, deprecations, nil
}

// maxWorkflowLines bounds the lines withPinnedReleases walks in one workflow (HISS-02).
const maxWorkflowLines = 100000

// maxActionRefsPerWorkflow bounds the action references one workflow contributes (HISS-02).
const maxActionRefsPerWorkflow = 100

// actionRef is one remote action reference on a live workflow line: the zero-based line
// index, the action and the version it is compared at.
type actionRef struct {
	line            int
	action, version string
}

// liveActionRefs returns the remote action references of a workflow whose comments are
// stripped, line by line and at most maxActionRefsPerWorkflow of them. A local action
// (./...) is not a pin.
func liveActionRefs(live string) []actionRef {
	lines := strings.Split(live, "\n")
	var refs []actionRef
	for index := 0; index < len(lines) && index < maxWorkflowLines && len(refs) < maxActionRefsPerWorkflow; index++ {
		for _, m := range workflowActionRegex.FindAllStringSubmatch(lines[index], maxActionRefsPerWorkflow-len(refs)) {
			if !strings.HasPrefix(m[1], ".") {
				refs = append(refs, actionRef{line: index, action: m[1], version: m[2]})
			}
		}
	}
	return refs
}

// withPinnedReleases rewrites every uses: reference pinned by full commit SHA with its
// release as a trailing comment (util.ParsePinnedAction) to action@release. The comment
// names the release the SHA stands for, and Renovate and Dependabot keep the two together.
// Stripping comments first would leave the bare SHA, which no release tag equals, so a
// current SHA pin would read as drift and a SHA pin of a deprecated major would go unflagged.
// A commented-out step keeps its leading "#" and is stripped as before.
func withPinnedReleases(content string) (string, error) {
	lines, _, err := releasedLines(content)
	if err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

// releasedLines splits content into lines, rewriting each SHA pin with a release comment as
// withPinnedReleases does, and returns every SHA pin (util.ParseSHAPin) by line index, bare
// ones included, so the scan can still name the commit a rewritten line runs.
func releasedLines(content string) ([]string, map[int]util.PinnedAction, error) {
	lines, uses, err := util.ScanActionUses(content, maxWorkflowLines)
	if err != nil {
		return nil, nil, err
	}
	pins := make(map[int]util.PinnedAction)
	for _, use := range uses {
		if !use.SHAPinned {
			continue
		}
		pins[use.Line] = use.Pin
		if use.Pinned {
			lines[use.Line] = strings.Replace(lines[use.Line], use.Ref, use.Pin.Action+"@"+use.Pin.Release, 1)
		}
	}
	return lines, pins, nil
}

// markSHAPin records that cand runs the commit pin names. A pin whose comment names a release
// is compared at that release and stays PinUnverified until VerifyActionPins asks its
// upstream. A pin that names no release has nothing to compare, so it is PinUnversioned:
// neither drift from a SHA to a tag nor up to date (#610).
func markSHAPin(cand *ActionCandidate, pin util.PinnedAction) {
	cand.PinnedSHA = pin.SHA
	if pin.Release == "" {
		cand.Pin = PinUnversioned
		cand.PinDetail = "no release comment names the pinned commit"
		cand.UpToDate = false
		return
	}
	cand.Pin = PinUnverified
	cand.PinDetail = "not checked against the upstream repository"
}

func buildActionCandidate(actName, curVer, fileName string) (ActionCandidate, *DeprecationWarning) {
	latestVer, hasLatest := knownActionLatest[actName]
	if !hasLatest {
		latestVer = curVer
	}
	cand := ActionCandidate{
		WorkflowFile:   fileName,
		Action:         actName,
		CurrentVersion: curVer,
		LatestVersion:  latestVer,
		UpToDate:       ActionPinCurrent(curVer, latestVer),
	}
	reason, found := deprecatedRuntime(actName, curVer)
	if !found {
		return cand, nil
	}
	if hasLatest {
		reason += "; upgrade to " + latestVer
	}
	cand.Deprecated, cand.Warning = true, reason
	return cand, &DeprecationWarning{
		Component: actName + "@" + curVer,
		Kind:      "runner-runtime-deprecated",
		Details:   reason + " in " + fileName,
	}
}

// deprecatedRuntime returns the deprecation deprecatedActionVersions records for action at
// exactly version.
func deprecatedRuntime(action, version string) (string, bool) {
	reason, found := deprecatedActionVersions[action][version]
	return reason, found
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
