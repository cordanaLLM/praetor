// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocsAuditRejectsUnknownFlagsAndExtraPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--all"}, {root, filepath.Join(root, "other")}} {
		if err := runDocsAudit(context.Background(), args); err == nil {
			t.Fatalf("docs audit accepted invalid arguments %v", args)
		}
	}
}

func TestDocsAuditEmptyRepositoryIsNotApplicable(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return runDocsAudit(context.Background(), []string{t.TempDir()})
	})
	if err == nil || !strings.Contains(err.Error(), "not_applicable") {
		t.Fatalf("expected not-applicable failure, output=%q err=%v", out, err)
	}
	if !strings.Contains(out, "Status:     not_applicable") || !strings.Contains(out, "Passed:     false") {
		t.Fatalf("missing explicit empty-scope status: %q", out)
	}
	if strings.Contains(out, "100.0%") {
		t.Fatalf("empty scope claims full coverage: %q", out)
	}
}

func TestDocsAuditRejectsMissingAndFileRoots(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a repository"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{filepath.Join(t.TempDir(), "missing"), file} {
		if err := runDocsAudit(context.Background(), []string{root}); err == nil {
			t.Fatalf("docs audit accepted invalid root %q", root)
		}
	}
}

// auditNodeRepo declares left-pad 1.3.0 with a README that documents it.
func auditNodeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{"dependencies":{"left-pad":"1.3.0"}}`)
	writeFixtureFile(t, root, "node_modules/left-pad/README.md", "# left-pad\nPads strings on the left, documented here.\n")
	return root
}

// A sheet from an older catalog schema is listed as stale and fails the audit; after a sync
// the same repository passes.
func TestDocsAuditListsStaleSheetsUntilSynced(t *testing.T) {
	root := auditNodeRepo(t)
	writeFixtureFile(t, root, ".workingdir/docs/catalog.json",
		`{"version":"v1","packages":{"left-pad@1.3.0":{"package_name":"left-pad","version":"1.3.0","token_count":12}}}`)
	out, err := captureStdout(t, func() error { return runDocsAudit(t.Context(), []string{root}) })
	if err == nil || !strings.Contains(err.Error(), "0 missing, 1 stale") {
		t.Fatalf("stale sheet passed the audit: output=%q err=%v", out, err)
	}
	if !strings.Contains(out, "Stale Distilled Documentation:\n  - left-pad@1.3.0") || strings.Contains(out, "Missing Distilled") {
		t.Fatalf("stale sheet not listed as stale: %q", out)
	}
	if _, err := captureStdout(t, func() error { return runDocsSync(t.Context(), []string{"--offline", root}) }); err != nil {
		t.Fatalf("docs sync: %v", err)
	}
	out, err = captureStdout(t, func() error { return runDocsAudit(t.Context(), []string{root}) })
	if err != nil || !strings.Contains(out, "Documented: 1 / 1") {
		t.Fatalf("synced repository failed the audit: output=%q err=%v", out, err)
	}
}

// A README that yields no documentation content is cached, but the audit lists it as missing.
func TestDocsAuditCountsHeaderOnlySheetAsMissing(t *testing.T) {
	root := auditNodeRepo(t)
	writeFixtureFile(t, root, "node_modules/left-pad/README.md", "# left-pad\n")
	if _, err := captureStdout(t, func() error { return runDocsSync(t.Context(), []string{"--offline", root}) }); err != nil {
		t.Fatalf("docs sync: %v", err)
	}
	out, err := captureStdout(t, func() error { return runDocsAudit(t.Context(), []string{root}) })
	if err == nil || !strings.Contains(err.Error(), "1 missing, 0 stale") || !strings.Contains(out, "Documented: 0 / 1") {
		t.Fatalf("header-only sheet counted as documented: output=%q err=%v", out, err)
	}
}
