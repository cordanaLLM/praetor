// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"bytes"
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
	GHSA            string `json:"github_advisory_id"`
	URL             string `json:"url"`
	Severity        string `json:"severity"`
	null            bool
	missingSeverity bool
}

// UnmarshalJSON records null advisory objects and missing severity fields for fail-closed validation.
func (a *nodeAuditAdvisory) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*a = nodeAuditAdvisory{null: true}
		return nil
	}
	var raw struct {
		GHSA     string  `json:"github_advisory_id"`
		URL      string  `json:"url"`
		Severity *string `json:"severity"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*a = nodeAuditAdvisory{GHSA: raw.GHSA, URL: raw.URL, missingSeverity: raw.Severity == nil}
	if raw.Severity != nil {
		a.Severity = *raw.Severity
	}
	return nil
}

type nodeAuditVulnerability struct {
	Severity        string            `json:"severity"`
	Via             []json.RawMessage `json:"via"`
	null            bool
	missingSeverity bool
}

// UnmarshalJSON records null package objects and missing severity fields for fail-closed validation.
func (v *nodeAuditVulnerability) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*v = nodeAuditVulnerability{null: true}
		return nil
	}
	var raw struct {
		Severity *string           `json:"severity"`
		Via      []json.RawMessage `json:"via"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*v = nodeAuditVulnerability{Via: raw.Via, missingSeverity: raw.Severity == nil}
	if raw.Severity != nil {
		v.Severity = *raw.Severity
	}
	return nil
}

// validateNodeAuditSeverity enforces the single severity allow-list on an advisory, package or via object.
func validateNodeAuditSeverity(kind, name, severity string, nullObj, missing bool) error {
	if nullObj {
		return fmt.Errorf("%s %s is null", kind, name)
	}
	if missing {
		return fmt.Errorf("%s %s is missing severity", kind, name)
	}
	if severity == "" {
		return fmt.Errorf("%s %s has empty severity", kind, name)
	}
	switch severity {
	case "info", "low", "moderate", "high", "critical":
		return nil
	default:
		return fmt.Errorf("%s %s has unknown severity %q", kind, name, severity)
	}
}

type nodeAuditFindings struct {
	ids      []string
	seen     map[string]string
	high     int
	critical int
}

// npmAdvisoryRank maps high and critical severities to their required reachability level.
func npmAdvisoryRank(severity string) int {
	switch severity {
	case "high":
		return 1
	case "critical":
		return 2
	default:
		return 0
	}
}

// recordNodeAdvisorySeverity checks per-advisory severity consistency and reports whether id is new.
func recordNodeAdvisorySeverity(seen map[string]string, id, severity string) (bool, error) {
	if prev, ok := seen[id]; ok {
		if prev != severity {
			return false, fmt.Errorf("node audit advisory %s has conflicting severities %q and %q", id, prev, severity)
		}
		return false, nil
	}
	seen[id] = severity
	return true, nil
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
		return listedPnpmFindings(report.Advisories)
	}
	return listedNpmFindings(report.Vulnerabilities)
}

// listedPnpmFindings counts advisory severities independently of metadata totals.
func listedPnpmFindings(advisories map[string]nodeAuditAdvisory) (nodeAuditFindings, error) {
	if len(advisories) > maxNodeAdvisories {
		return nodeAuditFindings{}, fmt.Errorf("node audit exceeds %d listed entries", maxNodeAdvisories)
	}
	findings := nodeAuditFindings{seen: make(map[string]string, len(advisories))}
	keys := util.SortedKeys(advisories)
	for i := 0; i < len(keys) && i < maxNodeAdvisories; i++ {
		key := keys[i]
		advisory := advisories[key]
		if err := validateNodeAuditSeverity("node audit advisory", key, advisory.Severity, advisory.null, advisory.missingSeverity); err != nil {
			return nodeAuditFindings{}, err
		}
		id := nodeAuditGHSA(key, advisory)
		fresh, err := recordNodeAdvisorySeverity(findings.seen, id, advisory.Severity)
		if err != nil {
			return nodeAuditFindings{}, err
		}
		if fresh && findings.add(advisory.Severity) {
			findings.ids = append(findings.ids, id)
		}
	}
	return findings, nil
}

// listedNpmFindings counts packages and requires matching advisory reachability for every high or critical finding.
func listedNpmFindings(packages map[string]nodeAuditVulnerability) (nodeAuditFindings, error) {
	if len(packages) > maxNodeAdvisories {
		return nodeAuditFindings{}, fmt.Errorf("node audit exceeds %d listed entries", maxNodeAdvisories)
	}
	var findings nodeAuditFindings
	keys := util.SortedKeys(packages)
	budget := maxNodeAdvisories
	walk := newNpmAdvisoryWalk(packages, &budget)
	for i := 0; i < len(keys) && i < maxNodeAdvisories; i++ {
		name := keys[i]
		reach, err := walk.advisories(name)
		if err != nil {
			return findings, err
		}
		severity := packages[name].Severity
		if findings.add(severity) && reach < npmAdvisoryRank(severity) {
			return findings, fmt.Errorf("npm audit high or critical package %s has no bounded GHSA advisory", name)
		}
	}
	findings.seen = walk.seen
	findings.ids = walk.highOrCriticalIDs()
	return findings, nil
}

// npmAdvisoryFrame holds one package's iterative depth-first traversal state.
type npmAdvisoryFrame struct {
	name                          string
	dependencies                  []string
	index, low, base, next, reach int
}

// npmAdvisoryWalk retains one report-wide advisory severity map and component reachability rank.
type npmAdvisoryWalk struct {
	packages map[string]nodeAuditVulnerability
	memo     map[string]int
	seen     map[string]string
	budget   *int
	nodes    map[string]*npmAdvisoryFrame
	path     []*npmAdvisoryFrame
	active   []*npmAdvisoryFrame
}

// newNpmAdvisoryWalk initializes traversal state shared across report starts.
func newNpmAdvisoryWalk(packages map[string]nodeAuditVulnerability, budget *int) npmAdvisoryWalk {
	return npmAdvisoryWalk{packages: packages, memo: make(map[string]int), seen: make(map[string]string), budget: budget, nodes: make(map[string]*npmAdvisoryFrame)}
}

// storedAdvisoryEntries exposes retained advisory storage for bounded-walk tests.
func (w *npmAdvisoryWalk) storedAdvisoryEntries() int {
	return len(w.seen)
}

// highOrCriticalIDs returns sorted high and critical advisory IDs collected during the walk.
func (w *npmAdvisoryWalk) highOrCriticalIDs() []string {
	keys := util.SortedKeys(w.seen)
	ids := make([]string, 0, len(keys))
	for i := 0; i < len(keys) && i < maxNodeAdvisories; i++ {
		if npmAdvisoryRank(w.seen[keys[i]]) > 0 {
			ids = append(ids, keys[i])
		}
	}
	return ids
}

// advisories collects each advisory once and memoizes reachability rank through complete cycles.
func (w *npmAdvisoryWalk) advisories(name string) (int, error) {
	if reach, ok := w.memo[name]; ok {
		return reach, nil
	}
	if err := w.push(name); err != nil {
		return 0, err
	}
	for step := 0; len(w.path) > 0 && step < maxNodeAdvisories; step++ {
		frame := w.path[len(w.path)-1]
		if frame.next < len(frame.dependencies) {
			dependency := frame.dependencies[frame.next]
			frame.next++
			if err := w.follow(frame, dependency); err != nil {
				return 0, err
			}
			continue
		}
		w.finish(frame)
	}
	if len(w.path) > 0 {
		return 0, fmt.Errorf("npm audit traversal exceeds %d steps", maxNodeAdvisories)
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
	if err := validateNodeAuditSeverity("npm audit package", name, item.Severity, item.null, item.missingSeverity); err != nil {
		return err
	}
	reach, dependencies, err := npmViaAdvisories(item.Via, w.seen, nil, w.budget)
	if err != nil {
		return err
	}
	index := len(w.nodes)
	frame := &npmAdvisoryFrame{name: name, reach: reach, dependencies: dependencies, index: index, low: index, base: len(w.active)}
	w.nodes[name] = frame
	w.path = append(w.path, frame)
	w.active = append(w.active, frame)
	return nil
}

// follow reuses completed reachability and records back edges without revisiting packages.
func (w *npmAdvisoryWalk) follow(frame *npmAdvisoryFrame, dependency string) error {
	if reach, ok := w.memo[dependency]; ok {
		frame.reach = max(frame.reach, reach)
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
		parent.reach = max(parent.reach, frame.reach)
	}
}

// complete gives every strongly connected component member the same advisory reachability rank.
func (w *npmAdvisoryWalk) complete(frame *npmAdvisoryFrame) {
	reach := 0
	for i := frame.base; i < len(w.active) && i < maxNodeAdvisories; i++ {
		reach = max(reach, w.active[i].reach)
	}
	for i := frame.base; i < len(w.active) && i < maxNodeAdvisories; i++ {
		w.memo[w.active[i].name] = reach
	}
	frame.reach = reach
	w.active = w.active[:frame.base]
}

// npmViaAdvisories collects direct advisory objects, checks per-ID severities and schedules dependencies.
func npmViaAdvisories(via []json.RawMessage, seen map[string]string, queue []string, budget *int) (int, []string, error) {
	*budget -= len(via)
	if *budget < 0 {
		return 0, nil, fmt.Errorf("npm audit via exceeds %d entries", maxNodeAdvisories)
	}
	reach := 0
	for i := 0; i < len(via) && i < maxNodeAdvisories; i++ {
		dependency, advisory, err := npmViaEntry(via[i])
		if err != nil {
			return 0, nil, err
		}
		if dependency != "" {
			queue = append(queue, dependency)
			continue
		}
		if seen != nil {
			if _, err := recordNodeAdvisorySeverity(seen, advisory.GHSA, advisory.Severity); err != nil {
				return 0, nil, err
			}
		}
		reach = max(reach, npmAdvisoryRank(advisory.Severity))
	}
	if len(queue) > maxNodeAdvisories {
		return 0, nil, fmt.Errorf("npm audit traversal exceeds %d entries", maxNodeAdvisories)
	}
	return reach, queue, nil
}

// npmViaEntry distinguishes a dependency reference from a directly reported advisory.
func npmViaEntry(raw json.RawMessage) (string, nodeAuditAdvisory, error) {
	if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var dependency string
		if json.Unmarshal(raw, &dependency) == nil {
			if dependency == "" {
				return "", nodeAuditAdvisory{}, fmt.Errorf("npm audit via entry has empty package reference")
			}
			return dependency, nodeAuditAdvisory{}, nil
		}
	}
	var advisory nodeAuditAdvisory
	if err := json.Unmarshal(raw, &advisory); err != nil {
		return "", nodeAuditAdvisory{}, fmt.Errorf("parse npm audit via: %w", err)
	}
	advisory.GHSA = nodeAuditGHSA("unknown GHSA", advisory)
	if err := validateNodeAuditSeverity("npm audit via", advisory.GHSA, advisory.Severity, advisory.null, advisory.missingSeverity); err != nil {
		return "", nodeAuditAdvisory{}, err
	}
	return "", advisory, nil
}
