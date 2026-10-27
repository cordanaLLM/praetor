// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

// RequiredBPFLicenseExpression is the dual licence kernel-loaded BPF sources must carry (order engine-fr-rule-checks-20261027).
const (
	RequiredBPFLicenseExpression = "EUPL-1.2 OR GPL-2.0-only"
	GPL2LicenseTerm              = "GPL-2.0-only"
	maxBPFScanFiles              = 200000
)

// BPFLicenseOptions are the inputs of one CheckBPFLicenses run (order engine-fr-rule-checks-20261027).
type BPFLicenseOptions struct {
	Root           string
	DeclaredHolder string
}

// BPFLicenseReport is what CheckBPFLicenses found (order engine-fr-rule-checks-20261027).
type BPFLicenseReport struct {
	BPFFilesRead int
	Findings     []string
}

// CheckBPFLicenses enforces that kernel-loaded BPF sources are dual-licensed EUPL-1.2 OR GPL-2.0-only,
// and no other own file carries GPL-2.0-only (order engine-fr-rule-checks-20261027).
func CheckBPFLicenses(ctx context.Context, opts BPFLicenseOptions) (BPFLicenseReport, error) {
	if ctx == nil {
		return BPFLicenseReport{}, errors.New("BPF licence check requires a context")
	}
	if err := ctx.Err(); err != nil {
		return BPFLicenseReport{}, err
	}
	tables, err := loadReuseTables(ctx, opts.Root)
	if err != nil {
		return BPFLicenseReport{}, err
	}
	files, err := repoFiles(ctx, opts.Root)
	if err != nil {
		return BPFLicenseReport{}, err
	}
	rep := BPFLicenseReport{}
	declaredName := HolderName(opts.DeclaredHolder)
	for i := 0; i < len(files) && i < maxBPFScanFiles; i++ {
		if err := ctx.Err(); err != nil {
			return BPFLicenseReport{}, err
		}
		rel := files[i]
		finding, isBPF, err := auditBPFFile(opts.Root, rel, declaredName, tables)
		if err != nil {
			return BPFLicenseReport{}, err
		}
		if isBPF {
			rep.BPFFilesRead++
		}
		if finding != "" {
			rep.Findings = append(rep.Findings, finding)
		}
	}
	slices.Sort(rep.Findings)
	return rep, nil
}

// loadReuseTables loads REUSE.toml annotations if the file exists.
func loadReuseTables(ctx context.Context, root string) ([]ReuseAnnotation, error) {
	reusePath := filepath.Join(root, ReuseFile)
	data, exists, err := contextopt.ObserveSnapshot(ctx, reusePath)
	if err != nil || !exists {
		return nil, err
	}
	return ReuseAnnotationTables(string(data))
}

// auditBPFFile inspects one file for BPF compliance and GPL-2.0-only restrictions.
func auditBPFFile(root, rel, declaredName string, tables []ReuseAnnotation) (finding string, isBPF bool, err error) {
	absPath := filepath.Join(root, filepath.FromSlash(rel))
	data, err := util.ReadFileLimited(absPath, hiss.MaxScanFileSize)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, util.ErrNotRegularFile) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", rel, err)
	}
	bpf := isKernelBPF(rel, data)
	ident := extractLicenseIdentifier(data)
	if bpf {
		if ident != RequiredBPFLicenseExpression {
			return fmt.Sprintf("%s: kernel-loaded BPF source must carry SPDX-License-Identifier: %s", rel, RequiredBPFLicenseExpression), true, nil
		}
		return "", true, nil
	}
	if isExcludedFromGPLCheck(rel, data, declaredName, tables) {
		return "", false, nil
	}
	if fileCarriesGPL(ident, rel, tables) {
		return fmt.Sprintf("%s: own file carries %s licence", rel, GPL2LicenseTerm), false, nil
	}
	return "", false, nil
}

// isKernelBPF reports whether rel is a kernel-loaded BPF source: name ends in .bpf.c or .bpf.h,
// or C source includes bpf_helpers.h or vmlinux.h and holds the SEC(" marker (order engine-fr-rule-checks-20261027).
func isKernelBPF(rel string, content []byte) bool {
	if strings.HasSuffix(rel, ".bpf.c") || strings.HasSuffix(rel, ".bpf.h") {
		return true
	}
	ext := filepath.Ext(rel)
	if ext != ".c" && ext != ".h" {
		return false
	}
	hasInclude := bytes.Contains(content, []byte("bpf_helpers.h")) || bytes.Contains(content, []byte("vmlinux.h"))
	return hasInclude && bytes.Contains(content, []byte("SEC(\""))
}

// isExcludedFromGPLCheck checks whether rel is exempt from non-BPF GPL check.
func isExcludedFromGPLCheck(rel string, content []byte, declaredName string, tables []ReuseAnnotation) bool {
	if strings.HasPrefix(rel, LicensesDir+"/") || rel == LicensesDir {
		return true
	}
	if strings.HasPrefix(rel, "vendor/") || strings.Contains(rel, "/vendor/") ||
		strings.HasPrefix(rel, "third_party/") || strings.Contains(rel, "/third_party/") {
		return true
	}
	if isUnderOtherHolderAnnotation(rel, tables, declaredName) {
		return true
	}
	return fileHolderOtherThan(content, declaredName)
}

// isUnderOtherHolderAnnotation checks if REUSE annotations assign this path to another holder.
func isUnderOtherHolderAnnotation(rel string, tables []ReuseAnnotation, declaredName string) bool {
	for i := len(tables) - 1; i >= 0; i-- {
		ann := tables[i]
		if !matchesAnnotation(ann.Paths, rel) {
			continue
		}
		if hasOtherHolder(ann.Copyrights, declaredName) {
			return true
		}
	}
	return false
}

// matchesAnnotation checks if any path glob matches rel.
func matchesAnnotation(paths []string, rel string) bool {
	for j := 0; j < len(paths) && j < MaxReuseLines; j++ {
		if reuseGlobOverlaps(paths[j], rel) {
			return true
		}
	}
	return false
}

// hasOtherHolder checks if any copyright text names a holder other than declaredName.
func hasOtherHolder(copyrights []string, declaredName string) bool {
	if declaredName == "" {
		return len(copyrights) > 0
	}
	for i := 0; i < len(copyrights) && i < MaxReuseLines; i++ {
		_, holderPart := SplitYearAndHolder(copyrights[i])
		if name := HolderName(holderPart); name != "" && name != declaredName {
			return true
		}
	}
	return false
}

// fileHolderOtherThan checks if the file header's copyright names a holder other than declaredName.
func fileHolderOtherThan(content []byte, declaredName string) bool {
	if declaredName == "" {
		return false
	}
	lines := strings.Split(string(content), "\n")
	for i := 0; i < len(lines) && i < 100; i++ {
		idx := strings.Index(lines[i], SPDXFileCopyrightTextTag)
		if idx == -1 {
			continue
		}
		raw := cleanHolderComment(lines[i][idx+len(SPDXFileCopyrightTextTag):])
		_, holderPart := SplitYearAndHolder(raw)
		if name := HolderName(holderPart); name != "" && name != declaredName {
			return true
		}
	}
	return false
}

// fileCarriesGPL checks if the file's identifier or REUSE annotations name GPL-2.0-only.
func fileCarriesGPL(ident, rel string, tables []ReuseAnnotation) bool {
	if ident != "" {
		return slices.Contains(licenseTerms(ident), GPL2LicenseTerm)
	}
	for i := len(tables) - 1; i >= 0; i-- {
		if !matchesAnnotation(tables[i].Paths, rel) {
			continue
		}
		for j := 0; j < len(tables[i].Licenses) && j < MaxReuseLines; j++ {
			if slices.Contains(licenseTerms(tables[i].Licenses[j]), GPL2LicenseTerm) {
				return true
			}
		}
		break
	}
	return false
}

// extractLicenseIdentifier reads SPDX-License-Identifier from the file header.
func extractLicenseIdentifier(content []byte) string {
	lines := strings.Split(string(content), "\n")
	for i := 0; i < len(lines) && i < 100; i++ {
		line := lines[i]
		idx := strings.Index(line, "SPDX-License-Identifier:")
		if idx != -1 {
			expr := line[idx+len("SPDX-License-Identifier:"):]
			expr = strings.TrimSpace(expr)
			expr = strings.TrimSuffix(expr, "*/")
			expr = strings.TrimSuffix(expr, "-->")
			return strings.TrimSpace(expr)
		}
	}
	return ""
}
