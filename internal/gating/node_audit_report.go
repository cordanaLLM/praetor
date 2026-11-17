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
	for i := 0; i < len(keys) && i < maxNodeAdvisories; i++ {
		if !findings.add(packages[keys[i]].Severity) {
			continue
		}
		ids, err := npmPackageAdvisories(packages, keys[i], &budget)
		if err != nil {
			return findings, err
		}
		findings.ids = append(findings.ids, ids...)
		if len(findings.ids) > maxNodeAdvisories {
			return findings, fmt.Errorf("node audit exceeds %d advisory references", maxNodeAdvisories)
		}
	}
	return findings, nil
}

// npmPackageAdvisories follows npm's via references iteratively, including transitive packages.
func npmPackageAdvisories(packages map[string]nodeAuditVulnerability, name string, budget *int) ([]string, error) {
	queue := []string{name}
	seen := make(map[string]bool)
	var ids []string
	for i := 0; i < len(queue) && i < maxNodeAdvisories; i++ {
		*budget -= 1
		if *budget < 0 {
			return nil, fmt.Errorf("npm audit traversal exceeds %d steps", maxNodeAdvisories)
		}
		if seen[queue[i]] {
			continue
		}
		seen[queue[i]] = true
		item, ok := packages[queue[i]]
		if !ok {
			return nil, fmt.Errorf("npm audit via references missing package %s", queue[i])
		}
		var err error
		ids, queue, err = npmViaAdvisories(item.Via, ids, queue, budget)
		if err != nil {
			return nil, err
		}
	}
	if len(queue) > maxNodeAdvisories || len(ids) == 0 {
		return nil, fmt.Errorf("npm audit high or critical package %s has no bounded GHSA advisory", name)
	}
	return ids, nil
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
