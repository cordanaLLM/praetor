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

// sourceLanguageC is the VerificationPlan.SourceLanguages entry for a repository carrying C or
// C++ sources (hisscatalog.LanguageC, labelled C/C++), the language a build compiling them without
// meson or CMake (clang from a Makefile, eBPF objects from a script, Bazel) declares through no
// marker (#549).
const sourceLanguageC = "c"

// cSourceKind is what a file name says about the HISS C/C++ clauses: a source the audit's native
// scan reads, the `.h` header C and C++ share with Objective-C, or an Objective-C source.
type cSourceKind uint8

const (
	cSourceNone cSourceKind = iota
	cSourceNative
	cSourceHeader
	cSourceObjC
)

// cSourceKindOf classifies ext in any case, as the audit's scan folds it. Every extension the
// native scanner reads (hiss.IsNativeExtension, the files HISS-01's `goto` and HISS-08's
// banned-libc checks cover) is C/C++, except `.h`, the header Objective-C shares. Objective-C
// (`.m`) and Objective-C++ (`.mm`) sources, which the audit does not scan, are recorded only so
// they can claim a `.h`.
func cSourceKindOf(ext string) cSourceKind {
	switch lower := strings.ToLower(ext); {
	case lower == ".h":
		return cSourceHeader
	case hiss.IsNativeExtension(lower):
		return cSourceNative
	case lower == ".m" || lower == ".mm":
		return cSourceObjC
	default:
		return cSourceNone
	}
}

// cSourceObservation records the C/C++ and Objective-C sources the verification walk visited. It
// adds no walk of its own: the walk's entry and depth bounds (VerificationLimits, the
// --verification-max-* flags) bound it, and it reads no file content, so it spends none of the
// file or byte budget.
type cSourceObservation struct {
	// paths holds the slash-separated C/C++ and Objective-C paths outside the audit's ignored
	// directories, at most one per walk entry.
	paths []string
}

// observe records the file at rel, a slash-separated path under the repository root. A file
// under a directory the HISS audit's scan ignores (hiss.ShouldIgnorePath: vendor, third_party,
// testdata, build output and the like) is not the repository's own source and says nothing about
// its language. The walk visits a subset of the files the scan reads: it also skips bin, obj,
// dist and __pycache__ (skipVerificationDirectory), which the scan enters, so C/C++ sources found
// only there select no clause.
func (o *cSourceObservation) observe(rel string) {
	if cSourceKindOf(path.Ext(rel)) == cSourceNone || hiss.ShouldIgnorePath(rel) {
		return
	}
	o.paths = append(o.paths, rel)
}

// languages returns the source languages the observed files give the repository at root:
// sourceLanguageC or nothing. Only the files git reports as the repository's own count, the set
// the audit's HISS scan reads (hiss.GitVisiblePaths: tracked files plus untracked files no ignore
// rule matches), so an ignored in-place Cython `.c` or C under IDE build output selects no C/C++
// clause the audit never enforces, and a fresh clone reads the same set as a built checkout.
// Outside a work tree git gives no answer and every observed file counts. Git is asked only when
// the walk saw a C/C++ or Objective-C file.
func (o cSourceObservation) languages(ctx context.Context, root string) []string {
	if len(o.paths) == 0 {
		return nil
	}
	if o.carriesC(hiss.GitVisiblePaths(ctx, root)) {
		return []string{sourceLanguageC}
	}
	return nil
}

// carriesC decides whether the observed sources visible reports make the repository a C/C++
// repository: any source the audit's native scan reads does (C, C++, CUDA, HIP), and a `.h`
// header does unless an Objective-C source claims it, since `.h` is Objective-C's header too and
// the audit does not scan Objective-C. A nil visible set (no git answer) keeps every observed file.
func (o cSourceObservation) carriesC(visible *hiss.GitVisibleTree) bool {
	var header, objC bool
	for _, rel := range o.paths {
		if !visible.HasFile(rel) {
			continue
		}
		switch cSourceKindOf(path.Ext(rel)) {
		case cSourceNative:
			return true
		case cSourceHeader:
			header = true
		default:
			objC = true
		}
	}
	return header && !objC
}
