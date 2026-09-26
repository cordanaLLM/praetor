// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// distilledFiles returns the names of the regular files directly inside repo's distilled
// directory, failing on anything else found there.
func distilledFiles(t *testing.T, repo string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repo, DistilledDirRel))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			t.Fatalf("distilled directory holds a non-file entry %s", entry.Name())
		}
		names = append(names, entry.Name())
	}
	return names
}

// A SemVer version keeps its filename.
func TestSanitizeDocFilename_Positive_SemVerUnchanged(t *testing.T) {
	for want, parts := range map[string][2]string{
		"github.com_example_dep@v1.2.3.md": {"github.com/example/dep", "v1.2.3"},
		"@scope_pkg@1.2.3-rc.1+build.md":   {"@scope/pkg", "1.2.3-rc.1+build"},
	} {
		if got := sanitizeDocFilename(parts[0], parts[1]); got != want {
			t.Errorf("sanitizeDocFilename(%q, %q) = %q, want %q", parts[0], parts[1], got, want)
		}
	}
}

// A version carrying path syntax is written directly inside the distilled directory: it
// neither lands elsewhere in the repository nor aborts the write for escaping it.
func TestSaveCachedDoc_Negative_PathShapedVersionsStayInside(t *testing.T) {
	for _, version := range []string{"file:../../../../outside", "file:" + strings.Repeat("../", 12) + "x", "a/b:c", `..\..\win`} {
		t.Run(version, func(t *testing.T) {
			repo := t.TempDir()
			doc := sampleDoc("local-lib")
			doc.Version = version
			if err := SaveCachedDoc(repo, doc); err != nil {
				t.Fatalf("path-shaped version refused: %v", err)
			}
			if files := distilledFiles(t, repo); len(files) != 1 || strings.ContainsAny(files[0], `/\:`) {
				t.Fatalf("distilled files = %v, want one sanitized file", files)
			}
			if _, err := os.Stat(filepath.Join(repo, "outside.md")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("distilled file escaped the distilled directory: %v", err)
			}
		})
	}
}

// A package.json file: dependency does not abort the sync: it and the next dependency are
// both distilled inside the distilled directory.
func TestSyncRepositoryDocs_Negative_FileVersionDoesNotAbortSync(t *testing.T) {
	repo := t.TempDir()
	local := "file:" + strings.Repeat("../", 8) + "outside"
	writeNodeFile(t, repo, "package.json", `{"dependencies":{"local-lib":"`+local+`","left-pad":"^1.3.0"}}`)
	writeNodeFile(t, repo, "node_modules/local-lib/README.md", "# local-lib\nA local library fixture with documentation.\n")
	writeNodeFile(t, repo, "node_modules/left-pad/README.md", "# left-pad\nPads strings on the left, documented here.\n")
	ctx, cancel := context.WithTimeout(t.Context(), defaultTestTimeout)
	defer cancel()
	cat, err := SyncRepositoryDocs(ctx, repo, DistillOptions{OfflineOnly: true})
	if err != nil {
		t.Fatalf("sync aborted by a file: dependency: %v", err)
	}
	if _, ok := cat.Packages["local-lib@"+local]; !ok || len(cat.Packages) != 2 {
		t.Fatalf("catalog = %v, want both dependencies", cat.Packages)
	}
	if files := distilledFiles(t, repo); len(files) != 2 {
		t.Fatalf("distilled files = %v, want two", files)
	}
}

// An empty version, a leading '-', and control or Windows-reserved characters all yield a
// plain file name.
func TestSanitizeDocFilename_Boundary_EdgeComponents(t *testing.T) {
	for want, parts := range map[string][2]string{
		"pkg@.md":            {"pkg", ""},
		"pkg@_1.0.0.md":      {"pkg", "-1.0.0"},
		"_flag@1.0.0.md":     {"-flag", "1.0.0"},
		"pkg@_=1.0.0_a_b.md": {"pkg", ">=1.0.0|a\tb"},
		"pkg@_x__.md":        {"pkg", `"x*?`},
	} {
		if got := sanitizeDocFilename(parts[0], parts[1]); got != want {
			t.Errorf("sanitizeDocFilename(%q, %q) = %q, want %q", parts[0], parts[1], got, want)
		}
	}
}
