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
