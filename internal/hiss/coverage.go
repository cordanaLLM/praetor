package hiss

import (
	"fmt"
	"path/filepath"
	"strings"
)

const maxCoverageExtensions = 32
const maxCoverageExtensionBytes = 32

// ScanCoverage records observed file scope, not a claim of language correctness.
// FilesRead counts bounded reads dispatched to supported scanners, including a
// file that made the report Truncated. Unscanned files include documentation and
// configuration as well as unsupported source languages. Ignored directories,
// supported-file symlinks and oversized inputs are accounted for in ScanSkips.
type ScanCoverage struct {
	FilesRead              int            `json:"files_read"`
	UnscannedFiles         int            `json:"unscanned_files"`
	UnscannedByExtension   map[string]int `json:"unscanned_by_extension"`
	UnlistedUnscannedFiles int            `json:"unlisted_unscanned_files"`
}

// CoverageEvidence summarizes recorded scope for text consumers. A successful
// file read establishes input coverage, not complete analysis or a passing test.
func (r *ScanReport) CoverageEvidence() string {
	if r == nil || r.Coverage == nil {
		return "HISS file coverage was not recorded; source coverage is unknown."
	}
	c := r.Coverage
	return fmt.Sprintf("HISS file scope: %d files read by supported scanners; %d files outside supported extensions; %d ignored directories, %d symlinks and %d oversized inputs skipped. Native application tests are separate.",
		c.FilesRead, c.UnscannedFiles, r.Skips.DirCount, r.Skips.Symlinks, r.Skips.Oversize)
}

func (c *ScanCoverage) recordUnscanned(path string) {
	c.UnscannedFiles++
	ext := strings.ToLower(filepath.Ext(path))
	if len(ext) > maxCoverageExtensionBytes {
		c.UnlistedUnscannedFiles++
		return
	}
	if _, known := c.UnscannedByExtension[ext]; !known && len(c.UnscannedByExtension) == maxCoverageExtensions {
		c.UnlistedUnscannedFiles++
		return
	}
	c.UnscannedByExtension[ext]++
}
