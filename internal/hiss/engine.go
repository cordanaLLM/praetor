// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The scan engine and its language scanners.
//
// Scan owns everything that does not depend on a language: the walk and its bounds, the git
// scope, the ignore policy, the byte-bounded read and the coverage accounting. A language
// scanner owns only what a file's bytes mean for the invariants. Adding a language is one entry
// in languageScanners: SupportsExtension, the walk's scope test and the coverage report all
// read that table, so a language is never reported as supported where it is not scanned, nor
// scanned where the report calls it unscanned.

// languageScanner is one language's HISS rule set.
type languageScanner interface {
	// handles reports whether the scanner reads files with ext, a lower-cased extension.
	handles(ext string) bool
	// name is the language recorded for a file whose extension util.SourceLanguage does not
	// name (a .hip HIP source is C-family code to the native scanner).
	name() string
	// scan audits one file and reports whether it examined the file. A file it declines, such
	// as minified output, is recorded as unscanned rather than read, so the report never
	// counts it as clean.
	scan(src sourceFile, rep *ScanReport, opts ScanOptions) bool
}

// contentScanner is a language scanner that also reads files whose extension alone does not
// assign them to it: a script without an extension whose interpreter line names a shell, a YAML
// file that is an Ansible playbook, a unit file systemd reads. candidate is a path test made
// before any read; claims decides from the bytes. A candidate no scanner claims is recorded
// exactly like a file of an extension no scanner reads, so a YAML or extensionless file that is
// none of these is reported as it always was, and a claim never widens SupportsExtension.
type contentScanner interface {
	languageScanner
	// candidate reports whether the scanner may claim rel, a slash-separated path whose
	// lower-cased extension is ext.
	candidate(rel, ext string) bool
	// claims reports whether the file's bytes are this scanner's language.
	claims(src sourceFile) bool
}

// boundedScanner is a language scanner that reads at most fileBound files in one scan
// (HISS-02). A file past the bound is recorded as unscanned source of its language, never as
// clean: GitHub reads its workflows out of one directory, and the workflow scanner reads no more
// of them than the forge audits do (ghworkflow.MaxFiles).
type boundedScanner interface {
	languageScanner
	fileBound() int
}

// packageScanner is a language scanner whose rules also need every file it examined once the
// walk is over: a call cycle through two functions is only visible when every file of their
// package has been read.
type packageScanner interface {
	languageScanner
	finish(ctx context.Context, rep *ScanReport, root string, paths []string)
}

// sourceFile is one file handed to a language scanner.
type sourceFile struct {
	// rel is the path relative to the scan root, as violations report it.
	rel  string
	data []byte
}

// lines splits the file into its physical lines.
func (s sourceFile) lines() []string {
	return strings.Split(string(s.data), "\n")
}

// lfLines splits the file into its physical lines with a CRLF checkout, which `* text=auto`
// gives every text file on Windows, read as LF. A scanner whose rules look at the end of a line
// then reads a file identically on every platform (HISS-21).
func (s sourceFile) lfLines() []string {
	text, _ := util.NormalizeLineEndings(string(s.data))
	return strings.Split(text, "\n")
}

// languageScanners is the one dispatch table. No two scanners claim one extension.
var languageScanners = [...]languageScanner{
	goLanguage{}, nativeLanguage{}, pythonLanguage{}, rustLanguage{}, scriptLanguage{},
	shellLanguage{}, workflowLanguage{}, systemdLanguage{}, ansibleLanguage{},
}

// scannerIndex returns the index of the scanner that reads ext, a lower-cased extension, or -1
// when no scanner does.
func scannerIndex(ext string) int {
	for i := 0; i < len(languageScanners); i++ {
		if languageScanners[i].handles(ext) {
			return i
		}
	}
	return -1
}

func isScannableExt(ext string) bool {
	return scannerIndex(ext) >= 0
}

// isContentCandidate reports whether a content scanner may claim rel, a path whose lower-cased
// extension is ext, once its bytes are read.
func isContentCandidate(rel, ext string) bool {
	slashRel := filepath.ToSlash(rel)
	for i := 0; i < len(languageScanners); i++ {
		if c, ok := languageScanners[i].(contentScanner); ok && c.candidate(slashRel, ext) {
			return true
		}
	}
	return false
}

// mayScan reports whether rel, by its path alone, is a file some language scanner may read: an
// extension a scanner owns, or a path a content scanner may claim from its bytes.
func mayScan(rel string) bool {
	ext := strings.ToLower(filepath.Ext(rel))
	return isScannableExt(ext) || isContentCandidate(rel, ext)
}

// claimingScanner returns the index of the content scanner that claims src, a file whose
// lower-cased extension is ext, or -1 when none does.
func claimingScanner(src sourceFile, ext string) int {
	slashRel := filepath.ToSlash(src.rel)
	for i := 0; i < len(languageScanners); i++ {
		c, ok := languageScanners[i].(contentScanner)
		if ok && c.candidate(slashRel, ext) && c.claims(src) {
			return i
		}
	}
	return -1
}

// fileLanguage names the language of rel for the coverage report: the repository-wide name
// util.SourceLanguage gives, or the scanner's own for an extension that table leaves out.
func fileLanguage(s languageScanner, rel string) string {
	if language := util.SourceLanguage(rel); language != "" {
		return language
	}
	return s.name()
}

// scanFile reads one source file under a byte bound and hands it to its language scanner: the
// one its extension names, or else the content scanner that claims its bytes. The path is
// re-confined to the root before it is opened.
func (w *scanWalker) scanFile(path, rel string) error {
	ext := strings.ToLower(filepath.Ext(rel))
	idx := scannerIndex(ext)
	if idx < 0 && !isContentCandidate(rel, ext) {
		w.rep.Coverage.recordUnscanned(rel)
		return nil
	}
	data, err := readBounded(w.root, rel)
	if err != nil {
		return w.readFailed(rel, idx, err)
	}
	src := sourceFile{rel: rel, data: data}
	if idx < 0 {
		if idx = claimingScanner(src, ext); idx < 0 {
			w.rep.Coverage.recordUnscanned(rel)
			return nil
		}
	}
	scanner := languageScanners[idx]
	if !w.admit(idx) || !w.scanAnchored(scanner, src) {
		// A declined file, or one past its scanner's file bound, is source of the scanner's
		// language that no rule examined.
		w.rep.Coverage.recordUnscannedAs(rel, fileLanguage(scanner, rel))
		return nil
	}
	w.rep.Coverage.recordRead(fileLanguage(scanner, rel))
	// Remember the file for its package pass, which runs once every file has been read.
	if _, ok := scanner.(packageScanner); ok && len(w.packages[idx]) < maxCallGraphFiles {
		w.packages[idx] = append(w.packages[idx], path)
	}
	return nil
}

// admit counts one more file for the scanner at idx and reports whether it is within that
// scanner's file bound. A scanner without a bound admits every file.
func (w *scanWalker) admit(idx int) bool {
	bounded, ok := languageScanners[idx].(boundedScanner)
	if !ok {
		return true
	}
	if w.admitted[idx] >= bounded.fileBound() {
		return false
	}
	w.admitted[idx]++
	return true
}

// scanAnchored hands src to scanner and anchors the findings that scan recorded to the
// functions it noted (anchor.go), so each finding leaves the walk with an identity a line shift
// does not change. It reports what the scanner reports: whether it examined the file.
func (w *scanWalker) scanAnchored(scanner languageScanner, src sourceFile) bool {
	first := len(w.rep.Violations)
	w.rep.functions = w.rep.functions[:0]
	examined := scanner.scan(src, w.rep, w.opts)
	anchorViolations(w.rep.Violations[first:], w.rep.functions, src)
	return examined
}

// readFailed accounts for a file the bounded read refused. A content candidate was never known
// to be source, so any failed read (oversize, permission denied, a confinement refusal) leaves
// it an unscanned file, as it was before any scanner could claim one. An oversize file of a
// scanned extension is a skipped input; any other failure on one fails the scan.
func (w *scanWalker) readFailed(rel string, idx int, err error) error {
	if idx < 0 {
		w.rep.Coverage.recordUnscanned(rel)
		return nil
	}
	if !errors.Is(err, errOversize) {
		return err
	}
	w.rep.Skips.Oversize++
	return nil
}

// finish runs every package pass over the files its scanner examined.
func (w *scanWalker) finish() {
	for i := 0; i < len(languageScanners); i++ {
		if p, ok := languageScanners[i].(packageScanner); ok && len(w.packages[i]) > 0 {
			p.finish(w.ctx, w.rep, w.root, w.packages[i])
		}
	}
}

// goLanguage reads Go through go/parser (go_ast.go) and closes call cycles across a package
// (go_callgraph.go).
type goLanguage struct{}

func (goLanguage) handles(ext string) bool { return ext == ".go" }

func (goLanguage) name() string { return "go" }

func (goLanguage) scan(src sourceFile, rep *ScanReport, opts ScanOptions) bool {
	scanGoSource(src.data, src.rel, rep, opts)
	return true
}

func (goLanguage) finish(ctx context.Context, rep *ScanReport, root string, paths []string) {
	reportCallCycles(ctx, rep, paths, root)
}

// nativeLanguage reads C, C++, CUDA and HIP line by line (rules.go scanNativeLines).
type nativeLanguage struct{}

func (nativeLanguage) handles(ext string) bool { return isNativeExt(ext) }

func (nativeLanguage) name() string { return "c" }

func (nativeLanguage) scan(src sourceFile, rep *ScanReport, opts ScanOptions) bool {
	scanNativeLines(src.lines(), src.rel, rep, opts)
	return true
}

// pythonLanguage reads Python by indentation (rules.go scanPythonLines).
type pythonLanguage struct{}

func (pythonLanguage) handles(ext string) bool { return ext == ".py" }

func (pythonLanguage) name() string { return "python" }

func (pythonLanguage) scan(src sourceFile, rep *ScanReport, opts ScanOptions) bool {
	scanPythonLines(src.lines(), src.rel, rep, opts)
	return true
}

// rustLanguage reads Rust line by line (rules.go scanRustLines).
type rustLanguage struct{}

func (rustLanguage) handles(ext string) bool { return ext == ".rs" }

func (rustLanguage) name() string { return "rust" }

func (rustLanguage) scan(src sourceFile, rep *ScanReport, opts ScanOptions) bool {
	scanRustLines(src.lines(), src.rel, rep, opts)
	return true
}
