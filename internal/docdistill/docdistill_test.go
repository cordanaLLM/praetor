// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocDistill_3D(t *testing.T) {
	// ==========================================
	// 1. Positive Tests
	// ==========================================
	t.Run("Positive: Compression & Token Bounding", func(t *testing.T) {
		ref := PackageRef{
			Name:    "github.com/example/pkg",
			Version: "v1.2.3",
			Kind:    KindGoModule,
			Direct:  true,
		}

		rawMD := `# Example Package
[![Build](https://img.shields.io/badge/build-pass-green)](https://example.com)

<div>Some html banner</div>

Example Package provides high-performance data serialization.

## Sponsors
Please donate on Patreon!

## Exported API
func Marshal(v any) ([]byte, error)
func Unmarshal(data []byte, v any) error
type Encoder struct
type Decoder interface

## Configuration
Use flag --fast for turbo mode.
Set env: TURBO=1

## Warnings
Note: Caller must close decoder after use.
`
		opts := DefaultDistillOptions()
		opts.MaxTokensPerPackage = 200

		distilled := CompressDocumentation(ref, rawMD, opts)
		if distilled == nil {
			t.Fatalf("expected non-nil distilled doc")
		}
		if distilled.PackageName != "github.com/example/pkg" {
			t.Errorf("unexpected package name: %s", distilled.PackageName)
		}
		if len(distilled.ContentHash) != 64 {
			t.Errorf("expected 64-char sha256 content hash, got: %s", distilled.ContentHash)
		}
		if distilled.TokenCount > opts.MaxTokensPerPackage {
			t.Errorf("token count %d exceeded max %d", distilled.TokenCount, opts.MaxTokensPerPackage)
		}
		if strings.Contains(distilled.RawMarkdown, "Sponsors") {
			t.Errorf("expected boilerplate 'Sponsors' to be stripped")
		}
		if strings.Contains(distilled.RawMarkdown, "<div>") {
			t.Errorf("expected HTML tags to be stripped")
		}
	})

	t.Run("Positive: Catalog & Cache Operations", func(t *testing.T) {
		tmpDir := t.TempDir()

		cat, err := LoadCatalog(tmpDir)
		if err != nil {
			t.Fatalf("failed loading new catalog: %v", err)
		}
		if cat == nil || len(cat.Packages) != 0 {
			t.Fatalf("expected empty catalog")
		}

		doc := &DistilledDoc{
			PackageName: "github.com/example/alpha",
			Version:     "v1.0.0",
			Kind:        KindGoModule,
			Summary:     "Alpha test library",
			TokenCount:  50,
			ContentHash: "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
			RawMarkdown: "# Alpha v1.0.0\nAlpha test library",
		}

		if err := SaveCachedDoc(tmpDir, doc); err != nil {
			t.Fatalf("failed saving cached doc: %v", err)
		}

		cached, found := GetCachedDoc(tmpDir, "github.com/example/alpha", "v1.0.0")
		if !found || cached == nil {
			t.Fatalf("expected cached doc to be found")
		}
		if cached.Summary != "Alpha test library" {
			t.Errorf("unexpected summary: %s", cached.Summary)
		}
	})

	// ==========================================
	// 2. Negative Tests
	// ==========================================
	t.Run("Negative: Nil Context", func(t *testing.T) {
		var absentContext context.Context
		_, err := ScanDeclaredDependencies(absentContext, "/tmp", false)
		if err == nil {
			t.Errorf("expected error with nil context")
		}

		_, err = HarvestDocumentation(absentContext, PackageRef{}, false)
		if err == nil {
			t.Errorf("expected error harvesting with nil context")
		}
	})

	t.Run("Negative: Cancelled Context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := ScanDeclaredDependencies(ctx, "/tmp", false)
		if err == nil {
			t.Errorf("expected error with cancelled context")
		}
	})

	t.Run("Negative: Corrupted Catalog File", func(t *testing.T) {
		tmpDir := t.TempDir()
		docsDir := filepath.Join(tmpDir, DocsDirRel)
		if err := os.MkdirAll(docsDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, CatalogFileRel), []byte("{corrupt-json"), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := LoadCatalog(tmpDir)
		if err == nil {
			t.Errorf("expected error on corrupt catalog JSON")
		}
	})

	// ==========================================
	// 3. Boundary Tests
	// ==========================================
	t.Run("Boundary: Empty Repo Manifests", func(t *testing.T) {
		tmpDir := t.TempDir()
		refs, err := ScanDeclaredDependencies(context.Background(), tmpDir, false)
		if err != nil {
			t.Fatalf("unexpected error on empty repo: %v", err)
		}
		if len(refs) != 0 {
			t.Errorf("expected 0 refs, got %d", len(refs))
		}

		audit, err := AuditDocumentationCoverage(context.Background(), tmpDir, DefaultDistillOptions())
		if err != nil {
			t.Fatalf("unexpected error auditing empty repo: %v", err)
		}
		if audit.CoverageScore != 0 || audit.Passed || audit.Status != "not_applicable" {
			t.Errorf("expected not-applicable empty repo, got: %+v", audit)
		}
	})

	t.Run("Boundary: Huge Markdown Compression Scaling", func(t *testing.T) {
		// Generate 10,000 repetitive words
		hugeWords := make([]string, 10000)
		for i := 0; i < 10000; i++ {
			hugeWords[i] = "parameter"
		}
		hugeMD := "# Huge Doc\n" + strings.Join(hugeWords, " ")

		ref := PackageRef{
			Name:    "github.com/example/huge",
			Version: "v5.0.0",
			Kind:    KindGoModule,
		}

		opts := DefaultDistillOptions()
		opts.MaxTokensPerPackage = 150

		distilled := CompressDocumentation(ref, hugeMD, opts)
		if distilled.TokenCount > opts.MaxTokensPerPackage {
			t.Errorf("token count %d exceeded boundary budget %d", distilled.TokenCount, opts.MaxTokensPerPackage)
		}
		if !strings.Contains(distilled.RawMarkdown, "Truncated") {
			t.Errorf("expected truncation notice in huge markdown")
		}
	})
}
