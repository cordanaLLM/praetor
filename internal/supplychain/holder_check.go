// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Bounds for copyright holder scan (HISS-02, order engine-fr-rule-checks-20261027).
const (
	maxHolderScanFiles = 200000
	maxHolderFileLines = 100000
	// SPDXFileCopyrightTextTag is the comment tag a file states its copyright holder with.
	SPDXFileCopyrightTextTag = "SPDX-FileCopyrightText:"
)

var yearPrefixRe = regexp.MustCompile(`^(?:(?:Copyright\s+)?(?:\([Cc]\)\s*)?)?([0-9]{4}(?:\s*[-–,]\s*[0-9]{4})*)\s+(.*)$`)

// CopyrightHolderOptions are the inputs of one CheckCopyrightHolders run (order engine-fr-rule-checks-20261027).
type CopyrightHolderOptions struct {
	Root            string
	CopyrightHolder string
}

// CopyrightHolderReport is what CheckCopyrightHolders found (order engine-fr-rule-checks-20261027).
type CopyrightHolderReport struct {
	Skipped  string
	Holder   string
	Findings []string
}

// HolderName extracts the rights holder name from a copyright holder declaration or line:
// the text before '<', case-insensitive and trimmed (order engine-fr-rule-checks-20261027).
func HolderName(holder string) string {
	if idx := strings.Index(holder, "<"); idx != -1 {
		holder = holder[:idx]
	}
	return strings.ToLower(strings.TrimSpace(holder))
}

// SplitYearAndHolder splits an SPDX copyright text into its leading year/range and the holder portion.
func SplitYearAndHolder(text string) (yearPart, holderPart string) {
	trimmed := strings.TrimSpace(text)
	match := yearPrefixRe.FindStringSubmatch(trimmed)
	if match == nil {
		return "", trimmed
	}
	return strings.TrimSpace(match[1]), strings.TrimSpace(match[2])
}

// CheckCopyrightHolders checks that every own copyright line matches the declared holder in exact form.
func CheckCopyrightHolders(ctx context.Context, opts CopyrightHolderOptions) (CopyrightHolderReport, error) {
	if ctx == nil {
		return CopyrightHolderReport{}, errors.New("copyright holder check requires a context")
	}
	if err := ctx.Err(); err != nil {
		return CopyrightHolderReport{}, err
	}
	declared := strings.TrimSpace(opts.CopyrightHolder)
	if declared == "" {
		return CopyrightHolderReport{Skipped: ".standards.yaml declares no licensing.copyright_holder"}, nil
	}
	declaredName := HolderName(declared)
	rep := CopyrightHolderReport{Holder: declared}
	reuseFindings, err := checkReuseTomlHolders(ctx, opts.Root, declared, declaredName)
	if err != nil {
		return CopyrightHolderReport{}, err
	}
	rep.Findings = append(rep.Findings, reuseFindings...)
	fileFindings, err := checkTrackedFileHolders(ctx, opts.Root, declared, declaredName)
	if err != nil {
		return CopyrightHolderReport{}, err
	}
	rep.Findings = append(rep.Findings, fileFindings...)
	slices.Sort(rep.Findings)
	return rep, nil
}

// checkReuseTomlHolders audits copyright lines in REUSE.toml comments and annotations.
func checkReuseTomlHolders(ctx context.Context, root, declared, declaredName string) ([]string, error) {
	reusePath := filepath.Join(root, ReuseFile)
	data, exists, err := contextopt.ObserveSnapshot(ctx, reusePath)
	if err != nil || !exists {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	var findings []string
	commentFindings := scanLinesForHolder(lines, declared, declaredName, ReuseFile)
	findings = append(findings, commentFindings...)
	tables, err := ReuseAnnotationTables(string(data))
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(tables) && i < MaxReuseLines; i++ {
		for j := 0; j < len(tables[i].Copyrights) && j < MaxReuseLines; j++ {
			cText := strings.TrimSpace(tables[i].Copyrights[j])
			if finding := verifyHolderText(cText, declared, declaredName, ReuseFile, lines); finding != "" {
				findings = append(findings, finding)
			}
		}
	}
	return findings, nil
}

// verifyHolderText verifies one copyright text from a REUSE.toml annotation.
func verifyHolderText(cText, declared, declaredName, file string, lines []string) string {
	yearPart, holderPart := SplitYearAndHolder(cText)
	if HolderName(holderPart) != declaredName {
		return ""
	}
	if holderPart == declared {
		return ""
	}
	expected := declared
	if yearPart != "" {
		expected = yearPart + " " + declared
	}
	lineNum := findTextLine(lines, cText, holderPart)
	return fmt.Sprintf("%s:%d: found %q, expected %q", file, lineNum, cText, expected)
}

// findTextLine locates the 1-based line number containing target or fallback in lines.
func findTextLine(lines []string, target, fallback string) int {
	for i := 0; i < len(lines) && i < maxHolderFileLines; i++ {
		if strings.Contains(lines[i], target) || (fallback != "" && strings.Contains(lines[i], fallback)) {
			return i + 1
		}
	}
	return 1
}

// scanLinesForHolder checks lines for SPDX-FileCopyrightText: comments.
func scanLinesForHolder(lines []string, declared, declaredName, file string) []string {
	var findings []string
	for i := 0; i < len(lines) && i < maxHolderFileLines; i++ {
		line := lines[i]
		idx := strings.Index(line, SPDXFileCopyrightTextTag)
		if idx == -1 {
			continue
		}
		raw := cleanHolderComment(line[idx+len(SPDXFileCopyrightTextTag):])
		yearPart, holderPart := SplitYearAndHolder(raw)
		if HolderName(holderPart) != declaredName || holderPart == declared {
			continue
		}
		expected := declared
		if yearPart != "" {
			expected = yearPart + " " + declared
		}
		findings = append(findings, fmt.Sprintf("%s:%d: found %q, expected %q", file, i+1, raw, expected))
	}
	return findings
}

// cleanHolderComment trims comment syntax from a copyright line.
func cleanHolderComment(text string) string {
	t := strings.TrimSpace(text)
	t = strings.TrimSuffix(t, "*/")
	t = strings.TrimSuffix(t, "-->")
	return strings.TrimSpace(t)
}

// checkTrackedFileHolders audits SPDX-FileCopyrightText in tracked own files outside LICENSES/ and .reuse/.
func checkTrackedFileHolders(ctx context.Context, root, declared, declaredName string) ([]string, error) {
	files, err := repoFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	var findings []string
	for i := 0; i < len(files) && i < maxHolderScanFiles; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rel := files[i]
		if rel == ReuseFile || strings.HasPrefix(rel, LicensesDir+"/") || strings.HasPrefix(rel, ".reuse/") {
			continue
		}
		fileFindings, err := checkSingleFileHolder(root, rel, declared, declaredName)
		if err != nil {
			return nil, err
		}
		findings = append(findings, fileFindings...)
	}
	return findings, nil
}

// checkSingleFileHolder reads one regular file and scans its lines for holder mismatches.
func checkSingleFileHolder(root, rel, declared, declaredName string) ([]string, error) {
	absPath := filepath.Join(root, filepath.FromSlash(rel))
	data, err := util.ReadFileLimited(absPath, hiss.MaxScanFileSize)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, util.ErrNotRegularFile) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	lines := strings.Split(string(data), "\n")
	return scanLinesForHolder(lines, declared, declaredName, rel), nil
}

// repoFiles returns tracked or discovered files below root.
func repoFiles(ctx context.Context, root string) ([]string, error) {
	visible := hiss.GitVisiblePaths(ctx, root)
	if visible != nil {
		return visible.Files(), nil
	}
	return walkRepoFiles(ctx, root)
}

// isWalkIgnoredDir reports whether dir name should be skipped.
func isWalkIgnoredDir(name string) bool {
	return name == ".git" || name == ".workingdir" || util.IsToolchainTreeDir(name) || util.IsScratchDir(name)
}

// walkRepoFiles walks the root directory when git is not available (e.g. offline temp dirs).
func walkRepoFiles(ctx context.Context, root string) ([]string, error) {
	var files []string
	walker := func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if isWalkIgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || len(files) >= maxHolderScanFiles {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	}
	if err := filepath.WalkDir(root, walker); err != nil {
		return nil, err
	}
	slices.Sort(files)
	return files, nil
}
