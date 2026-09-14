package hiss

import (
	"errors"
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

// Validate checks bounded counter consistency, not the authenticity of recorded
// observations. A nil receiver represents unknown historical coverage.
func (c *ScanCoverage) Validate() error {
	if c == nil {
		return nil
	}
	if err := c.validateCounts(); err != nil {
		return err
	}
	// Subtract from the declared total so malicious counters cannot overflow a sum.
	remaining := c.UnscannedFiles - c.UnlistedUnscannedFiles
	for ext, count := range c.UnscannedByExtension {
		if len(ext) > maxCoverageExtensionBytes {
			return errors.New("coverage extension exceeds its byte bound")
		}
		if count < 0 || count > remaining {
			return errors.New("coverage extension counts contradict the unscanned total")
		}
		remaining -= count
	}
	if remaining != 0 {
		return errors.New("coverage extension counts contradict the unscanned total")
	}
	return nil
}

func (c *ScanCoverage) validateCounts() error {
	if c.FilesRead < 0 || c.UnscannedFiles < 0 || c.UnlistedUnscannedFiles < 0 {
		return errors.New("coverage counts must be nonnegative")
	}
	if c.UnlistedUnscannedFiles > c.UnscannedFiles {
		return errors.New("coverage unlisted count exceeds the unscanned total")
	}
	if len(c.UnscannedByExtension) > maxCoverageExtensions {
		return errors.New("coverage extension map exceeds its entry bound")
	}
	return nil
}

// CoverageEvidence summarizes recorded scope for text consumers. A successful
// file read establishes input coverage, not complete analysis or a passing test.
func (r *ScanReport) CoverageEvidence() string {
	evidence := "HISS file coverage was not recorded; source coverage is unknown."
	if r == nil {
		return evidence
	}
	if c := r.Coverage; c != nil {
		evidence = fmt.Sprintf("HISS file scope: %d files read by supported scanners; %d files outside supported extensions; %d ignored directories, %d symlinks and %d oversized inputs skipped. Native application tests are separate.",
			c.FilesRead, c.UnscannedFiles, r.Skips.DirCount, r.Skips.Symlinks, r.Skips.Oversize)
	}
	if r.Skips.Unparsed > 0 {
		evidence += fmt.Sprintf(" %d file(s) yielded no analyzable structure and were never examined by any rule.", r.Skips.Unparsed)
	}
	if r.Truncated {
		evidence += " Scan was truncated; file scope is partial."
	}
	return evidence
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
