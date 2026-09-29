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

// languageScanners is the one dispatch table. No two scanners claim one extension.
var languageScanners = [...]languageScanner{
	goLanguage{}, nativeLanguage{}, pythonLanguage{}, rustLanguage{}, scriptLanguage{},
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

// fileLanguage names the language of rel for the coverage report: the repository-wide name
// util.SourceLanguage gives, or the scanner's own for an extension that table leaves out.
func fileLanguage(s languageScanner, rel string) string {
	if language := util.SourceLanguage(rel); language != "" {
		return language
	}
	return s.name()
}

// scanFile reads one source file under a byte bound and hands it to its language scanner. The
// path is re-confined to the root before it is opened.
func (w *scanWalker) scanFile(path, rel string) error {
	idx := scannerIndex(strings.ToLower(filepath.Ext(rel)))
	if idx < 0 {
		w.rep.Coverage.recordUnscanned(rel)
		return nil
	}
	data, err := readBounded(w.root, rel)
	if err != nil {
		if errors.Is(err, errOversize) {
			w.rep.Skips.Oversize++
			return nil
		}
		return err
	}
	scanner := languageScanners[idx]
	if !scanner.scan(sourceFile{rel: rel, data: data}, w.rep, w.opts) {
		w.rep.Coverage.recordUnscanned(rel)
		return nil
	}
	w.rep.Coverage.recordRead(fileLanguage(scanner, rel))
	// Remember the file for its package pass, which runs once every file has been read.
	if _, ok := scanner.(packageScanner); ok && len(w.packages[idx]) < maxCallGraphFiles {
		w.packages[idx] = append(w.packages[idx], path)
	}
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
