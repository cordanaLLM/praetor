// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
)

const (
	maxGosecIssues   = 10000
	gosecScanTimeout = 10 * time.Minute
)

type gosecJSONIssue struct {
	RuleID  string `json:"rule_id"`
	File    string `json:"file"`
	Line    string `json:"line"`
	Details string `json:"details"`
}

type gosecJSONReport struct {
	Issues []gosecJSONIssue `json:"Issues"`
}

// parseGosecOutput converts gosec JSON output into keyed baseline infractions.
func parseGosecOutput(repoDir, moduleDir, out string) ([]baseline.Infraction, error) {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil, fmt.Errorf("%s: empty gosec report", stageSecurity)
	}
	var rep gosecJSONReport
	if err := json.Unmarshal([]byte(trimmed), &rep); err != nil {
		return nil, fmt.Errorf("%s: parse gosec JSON report: %w", stageSecurity, err)
	}
	if len(rep.Issues) == 0 {
		return []baseline.Infraction{}, nil
	}
	infractions, err := buildGosecInfractions(repoDir, moduleDir, rep.Issues)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", stageSecurity, err)
	}
	baseline.AssignFingerprints(infractions)
	return infractions, nil
}

func buildGosecInfractions(repoDir, moduleDir string, issues []gosecJSONIssue) ([]baseline.Infraction, error) {
	fileCache := make(map[string][]byte)
	limit := len(issues)
	if limit > maxGosecIssues {
		limit = maxGosecIssues
	}
	infractions := make([]baseline.Infraction, 0, limit)
	for i := 0; i < limit; i++ {
		iss := issues[i]
		line, err := parseGosecLine(iss.Line)
		if err != nil {
			return nil, fmt.Errorf("parse issue line %q in %s: %w", iss.Line, iss.File, err)
		}
		rel, abs := resolveIssuePaths(repoDir, moduleDir, iss.File)
		src, err := readCachedSource(fileCache, abs)
		if err != nil {
			return nil, fmt.Errorf("read %s for issue %s: %w", rel, iss.RuleID, err)
		}
		anchor := hiss.AnchorGoSource(src, line)
		infractions = append(infractions, baseline.Infraction{
			RuleID:     iss.RuleID,
			FilePath:   rel,
			LineNumber: line,
			Message:    iss.Details,
			Anchor:     anchor,
		})
	}
	return infractions, nil
}

// readCachedSource reads each reported file once per scan; a file gosec reported but that
// cannot be read fails the scan, so no finding is anchored against an empty source.
func readCachedSource(cache map[string][]byte, absPath string) ([]byte, error) {
	if data, ok := cache[absPath]; ok {
		return data, nil
	}
	data, err := util.ReadFileLimited(absPath, hiss.MaxScanFileSize)
	if err != nil {
		return nil, err
	}
	cache[absPath] = data
	return data, nil
}

func parseGosecLine(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, fmt.Errorf("missing line number")
	}
	parts := strings.Split(trimmed, "-")
	if len(parts) > 2 {
		return 0, fmt.Errorf("invalid line number %q", raw)
	}
	first, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || first <= 0 {
		return 0, fmt.Errorf("invalid line number %q", raw)
	}
	if len(parts) == 2 {
		second, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || second < first {
			return 0, fmt.Errorf("invalid line range %q", raw)
		}
	}
	return first, nil
}

// resolveIssuePaths keys a gosec finding by its path relative to the repository root. gosec reports absolute paths,
// and Rel of an absolute path against a relative root (the pre-push hook's --path=.) kept it absolute, so the key held
// one checkout's location and matched no baseline recorded elsewhere (#1453): both sides are made absolute first.
func resolveIssuePaths(repoDir, moduleDir, file string) (relPath, absPath string) {
	if filepath.IsAbs(file) {
		absPath = filepath.Clean(file)
	} else {
		absPath = filepath.Clean(filepath.Join(moduleDir, file))
	}
	if abs, err := filepath.Abs(absPath); err == nil {
		absPath = abs
	}
	root, err := filepath.Abs(repoDir)
	if err != nil {
		root = repoDir
	}
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		rel = file
	}
	return baseline.NormalizePath(rel), absPath
}

// filterBaselineForModule extracts the subset of baselined infractions that belong to moduleDir.
func filterBaselineForModule(base *baseline.Baseline, repoDir, moduleDir string, allModules []goModule) *baseline.Baseline {
	if base == nil || base.Absent {
		return &baseline.Baseline{Absent: true, Version: 1, Infractions: []baseline.Infraction{}}
	}
	relMod, err := filepath.Rel(repoDir, moduleDir)
	if err != nil {
		relMod = "."
	}
	relMod = baseline.NormalizePath(relMod)
	filtered := make([]baseline.Infraction, 0, len(base.Infractions))
	for i := 0; i < len(base.Infractions) && i < maxGosecIssues; i++ {
		inf := base.Infractions[i]
		if moduleHoldsInfraction(relMod, inf.FilePath, repoDir, allModules) {
			filtered = append(filtered, inf)
		}
	}
	return &baseline.Baseline{
		Version:           base.Version,
		Repository:        base.Repository,
		CommitSHA:         base.CommitSHA,
		TotalInfractions:  len(filtered),
		IncreaseRationale: base.IncreaseRationale,
		Infractions:       filtered,
		Absent:            base.Absent,
	}
}

// pathInModule reports whether normFile lies within the non-root module directory relMod.
func pathInModule(normFile, relMod string) bool {
	if relMod == "." || relMod == "" {
		return false
	}
	return normFile == relMod || strings.HasPrefix(normFile, relMod+"/")
}

func moduleHoldsInfraction(relMod, filePath, repoDir string, allModules []goModule) bool {
	normFile := baseline.NormalizePath(filePath)
	if relMod != "." && relMod != "" {
		return pathInModule(normFile, relMod)
	}
	for i := 0; i < len(allModules) && i < apiassets.MaxTrackedModules; i++ {
		subRel, err := filepath.Rel(repoDir, allModules[i].dir)
		if err != nil {
			continue
		}
		if pathInModule(normFile, baseline.NormalizePath(subRel)) {
			return false
		}
	}
	return true
}

// ScanGosec scans all Go modules in repoDir for gosec findings and returns them keyed and anchored.
func ScanGosec(ctx context.Context, repoDir string) ([]baseline.Infraction, bool, error) {
	files, err := apiassets.TrackedModuleFiles(ctx, repoDir)
	if err != nil {
		return nil, false, fmt.Errorf("list tracked Go modules in %s: %w", repoDir, err)
	}
	if len(files) == 0 {
		if util.FileExists(filepath.Join(repoDir, "go.mod")) {
			files = []string{"go.mod"}
		} else {
			return nil, false, nil
		}
	}
	if _, err := exec.LookPath("gosec"); err != nil {
		return nil, true, fmt.Errorf("gosec is required: %w", err)
	}
	confPath := filepath.Join(repoDir, GosecConfigFile)
	if !util.FileExists(confPath) {
		return nil, true, fmt.Errorf("%s is missing: gosec requires its pinned configuration", GosecConfigFile)
	}
	return scanDiscoveredModules(ctx, repoDir, files, confPath)
}

func scanDiscoveredModules(ctx context.Context, repoDir string, files []string, confPath string) ([]baseline.Infraction, bool, error) {
	var allFindings []baseline.Infraction
	for i := 0; i < len(files) && i < apiassets.MaxTrackedModules; i++ {
		modDir := filepath.Join(repoDir, filepath.Dir(filepath.FromSlash(files[i])))
		findings, err := scanModuleGosec(ctx, repoDir, modDir, confPath)
		if err != nil {
			return nil, true, err
		}
		allFindings = append(allFindings, findings...)
	}
	baseline.AssignFingerprints(allFindings)
	return allFindings, true, nil
}

func scanModuleGosec(ctx context.Context, repoDir, modDir, confPath string) ([]baseline.Infraction, error) {
	scanCtx, cancel := context.WithTimeout(ctx, gosecScanTimeout)
	defer cancel()
	listing, err := util.RunCommand(scanCtx, modDir, "go", "list", "-f", "{{.Dir}}", "./...")
	if err != nil {
		return nil, fmt.Errorf("list Go packages in %s: %w", modDir, err)
	}
	if modulePackageCount(listing) == 0 {
		return nil, nil
	}
	packages, err := resolveSecurityPackages(modDir, listing)
	if err != nil || len(packages) == 0 {
		return nil, err
	}
	confRel, err := filepath.Rel(modDir, confPath)
	if err != nil {
		confRel = confPath
	}
	args := append([]string{"-fmt=json", "-conf", confRel}, packages...)
	out, err := util.RunCommand(scanCtx, modDir, "gosec", args...)
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("run gosec in %s: %w", modDir, err)
	}
	return parseGosecOutput(repoDir, modDir, out)
}
