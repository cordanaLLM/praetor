// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package bump

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

var workflowActionRegex = regexp.MustCompile(`uses:\s*([a-zA-Z0-9\-_/]+)@([a-zA-Z0-9.\-_+]+)`)

// Known canonical latest versions for standard CI actions.
var knownActionLatest = map[string]string{
	"actions/checkout":                  "v4",
	"actions/cache":                     "v6",
	"actions/setup-go":                  "v5",
	"actions/setup-python":              "v5",
	"actions/upload-artifact":           "v4",
	"actions/download-artifact":         "v4",
	"actions/upload-pages-artifact":     "v3",
	"actions/deploy-pages":              "v4",
	"actions/configure-pages":           "v5",
	"fsfe/reuse-action":                 "v5",
	"goreleaser/goreleaser-action":      "v7",
	"sigstore/cosign-installer":         "v4.1.2",
	"anchore/sbom-action/download-syft": "v0.24.2",
}

// Deprecated action versions known to target obsolete runtimes (e.g. Node 20 runner deprecation).
var deprecatedActionVersions = map[string]map[string]string{
	"actions/checkout": {
		"v1": "Node.js 12 runtime deprecated",
		"v2": "Node.js 16 runtime deprecated",
		"v3": "Node.js 16/20 runtime deprecated; upgrade to v4",
	},
	"actions/setup-go": {
		"v1": "Node.js 12 runtime deprecated",
		"v2": "Node.js 16 runtime deprecated",
		"v3": "Node.js 16 runtime deprecated",
		"v4": "Node.js 20 runtime deprecated; upgrade to v5",
	},
	"actions/setup-python": {
		"v1": "Node.js 12 runtime deprecated",
		"v2": "Node.js 16 runtime deprecated",
		"v3": "Node.js 16 runtime deprecated",
		"v4": "Node.js 20 runtime deprecated; upgrade to v5",
	},
	"actions/upload-artifact": {
		"v1": "Artifact v1 deprecated",
		"v2": "Node.js 16 runtime deprecated",
		"v3": "Artifact v3 sunset; upgrade to v4",
	},
	"actions/download-artifact": {
		"v1": "Artifact v1 deprecated",
		"v2": "Node.js 16 runtime deprecated",
		"v3": "Artifact v3 sunset; upgrade to v4",
	},
}

// ScanWorkflowActions inspects all workflow YAML files in repoPath/.github/workflows/.
func ScanWorkflowActions(repoPath string) ([]ActionCandidate, []DeprecationWarning, error) {
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
		cands, deps, err := parseWorkflowFile(repoPath, entry.Name(), seen)
		if err != nil {
			return nil, nil, err
		}
		candidates = append(candidates, cands...)
		deprecations = append(deprecations, deps...)
	}
	return candidates, deprecations, nil
}

func parseWorkflowFile(repoPath, fileName string, seen map[string]bool) ([]ActionCandidate, []DeprecationWarning, error) {
	content, err := readManifest(repoPath, filepath.Join(".github", "workflows", fileName))
	if err != nil {
		return nil, nil, fmt.Errorf("read workflow %s: %w", fileName, err)
	}
	matches := workflowActionRegex.FindAllStringSubmatch(string(content), 100)
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

func buildActionCandidate(actName, curVer, fileName string) (ActionCandidate, *DeprecationWarning) {
	latestVer, hasLatest := knownActionLatest[actName]
	if !hasLatest {
		latestVer = curVer
	}
	isDeprecated := false
	warningMsg := ""
	var dep *DeprecationWarning
	if depMap, ok := deprecatedActionVersions[actName]; ok {
		if msg, found := depMap[curVer]; found {
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
		Deprecated:     isDeprecated,
		Warning:        warningMsg,
	}, dep
}
