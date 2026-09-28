// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"strings"
	"testing"
)

// cleanupGotoManifest is a manifest declaring the C/C++ cleanup-goto exception at document.
func cleanupGotoManifest(document string) string {
	return "version: 1\nhiss:\n  exceptions:\n    c_goto_cleanup: \"" + document + "\"\n"
}

// TestHISSExceptions_Positive_DeclaredDocumentParses: a manifest naming the document that
// records its cleanup-goto exception loads, and CleanupGotoDocument returns that path.
func TestHISSExceptions_Positive_DeclaredDocumentParses(t *testing.T) {
	m, err := LoadManifest(writeManifest(t, cleanupGotoManifest("docs/adr/0003-cleanup-goto.md")))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if got := m.CleanupGotoDocument(); got != "docs/adr/0003-cleanup-goto.md" {
		t.Fatalf("CleanupGotoDocument = %q", got)
	}
}

// TestHISSExceptions_Negative_BadDeclarationsFail: a document path that is absolute, escapes
// the repository, uses a backslash or is not clean fails validation, and an exception the
// harness does not know fails the strict decode instead of being dropped as if declared.
func TestHISSExceptions_Negative_BadDeclarationsFail(t *testing.T) {
	// The manifest double-quotes the value, so `\\` decodes to the one backslash under test.
	for _, document := range []string{"/etc/cleanup.md", "../outside.md", `docs\\cleanup.md`, "docs/./cleanup.md", ".", ".."} {
		if _, err := LoadManifest(writeManifest(t, cleanupGotoManifest(document))); err == nil ||
			!strings.Contains(err.Error(), "hiss.exceptions.c_goto_cleanup") {
			t.Errorf("document %q: err = %v, want the c_goto_cleanup path refused", document, err)
		}
	}
	unknown := "version: 1\nhiss:\n  exceptions:\n    recursion_allowed: docs/recursion.md\n"
	if _, err := LoadManifest(writeManifest(t, unknown)); err == nil {
		t.Error("an unknown hiss exception loaded")
	}
}

// TestHISSExceptions_Boundary_EmptyNilAndLengthLimit: a manifest without the section, with an
// empty one, or a nil manifest declares nothing; a document path of exactly maxRepositoryPath
// bytes loads and one byte more is refused.
func TestHISSExceptions_Boundary_EmptyNilAndLengthLimit(t *testing.T) {
	var none *Manifest
	if none.CleanupGotoDocument() != "" {
		t.Error("nil manifest declares an exception")
	}
	for _, body := range []string{"version: 1\n", "version: 1\nhiss: {}\n", "version: 1\nhiss:\n  exceptions: {}\n"} {
		m, err := LoadManifest(writeManifest(t, body))
		if err != nil || m.CleanupGotoDocument() != "" {
			t.Errorf("%q: document %q, err %v; want none", body, m.CleanupGotoDocument(), err)
		}
	}
	longest := "docs/" + strings.Repeat("a", maxRepositoryPath-len("docs/.md")) + ".md"
	if _, err := LoadManifest(writeManifest(t, cleanupGotoManifest(longest))); err != nil {
		t.Errorf("%d-byte document refused: %v", len(longest), err)
	}
	if _, err := LoadManifest(writeManifest(t, cleanupGotoManifest("a"+longest))); err == nil {
		t.Errorf("%d-byte document loaded", len(longest)+1)
	}
}
