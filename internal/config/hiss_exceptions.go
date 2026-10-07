// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

// HISSPolicy is the manifest's hiss section: what this repository declares about the HISS
// directives adoption writes into its AGENTS.md and Paperclip harnesses and the audit enforces.
type HISSPolicy struct {
	Exceptions HISSExceptions `yaml:"exceptions,omitempty"`
}

// HISSExceptions names, per exception praetor knows, the repository document that records it.
// A declared exception replaces the directive clause it waives in the generated harnesses
// (internal/hisscatalog Exception) and narrows the audit's check to the exception's rule;
// both honour it only while that document exists (Manifest.CleanupGotoException), so neither
// grants an exception the repository does not document. An unknown key fails the strict
// manifest decode.
type HISSExceptions struct {
	// CGotoCleanup is the document allowing a C/C++ `goto` that jumps forward to the one
	// cleanup label of its function (single-level error unwinding), in place of HISS-01's
	// zero-`goto` clause. The audit's native scan then accepts exactly the gotos
	// hiss.CleanupGoto describes and reports every other one.
	CGotoCleanup string `yaml:"c_goto_cleanup,omitempty"`
	// CGotoCleanupLabels are label names the exception accepts beyond cleanup, out, err and
	// fail: C identifiers, at most hiss.MaxCleanupGotoLabels, and only beside CGotoCleanup.
	CGotoCleanupLabels []string `yaml:"c_goto_cleanup_labels,omitempty"`
}

// validate holds every declared exception document to a clean repository-relative path, the
// shape register.sources inputs use (ValidRepositoryPath), and every declared label to a C
// identifier. A nil section declares nothing.
func (p *HISSPolicy) validate() error {
	if p == nil {
		return nil
	}
	exceptions := p.Exceptions
	if document := exceptions.CGotoCleanup; document != "" && !ValidRepositoryPath(document) {
		return fmt.Errorf("hiss.exceptions.c_goto_cleanup %q must be a clean local forward-slash path of at most %d bytes",
			document, maxRepositoryPath)
	}
	return validateCleanupGotoLabels(exceptions)
}

// validateCleanupGotoLabels refuses declared labels without the exception they extend, more
// than hiss.MaxCleanupGotoLabels of them, and any name that is no C identifier.
func validateCleanupGotoLabels(exceptions HISSExceptions) error {
	labels := exceptions.CGotoCleanupLabels
	switch {
	case len(labels) == 0:
		return nil
	case exceptions.CGotoCleanup == "":
		return errors.New("hiss.exceptions.c_goto_cleanup_labels requires hiss.exceptions.c_goto_cleanup")
	case len(labels) > hiss.MaxCleanupGotoLabels:
		return fmt.Errorf("hiss.exceptions.c_goto_cleanup_labels declares %d labels, at most %d allowed",
			len(labels), hiss.MaxCleanupGotoLabels)
	}
	for _, label := range labels {
		if !hiss.ValidCleanupGotoLabel(label) {
			return fmt.Errorf("hiss.exceptions.c_goto_cleanup_labels entry %q must be a C identifier of at most 64 bytes", label)
		}
	}
	return nil
}

// CleanupGotoDocument returns the document the manifest names for the C/C++ cleanup-`goto`
// exception, or "" when it declares none. It is safe on a nil manifest.
func (m *Manifest) CleanupGotoDocument() string {
	if m == nil || m.HISS == nil {
		return ""
	}
	return m.HISS.Exceptions.CGotoCleanup
}

// CleanupGotoException returns the cleanup-goto exception the manifest declares for the
// repository at root, enabled only while the document it names is a regular file inside root.
// A declaration whose document is missing enables nothing, and the returned warning says so:
// the audit keeps reporting every `goto` and adoption keeps the zero-goto clause. It is the one
// reading of the declaration the audit, the gate, the baseline and adoption share, and is safe on
// a nil manifest.
func (m *Manifest) CleanupGotoException(root string) (hiss.CleanupGoto, string) {
	document := m.CleanupGotoDocument()
	if document == "" {
		return hiss.CleanupGoto{}, ""
	}
	if path, err := util.ConfinePath(root, filepath.FromSlash(document)); err == nil && util.FileExists(path) {
		return hiss.CleanupGoto{Enabled: true, Labels: m.HISS.Exceptions.CGotoCleanupLabels}, ""
	}
	return hiss.CleanupGoto{}, fmt.Sprintf("%s hiss.exceptions.c_goto_cleanup names %s, which is no regular file in the repository; "+
		"HISS-01 reports every goto and keeps its zero-goto clause until that document exists", ManifestFileName, document)
}

// HISSScanOptions returns opts with every scan input the effective policy carries for the
// repository at root: its complexity limits (ComplexityPolicy.ScanOptions) and the exceptions its
// manifest declares and documents (Manifest.CleanupGotoException). The warning names a declared
// exception left unhonoured. A nil policy scans under HISSComplexityCeiling and no exception.
func (e *EffectivePolicy) HISSScanOptions(root string, opts hiss.ScanOptions) (hiss.ScanOptions, string) {
	if e == nil {
		return HISSComplexityCeiling().ScanOptions(opts), ""
	}
	opts = e.Policy.Complexity.ScanOptions(opts)
	var warning string
	opts.CleanupGoto, warning = e.Manifest.CleanupGotoException(root)
	return opts, warning
}
