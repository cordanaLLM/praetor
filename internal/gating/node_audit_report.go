// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxNodeAdvisories bounds both audit maps and dependency traversal work.
const maxNodeAdvisories = 65536

type nodeAuditAdvisory struct {
	GHSA     string `json:"github_advisory_id"`
	URL      string `json:"url"`
	Severity string `json:"severity"`
}

type nodeAuditVulnerability struct {
	Severity string            `json:"severity"`
	Via      []json.RawMessage `json:"via"`
}

type nodeAuditFindings struct {
	ids      []string
	high     int
	critical int
}

// nodeAuditGHSA extracts an exact advisory ID, retaining unknown IDs as unexcusable findings.
func nodeAuditGHSA(key string, advisory nodeAuditAdvisory) string {
	if advisory.GHSA != "" {
		return advisory.GHSA
	}
	parsed, err := url.Parse(advisory.URL)
	if err == nil && parsed.Host == "github.com" {
		id := strings.TrimPrefix(parsed.Path, "/advisories/")
		if config.ValidGHSA(id) {
			return id
		}
	}
	return key
}

// add records the listed severity before exceptions, so metadata gaps remain visible.
func (f *nodeAuditFindings) add(severity string) bool {
	switch severity {
	case "high":
		f.high++
	case "critical":
		f.critical++
	default:
		return false
	}
	return true
}

// listedNodeFindings reads pnpm advisories or npm 7+ package findings deterministically.
func listedNodeFindings(report npmAuditReport) (nodeAuditFindings, error) {
	if len(report.Advisories) > maxNodeAdvisories || len(report.Vulnerabilities) > maxNodeAdvisories {
		return nodeAuditFindings{}, fmt.Errorf("node audit exceeds %d listed entries", maxNodeAdvisories)
	}
	if report.Metadata.Vulnerabilities.High < 0 || report.Metadata.Vulnerabilities.Critical < 0 {
		return nodeAuditFindings{}, fmt.Errorf("node audit report contains negative vulnerability counts")
	}
	if report.Advisories != nil && report.Vulnerabilities != nil {
		return nodeAuditFindings{}, fmt.Errorf("node audit report mixes advisory and vulnerability map formats")
	}
	if report.Advisories != nil {
		return listedPnpmFindings(report.Advisories), nil
	}
	return listedNpmFindings(report.Vulnerabilities)
}

// listedPnpmFindings counts advisory severities independently of metadata totals.
func listedPnpmFindings(advisories map[string]nodeAuditAdvisory) nodeAuditFindings {
	var findings nodeAuditFindings
	keys := util.SortedKeys(advisories)
	for i := 0; i < len(keys) && i < maxNodeAdvisories; i++ {
		advisory := advisories[keys[i]]
		if findings.add(advisory.Severity) {
			findings.ids = append(findings.ids, nodeAuditGHSA(keys[i], advisory))
		}
	}
	return findings
}

// listedNpmFindings counts package severities while checking each referenced high advisory.
func listedNpmFindings(packages map[string]nodeAuditVulnerability) (nodeAuditFindings, error) {
	var findings nodeAuditFindings
	keys := util.SortedKeys(packages)
	budget := maxNodeAdvisories
	memo := make(map[string][]string)
	for i := 0; i < len(keys) && i < maxNodeAdvisories; i++ {
		if !findings.add(packages[keys[i]].Severity) {
			continue
		}
		ids, err := npmPackageAdvisories(packages, keys[i], &budget, memo)
		if err != nil {
			return findings, err
		}
		if len(ids) == 0 {
			return findings, fmt.Errorf("npm audit high or critical package %s has no bounded GHSA advisory", keys[i])
		}
		findings.ids = npmAdvisoryUnion(findings.ids, ids)
		if len(findings.ids) > maxNodeAdvisories {
			return findings, fmt.Errorf("node audit exceeds %d advisory references", maxNodeAdvisories)
		}
	}
	return findings, nil
}

// npmAdvisoryFrame holds one package's iterative depth-first traversal state.
type npmAdvisoryFrame struct {
	name                   string
	ids, dependencies      []string
	index, low, base, next int
}

// npmAdvisoryWalk shares completed advisory sets and a single report budget.
type npmAdvisoryWalk struct {
	packages map[string]nodeAuditVulnerability
	memo     map[string][]string
	budget   *int
	nodes    map[string]*npmAdvisoryFrame
	path     []*npmAdvisoryFrame
	active   []*npmAdvisoryFrame
}

// npmPackageAdvisories memoizes transitive advisory sets, including complete dependency cycles.
func npmPackageAdvisories(packages map[string]nodeAuditVulnerability, name string, budget *int, memo map[string][]string) ([]string, error) {
	if ids, ok := memo[name]; ok {
		return ids, nil
	}
	walk := npmAdvisoryWalk{packages: packages, memo: memo, budget: budget, nodes: make(map[string]*npmAdvisoryFrame)}
	if err := walk.push(name); err != nil {
		return nil, err
	}
	for step := 0; len(walk.path) > 0 && step < maxNodeAdvisories; step++ {
		frame := walk.path[len(walk.path)-1]
		if frame.next < len(frame.dependencies) {
			dependency := frame.dependencies[frame.next]
			frame.next++
			if err := walk.follow(frame, dependency); err != nil {
				return nil, err
			}
			continue
		}
		walk.finish(frame)
	}
	if len(walk.path) > 0 {
		return nil, fmt.Errorf("npm audit traversal exceeds %d steps", maxNodeAdvisories)
	}
	return memo[name], nil
}

// push parses and charges each previously unresolved package and its via entries once.
func (w *npmAdvisoryWalk) push(name string) error {
	*w.budget -= 1
	if *w.budget < 0 {
		return fmt.Errorf("npm audit traversal exceeds %d steps", maxNodeAdvisories)
	}
	item, ok := w.packages[name]
	if !ok {
		return fmt.Errorf("npm audit via references missing package %s", name)
	}
	ids, dependencies, err := npmViaAdvisories(item.Via, nil, nil, w.budget)
	if err != nil {
		return err
	}
	index := len(w.nodes)
	frame := &npmAdvisoryFrame{name: name, ids: ids, dependencies: dependencies, index: index, low: index, base: len(w.active)}
	w.nodes[name] = frame
	w.path = append(w.path, frame)
	w.active = append(w.active, frame)
	return nil
}

// follow reuses completed sets and records back edges without revisiting active packages.
func (w *npmAdvisoryWalk) follow(frame *npmAdvisoryFrame, dependency string) error {
	if ids, ok := w.memo[dependency]; ok {
		frame.ids = npmAdvisoryUnion(frame.ids, ids)
		return nil
	}
	if child, ok := w.nodes[dependency]; ok {
		frame.low = min(frame.low, child.index)
		return nil
	}
	return w.push(dependency)
}

// finish completes a package or cycle before propagating its advisory set to its parent.
func (w *npmAdvisoryWalk) finish(frame *npmAdvisoryFrame) {
	w.path = w.path[:len(w.path)-1]
	if frame.low == frame.index {
		w.complete(frame)
	}
	if len(w.path) > 0 {
		parent := w.path[len(w.path)-1]
		parent.low = min(parent.low, frame.low)
		parent.ids = npmAdvisoryUnion(parent.ids, frame.ids)
	}
}

// complete gives every member of a strongly connected component the same complete advisory set.
func (w *npmAdvisoryWalk) complete(frame *npmAdvisoryFrame) {
	var ids []string
	for i := frame.base; i < len(w.active) && i < maxNodeAdvisories; i++ {
		ids = npmAdvisoryUnion(ids, w.active[i].ids)
	}
	for i := frame.base; i < len(w.active) && i < maxNodeAdvisories; i++ {
		w.memo[w.active[i].name] = ids
	}
	frame.ids = ids
	w.active = w.active[:frame.base]
}

// npmAdvisoryUnion deduplicates advisory sets without mutating slices shared by memo entries.
func npmAdvisoryUnion(left, right []string) []string {
	ids := make(map[string]bool, len(left)+len(right))
	for i := 0; i < len(left) && i < maxNodeAdvisories; i++ {
		ids[left[i]] = true
	}
	for i := 0; i < len(right) && i < maxNodeAdvisories; i++ {
		ids[right[i]] = true
	}
	return util.SortedKeys(ids)
}

// npmViaAdvisories collects direct advisory objects and schedules dependency-name references.
func npmViaAdvisories(via []json.RawMessage, ids, queue []string, budget *int) ([]string, []string, error) {
	*budget -= len(via)
	if *budget < 0 {
		return nil, nil, fmt.Errorf("npm audit via exceeds %d entries", maxNodeAdvisories)
	}
	for i := 0; i < len(via) && i < maxNodeAdvisories; i++ {
		dependency, id, err := npmViaEntry(via[i])
		if err != nil {
			return nil, nil, err
		}
		if dependency != "" {
			queue = append(queue, dependency)
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(queue) > maxNodeAdvisories || len(ids) > maxNodeAdvisories {
		return nil, nil, fmt.Errorf("npm audit traversal exceeds %d entries", maxNodeAdvisories)
	}
	return ids, queue, nil
}

// npmViaEntry distinguishes a dependency reference from a directly reported advisory.
func npmViaEntry(raw json.RawMessage) (string, string, error) {
	var dependency string
	if json.Unmarshal(raw, &dependency) == nil && dependency != "" {
		return dependency, "", nil
	}
	var advisory nodeAuditAdvisory
	if err := json.Unmarshal(raw, &advisory); err != nil {
		return "", "", fmt.Errorf("parse npm audit via: %w", err)
	}
	if advisory.Severity == "high" || advisory.Severity == "critical" {
		return "", nodeAuditGHSA("unknown GHSA", advisory), nil
	}
	return "", "", nil
}
