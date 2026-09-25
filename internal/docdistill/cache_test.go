// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// defaultTestTimeout bounds the offline documentation sync exercised below (HISS-02).
const defaultTestTimeout = 30 * time.Second

func sampleDoc(name string) *DistilledDoc {
	return &DistilledDoc{
		PackageName: name,
		Version:     "v1.0.0",
		Kind:        KindGoModule,
		Summary:     "sample",
		TokenCount:  3,
		RawMarkdown: "# sample\n",
	}
}

func TestSaveCachedDoc_Negative_NilInputs(t *testing.T) {
	if err := SaveCachedDoc(t.TempDir(), nil); err == nil {
		t.Fatal("nil doc must be rejected")
	}
	if err := SaveCatalog(t.TempDir(), nil); err == nil {
		t.Fatal("nil catalog must be rejected")
	}
}

func TestSaveCachedDoc_Negative_EscapingCacheDirIsRefused(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".workingdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, ".workingdir", "docs")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := SaveCachedDoc(repo, sampleDoc("github.com/example/escape"))
	if !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Fatalf("expected ErrPathEscapesRoot, got %v", err)
	}
	entries, readErr := os.ReadDir(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("nothing may be written through the symlink, found %d entries", len(entries))
	}
}

func TestSaveCachedDoc_Boundary_TraversalNameStaysInsideCache(t *testing.T) {
	repo := t.TempDir()
	doc := sampleDoc("../../escape")
	if err := SaveCachedDoc(repo, doc); err != nil {
		t.Fatalf("a traversal-shaped package name is sanitized, got %v", err)
	}
	distilled := filepath.Join(repo, DistilledDirRel)
	entries, err := os.ReadDir(distilled)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one distilled file under %s, err=%v", distilled, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "escape@v1.0.0.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("distilled file must not land outside the distilled dir")
	}
}

func TestSyncRepositoryDocs_UsesSharedWriter(t *testing.T) {
	repo := t.TempDir()
	goPath := t.TempDir()
	t.Setenv("GOPATH", goPath)
	t.Setenv("GOMODCACHE", "")
	readmeDir := filepath.Join(goPath, "pkg", "mod", "github.com", "example", "dep@v1.2.3")
	if err := os.MkdirAll(readmeDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(readmeDir, "README.md"), []byte("# Dependency\nLocal documentation fixture with a real source."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/x\n\nrequire github.com/example/dep v1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	cat, err := SyncRepositoryDocs(ctx, repo, DistillOptions{OfflineOnly: true})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(cat.Packages) == 0 {
		t.Fatal("expected at least one package in the catalog")
	}
	for _, doc := range cat.Packages {
		cached, found := GetCachedDoc(repo, doc.PackageName, doc.Version)
		if !found || cached == nil {
			t.Fatalf("doc %s written by sync must be readable through GetCachedDoc", doc.PackageName)
		}
		path := filepath.Join(repo, DistilledDirRel, sanitizeDocFilename(doc.PackageName, doc.Version))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("distilled markdown missing at %s: %v", path, err)
		}
	}
}
