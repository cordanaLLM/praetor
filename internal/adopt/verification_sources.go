// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"path"
	"strings"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

// sourceLanguageC is the VerificationPlan.SourceLanguages entry for a repository carrying C
// sources, the language a build compiling them without meson or CMake (clang from a Makefile,
// eBPF objects from a script) declares through no marker (#549).
const sourceLanguageC = "c"

// cFamilyKind is what a file name says about C: a C translation unit, a header C shares with
// C++ and Objective-C, or a source of one of those other languages.
type cFamilyKind uint8

const (
	cFamilyNone cFamilyKind = iota
	cFamilyC
	cFamilyHeader
	cFamilyOther
)

// cFamilyExtensions classifies C-family file extensions: the suffixes gcc(1) assigns C, C++,
// Objective-C and Objective-C++ ("Options Controlling the Kind of Output"), plus CUDA and HIP.
// They are matched exactly first, since GCC reads `.C` and `.H` as C++ and `.M` as
// Objective-C++, and then folded to lower case. This table decides C for the HISS
// clauses only; the editor's extension table (internal/editor languageForFile) maps a file to an
// editor capability and reads every `.h` as C, the ambiguity this one resolves.
var cFamilyExtensions = map[string]cFamilyKind{
	".c": cFamilyC, ".h": cFamilyHeader,
	".C": cFamilyOther, ".H": cFamilyOther, ".M": cFamilyOther,
	".cc": cFamilyOther, ".cp": cFamilyOther, ".cpp": cFamilyOther, ".cxx": cFamilyOther, ".c++": cFamilyOther,
	".hh": cFamilyOther, ".hp": cFamilyOther, ".hpp": cFamilyOther, ".hxx": cFamilyOther, ".h++": cFamilyOther, ".tcc": cFamilyOther,
	".m": cFamilyOther, ".mm": cFamilyOther, ".cu": cFamilyOther, ".cuh": cFamilyOther, ".hip": cFamilyOther,
}

func cFamilyKindOf(ext string) cFamilyKind {
	if kind, ok := cFamilyExtensions[ext]; ok {
		return kind
	}
	return cFamilyExtensions[strings.ToLower(ext)]
}

// cSourceObservation records the C-family sources the verification walk visited. It adds no
// walk of its own: the walk's entry and depth bounds (VerificationLimits, the
// --verification-max-* flags) bound it, and it reads no file content, so it spends none of the
// file or byte budget.
type cSourceObservation struct {
	// paths holds the slash-separated C-family paths outside the audit's ignored directories,
	// at most one per walk entry.
	paths []string
}

// observe records the file at rel, a slash-separated path under the repository root. A file
// under a directory the HISS audit's scan ignores (hiss.ShouldIgnorePath: vendor, third_party,
// testdata, build output and the like) is not the repository's own source and says nothing about
// its language; the walk itself already skips vendor, build and scratch trees.
func (o *cSourceObservation) observe(rel string) {
	if cFamilyKindOf(path.Ext(rel)) == cFamilyNone || hiss.ShouldIgnorePath(rel) {
		return
	}
	o.paths = append(o.paths, rel)
}

// languages returns the source languages the observed files give the repository at root:
// sourceLanguageC or nothing. Only the files git reports as the repository's own count, the set
// the audit's HISS scan reads (hiss.GitVisiblePaths: tracked files plus untracked files no ignore
// rule matches), so an ignored in-place Cython `.c` or C under IDE build output selects no C
// clause the audit never enforces, and a fresh clone reads the same set as a built checkout.
// Outside a work tree git gives no answer and every observed file counts. Git is asked only when
// the walk saw a C-family file.
func (o cSourceObservation) languages(ctx context.Context, root string) []string {
	if len(o.paths) == 0 {
		return nil
	}
	if o.carriesC(hiss.GitVisiblePaths(ctx, root)) {
		return []string{sourceLanguageC}
	}
	return nil
}

// carriesC decides whether the observed sources visible reports make the repository a C
// repository: any `.c` source does, and a `.h` header does only when no C++, Objective-C, CUDA or
// HIP source claims it, since `.h` is the conventional header of all of them. A nil visible set
// (no git answer) keeps every observed file.
func (o cSourceObservation) carriesC(visible *hiss.GitVisibleTree) bool {
	var header, other bool
	for _, rel := range o.paths {
		if !visible.HasFile(rel) {
			continue
		}
		switch cFamilyKindOf(path.Ext(rel)) {
		case cFamilyC:
			return true
		case cFamilyHeader:
			header = true
		default:
			other = true
		}
	}
	return header && !other
}
