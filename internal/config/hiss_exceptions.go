// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import "fmt"

// HISSPolicy is the manifest's hiss section: what this repository declares about the HISS
// directives adoption writes into its AGENTS.md and Paperclip harnesses.
type HISSPolicy struct {
	Exceptions HISSExceptions `yaml:"exceptions,omitempty"`
}

// HISSExceptions names, per exception the harness knows, the repository document that records
// it. A declared exception replaces the directive clause it waives in the generated harnesses
// (internal/hisscatalog Exception); adoption honours it only while that document exists, so
// the harness never states an exception the repository does not document. An unknown key
// fails the strict manifest decode.
type HISSExceptions struct {
	// CGotoCleanup is the document allowing a C/C++ `goto` that jumps forward to the one
	// cleanup label of its function (single-level error unwinding), in place of HISS-01's
	// zero-`goto` clause. The audit's HISS-01 scan still reports every such `goto`.
	CGotoCleanup string `yaml:"c_goto_cleanup,omitempty"`
}

// validate holds every declared exception document to a clean repository-relative path, the
// shape register.sources inputs use (validRepositoryPath). A nil section declares nothing.
func (p *HISSPolicy) validate() error {
	if p == nil {
		return nil
	}
	if document := p.Exceptions.CGotoCleanup; document != "" && !validRepositoryPath(document) {
		return fmt.Errorf("hiss.exceptions.c_goto_cleanup %q must be a clean local forward-slash path of at most %d bytes",
			document, maxRepositoryPath)
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
