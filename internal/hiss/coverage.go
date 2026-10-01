package hiss

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const maxCoverageExtensions = 32
const maxCoverageExtensionBytes = 32

// maxCoverageLanguages bounds each per-language map of a coverage record (HISS-02).
const maxCoverageLanguages = 64

// ScanCoverage records observed file scope, not a claim of language correctness.
// FilesRead counts the files a language scanner examined, including a file that made the
// report Truncated. Unscanned files include documentation and configuration as well as
// unsupported source languages and files a scanner declined (minified output). Ignored
// directories, supported-file symlinks and oversized inputs are accounted for in ScanSkips.
type ScanCoverage struct {
	FilesRead              int            `json:"files_read"`
	UnscannedFiles         int            `json:"unscanned_files"`
	UnscannedByExtension   map[string]int `json:"unscanned_by_extension"`
	UnlistedUnscannedFiles int            `json:"unlisted_unscanned_files"`
	// LanguagesRead counts, per language (util.SourceLanguage), the files counted in FilesRead.
	// A missing map in a retained report means the languages were not recorded.
	LanguagesRead map[string]int `json:"languages_read,omitempty"`
	// UnscannedLanguages counts, per language (util.SourceLanguage), the unscanned files that
	// are program source: no rule ran over them, so the invariants are unverified there rather
	// than satisfied.
	UnscannedLanguages map[string]int `json:"unscanned_languages,omitempty"`
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
	if err := validateLanguageCounts(c.LanguagesRead, c.FilesRead); err != nil {
		return fmt.Errorf("coverage languages read: %w", err)
	}
	if err := validateLanguageCounts(c.UnscannedLanguages, c.UnscannedFiles); err != nil {
		return fmt.Errorf("coverage unscanned languages: %w", err)
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

// validateLanguageCounts checks one per-language map against the file total it breaks down.
// The map may cover fewer files than the total (documentation has no language, and languages
// past the entry bound are not listed) but never more.
func validateLanguageCounts(languages map[string]int, total int) error {
	if len(languages) > maxCoverageLanguages {
		return errors.New("map exceeds its entry bound")
	}
	remaining := total
	for language, count := range languages {
		if language == "" || len(language) > maxCoverageExtensionBytes {
			return errors.New("language name is empty or exceeds its byte bound")
		}
		if count < 0 || count > remaining {
			return errors.New("counts contradict the file total")
		}
		remaining -= count
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
		if unscanned := c.UnscannedSourceSummary(); unscanned != "" {
			evidence += " Source no HISS scanner examined: " + unscanned + "."
		}
	}
	if r.Skips.Irregular > 0 {
		evidence += fmt.Sprintf(" %d non-regular file(s) (FIFO, device or socket) were refused before any read.", r.Skips.Irregular)
	}
	if r.Skips.Unparsed > 0 {
		evidence += fmt.Sprintf(" %d file(s) yielded no analyzable structure and were never examined by any rule.", r.Skips.Unparsed)
	}
	if r.Truncated {
		evidence += " Scan was truncated; file scope is partial."
	}
	return evidence
}

// ScannedLanguageNames returns the languages of the files a scanner examined, sorted.
func (c *ScanCoverage) ScannedLanguageNames() []string {
	if c == nil {
		return nil
	}
	return sortedLanguages(c.LanguagesRead)
}

// UnscannedSourceSummary names every source language no scanner examined, with its file
// count, sorted by language: "java (3 files), shell (1 file)". It is empty when every source
// file was examined.
func (c *ScanCoverage) UnscannedSourceSummary() string {
	if c == nil {
		return ""
	}
	languages := sortedLanguages(c.UnscannedLanguages)
	parts := make([]string, 0, len(languages))
	for _, language := range languages {
		unit := "files"
		if c.UnscannedLanguages[language] == 1 {
			unit = "file"
		}
		parts = append(parts, fmt.Sprintf("%s (%d %s)", language, c.UnscannedLanguages[language], unit))
	}
	return strings.Join(parts, ", ")
}

func sortedLanguages(counts map[string]int) []string {
	languages := make([]string, 0, len(counts))
	for language := range counts {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	return languages
}

// recordUnscanned counts one unscanned file under the language its extension names, if any.
func (c *ScanCoverage) recordUnscanned(path string) {
	c.recordUnscannedAs(path, util.SourceLanguage(path))
}

// recordUnscannedAs counts one unscanned file whose source language is language, or "" for a
// file that is not program source. A file a scanner claimed and then declined is source even
// where its extension names no language: an extensionless shell script, an Ansible playbook.
func (c *ScanCoverage) recordUnscannedAs(path, language string) {
	c.UnscannedFiles++
	if language != "" {
		c.UnscannedLanguages = addLanguage(c.UnscannedLanguages, language)
	}
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

// recordRead counts one file a language scanner examined.
func (c *ScanCoverage) recordRead(language string) {
	c.FilesRead++
	c.LanguagesRead = addLanguage(c.LanguagesRead, language)
}

// addLanguage counts one file of language, allocating the map on first use and listing no
// language past maxCoverageLanguages.
func addLanguage(counts map[string]int, language string) map[string]int {
	if counts == nil {
		counts = make(map[string]int)
	}
	if _, known := counts[language]; known || len(counts) < maxCoverageLanguages {
		counts[language]++
	}
	return counts
}
