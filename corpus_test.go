// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package praetor

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

// maxFilesWalked bounds the tree walk for HISS-02.
const maxFilesWalked = 4096

// collectTreeFiles lists all regular files below root relative to root.
func collectTreeFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	count := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if count >= maxFilesWalked {
			return fs.ErrInvalid
		}
		count++
		if !d.IsDir() {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk tree %s: %v", root, err)
	}
	sort.Strings(files)
	return files
}

// collectFSFiles lists all regular files below root in fsys relative to root.
func collectFSFiles(t *testing.T, fsys fs.FS, root string) []string {
	t.Helper()
	var files []string
	count := 0
	err := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if count >= maxFilesWalked {
			return fs.ErrInvalid
		}
		count++
		if !d.IsDir() {
			rel := strings.TrimPrefix(path, root+"/")
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk fs %s: %v", root, err)
	}
	sort.Strings(files)
	return files
}

// TestEmbeddedCorpus_Positive_TreeEqualsEmbedded compares the disk testdata tree with the embedded FS.
func TestEmbeddedCorpus_Positive_TreeEqualsEmbedded(t *testing.T) {
	diskFiles := collectTreeFiles(t, filepath.Join(".config", "hiss", "testdata"))
	embedFiles := collectFSFiles(t, EmbeddedCorpus, ".config/hiss/testdata")

	if len(diskFiles) == 0 {
		t.Fatal("disk corpus has zero files")
	}
	if err := checkCorpusInventory(diskFiles, embedFiles); err != nil {
		t.Fatal(err)
	}
	// Also check coverage.yaml is embedded.
	if _, err := fs.Stat(EmbeddedCorpus, ".config/hiss/coverage.yaml"); err != nil {
		t.Fatalf("embedded coverage.yaml missing: %v", err)
	}
}

// checkCorpusInventory compares the embedded inventory to its disk source.
func checkCorpusInventory(disk, embedded []string) error {
	if !slices.Equal(disk, embedded) {
		return fmt.Errorf("embedded fixture list differs from disk tree: got %d files, disk has %d files", len(embedded), len(disk))
	}
	return nil
}

// TestEmbeddedCorpus_Negative_PlantedMissingFixtureFails exercises the same comparison as the positive test.
func TestEmbeddedCorpus_Negative_PlantedMissingFixtureFails(t *testing.T) {
	root := ".config/hiss/testdata"
	planted := fstest.MapFS{
		root + "/HISS-01/go/positive/case.go": &fstest.MapFile{Data: []byte("package p")},
	}
	disk := []string{"HISS-01/go/positive/case.go", "HISS-20/go/gap/.hidden_fixture"}
	if err := checkCorpusInventory(disk, collectFSFiles(t, planted, root)); err == nil {
		t.Fatal("inventory accepted an embedded filesystem missing the planted fixture")
	}
}
