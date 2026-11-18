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

// listedNpmFindings counts packages and requires advisory reachability for every high finding.
func listedNpmFindings(packages map[string]nodeAuditVulnerability) (nodeAuditFindings, error) {
	var findings nodeAuditFindings
	keys := util.SortedKeys(packages)
	budget := maxNodeAdvisories
	walk := newNpmAdvisoryWalk(packages, &budget)
	for i := 0; i < len(keys) && i < maxNodeAdvisories; i++ {
		if !findings.add(packages[keys[i]].Severity) {
			continue
		}
		reachable, err := walk.advisories(keys[i])
		if err != nil {
			return findings, err
		}
		if !reachable {
			return findings, fmt.Errorf("npm audit high or critical package %s has no bounded GHSA advisory", keys[i])
		}
	}
	findings.ids = util.SortedKeys(walk.ids)
	return findings, nil
}

// npmAdvisoryFrame holds one package's iterative depth-first traversal state.
type npmAdvisoryFrame struct {
	name                   string
	dependencies           []string
	index, low, base, next int
	reachable              bool
}

// npmAdvisoryWalk retains one report-wide advisory set and component reachability.
type npmAdvisoryWalk struct {
	packages map[string]nodeAuditVulnerability
	memo     map[string]bool
	ids      map[string]bool
	budget   *int
	nodes    map[string]*npmAdvisoryFrame
	path     []*npmAdvisoryFrame
	active   []*npmAdvisoryFrame
}

// newNpmAdvisoryWalk initializes traversal state shared across report starts.
func newNpmAdvisoryWalk(packages map[string]nodeAuditVulnerability, budget *int) npmAdvisoryWalk {
	return npmAdvisoryWalk{packages: packages, memo: make(map[string]bool), ids: make(map[string]bool), budget: budget, nodes: make(map[string]*npmAdvisoryFrame)}
}

// storedAdvisoryEntries exposes retained advisory storage for bounded-walk tests.
func (w *npmAdvisoryWalk) storedAdvisoryEntries() int {
	return len(w.ids)
}

// advisories collects each advisory once and memoizes reachability through complete cycles.
func (w *npmAdvisoryWalk) advisories(name string) (bool, error) {
	if reachable, ok := w.memo[name]; ok {
		return reachable, nil
	}
	if err := w.push(name); err != nil {
		return false, err
	}
	for step := 0; len(w.path) > 0 && step < maxNodeAdvisories; step++ {
		frame := w.path[len(w.path)-1]
		if frame.next < len(frame.dependencies) {
			dependency := frame.dependencies[frame.next]
			frame.next++
			if err := w.follow(frame, dependency); err != nil {
				return false, err
			}
			continue
		}
		w.finish(frame)
	}
	if len(w.path) > 0 {
		return false, fmt.Errorf("npm audit traversal exceeds %d steps", maxNodeAdvisories)
	}
	return w.memo[name], nil
}

// push parses and charges each unresolved package and its via entries once.
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
	for i := 0; i < len(ids) && i < maxNodeAdvisories; i++ {
		w.ids[ids[i]] = true
	}
	index := len(w.nodes)
	frame := &npmAdvisoryFrame{name: name, reachable: len(ids) > 0, dependencies: dependencies, index: index, low: index, base: len(w.active)}
	w.nodes[name] = frame
	w.path = append(w.path, frame)
	w.active = append(w.active, frame)
	return nil
}

// follow reuses completed reachability and records back edges without revisiting packages.
func (w *npmAdvisoryWalk) follow(frame *npmAdvisoryFrame, dependency string) error {
	if reachable, ok := w.memo[dependency]; ok {
		frame.reachable = frame.reachable || reachable
		return nil
	}
	if child, ok := w.nodes[dependency]; ok {
		frame.low = min(frame.low, child.index)
		return nil
	}
	return w.push(dependency)
}

// finish completes a package or cycle before propagating reachability to its parent.
func (w *npmAdvisoryWalk) finish(frame *npmAdvisoryFrame) {
	w.path = w.path[:len(w.path)-1]
	if frame.low == frame.index {
		w.complete(frame)
	}
	if len(w.path) > 0 {
		parent := w.path[len(w.path)-1]
		parent.low = min(parent.low, frame.low)
		parent.reachable = parent.reachable || frame.reachable
	}
}

// complete gives every strongly connected component member the same advisory reachability.
func (w *npmAdvisoryWalk) complete(frame *npmAdvisoryFrame) {
	reachable := false
	for i := frame.base; i < len(w.active) && i < maxNodeAdvisories; i++ {
		reachable = reachable || w.active[i].reachable
	}
	for i := frame.base; i < len(w.active) && i < maxNodeAdvisories; i++ {
		w.memo[w.active[i].name] = reachable
	}
	frame.reachable = reachable
	w.active = w.active[:frame.base]
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
