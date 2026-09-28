// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hiss"
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

// labelsManifest declares the cleanup-goto exception at docs/cleanup-goto.md with labels.
func labelsManifest(labels ...string) string {
	body := cleanupGotoManifest("docs/cleanup-goto.md") + "    c_goto_cleanup_labels:\n"
	for _, label := range labels {
		body += "      - \"" + label + "\"\n"
	}
	return body
}

// TestHISSExceptions_Labels: Positive: up to hiss.MaxCleanupGotoLabels C identifiers load.
// Negative: labels without the exception, one label too many and a name that is no C identifier
// are refused. Boundary: exactly the bound loads.
func TestHISSExceptions_Labels(t *testing.T) {
	bound := make([]string, hiss.MaxCleanupGotoLabels)
	for i := range bound {
		bound[i] = "unwind" + strings.Repeat("x", i)
	}
	if _, err := LoadManifest(writeManifest(t, labelsManifest(bound...))); err != nil {
		t.Fatalf("%d labels refused: %v", len(bound), err)
	}
	for name, body := range map[string]string{
		"without exception": "version: 1\nhiss:\n  exceptions:\n    c_goto_cleanup_labels: [unwind]\n",
		"over the bound":    labelsManifest(append(bound, "extra")...),
		"not an identifier": labelsManifest("1st"),
		"empty name":        labelsManifest(""),
	} {
		if _, err := LoadManifest(writeManifest(t, body)); err == nil || !strings.Contains(err.Error(), "c_goto_cleanup_labels") {
			t.Errorf("%s: err = %v, want the labels refused", name, err)
		}
	}
}

// cleanupGotoRoot is a repository root whose manifest declares the exception with label unwind,
// carrying the document when documented, and no lock.
func cleanupGotoRoot(t *testing.T, documented bool) string {
	t.Helper()
	root := filepath.Dir(writeManifest(t, labelsManifest("unwind")))
	if documented {
		if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "docs", "cleanup-goto.md"), []byte("# Cleanup goto\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestCleanupGotoException_ScanOptions: the gate and baseline reading
// (ResolveRepositoryScanOptions) and the audit reading (EffectivePolicy.HISSScanOptions) enable
// the exception, with its declared labels, only while the document exists. Positive: documented.
// Negative: undocumented enables nothing and warns naming the document; an unadopted root, a nil
// manifest and a nil policy enable nothing silently. Boundary: the document path must be a file,
// a directory of that name does not count.
func TestCleanupGotoException_ScanOptions(t *testing.T) {
	root := cleanupGotoRoot(t, true)
	opts, warning, err := ResolveRepositoryScanOptions(t.Context(), root, hiss.ScanOptions{})
	if err != nil || warning != "" || !opts.CleanupGoto.Enabled || !slices.Equal(opts.CleanupGoto.Labels, []string{"unwind"}) {
		t.Fatalf("documented: %+v, %q, %v; want the exception with label unwind", opts.CleanupGoto, warning, err)
	}
	if opts.MaxFuncLOC != AuditMaxFuncLOC {
		t.Errorf("documented: function length %d, want the ceiling %d", opts.MaxFuncLOC, AuditMaxFuncLOC)
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	audited, warning := (&EffectivePolicy{Manifest: manifest, Policy: *DefaultPolicy()}).HISSScanOptions(root, hiss.ScanOptions{})
	if !audited.CleanupGoto.Enabled || warning != "" {
		t.Errorf("audit reading: %+v, %q; want the exception", audited.CleanupGoto, warning)
	}

	undocumented := cleanupGotoRoot(t, false)
	opts, warning, err = ResolveRepositoryScanOptions(t.Context(), undocumented, hiss.ScanOptions{})
	if err != nil || opts.CleanupGoto.Enabled || !strings.Contains(warning, "docs/cleanup-goto.md") {
		t.Fatalf("undocumented: %+v, %q, %v; want no exception and a warning naming the document", opts.CleanupGoto, warning, err)
	}
	if opts, warning, err = ResolveRepositoryScanOptions(t.Context(), t.TempDir(), hiss.ScanOptions{}); err != nil ||
		opts.CleanupGoto.Enabled || warning != "" {
		t.Errorf("unadopted root: %+v, %q, %v", opts.CleanupGoto, warning, err)
	}
	var none *Manifest
	if exception, warning := none.CleanupGotoException(root); exception.Enabled || warning != "" {
		t.Errorf("nil manifest: %+v, %q", exception, warning)
	}
	var unresolved *EffectivePolicy
	if ceiling, warning := unresolved.HISSScanOptions(root, hiss.ScanOptions{}); ceiling.CleanupGoto.Enabled ||
		warning != "" || ceiling.MaxFuncLOC != AuditMaxFuncLOC {
		t.Errorf("nil policy: %+v, %q", ceiling, warning)
	}

	directory := cleanupGotoRoot(t, false)
	if err := os.MkdirAll(filepath.Join(directory, "docs", "cleanup-goto.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if exception, warning := manifest.CleanupGotoException(directory); exception.Enabled || warning == "" {
		t.Errorf("directory as document: %+v, %q; want no exception and a warning", exception, warning)
	}
}
