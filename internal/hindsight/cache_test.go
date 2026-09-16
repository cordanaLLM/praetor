// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package hindsight

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestRecallLocalFactsWithError_Positive(t *testing.T) {
	root := t.TempDir()
	facts := []MemoryFact{
		{ID: "subject", Category: CategoryDependencyDoc, Subject: "YAML codec"},
		{ID: "statement", Category: CategoryDependencyDoc, Statement: "Parse yaml safely"},
		{ID: "tag", Category: CategoryDependencyDoc, Tags: []string{"yaml"}},
		{ID: "other", Category: CategoryGovernance, Subject: "yaml governance"},
	}
	if err := SaveLocalCache(root, facts); err != nil {
		t.Fatal(err)
	}
	matches, err := RecallLocalFactsWithError(root, "YaMl", CategoryDependencyDoc)
	if err != nil || len(matches) != 3 {
		t.Fatalf("query returned %d facts, %v", len(matches), err)
	}
	for i, id := range []string{"subject", "statement", "tag"} {
		if matches[i].ID != id {
			t.Fatalf("match %d: got %q, want %q", i, matches[i].ID, id)
		}
	}
}

func TestRecallLocalFactsWithError_Negative(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprintf("directory=%t", directory), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, MemoryFileRel)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if directory {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte("{invalid json"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := RecallLocalFactsWithError(root, "x", ""); err == nil {
				t.Fatal("invalid cache reported as empty matches")
			}
			if facts := RecallLocalFacts(root, "x", ""); len(facts) != 0 {
				t.Fatal("legacy best-effort API changed")
			}
		})
	}
}

func TestRecallLocalFactsWithError_Boundary(t *testing.T) {
	root := t.TempDir()
	if facts, err := RecallLocalFactsWithError(root, "missing", ""); err != nil || len(facts) != 0 {
		t.Fatalf("missing cache must be empty without error: %v %v", facts, err)
	}
	facts := make([]MemoryFact, 11)
	for i := range facts {
		facts[i] = MemoryFact{ID: fmt.Sprint(i), Subject: "match"}
	}
	if err := SaveLocalCache(root, facts); err != nil {
		t.Fatal(err)
	}
	if matches, err := RecallLocalFactsWithError(root, "", ""); err != nil || len(matches) != 10 {
		t.Fatalf("empty-query result cap: %d matches, %v", len(matches), err)
	}
	if matches, err := RecallLocalFactsWithError(root, "absent", ""); err != nil || len(matches) != 0 {
		t.Fatalf("valid zero-match query: %v %v", matches, err)
	}
}

func TestLocalCacheReadRejectsSymlinkAndSizeOverflow(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, MemoryDirRel)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "facts.json")
	if err := os.WriteFile(outsideFile, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, MemoryFileRel)
	if err := os.Symlink(outsideFile, target); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLocalCacheContext(t.Context(), root); err == nil {
		t.Fatal("escaping cache link accepted")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, make([]byte, (1<<20)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLocalCacheContext(t.Context(), root); err == nil {
		t.Fatal("oversized cache accepted")
	}
}

func TestLocalCacheWriteIsPrivateAndRejectsEscapingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := SaveLocalCacheContext(t.Context(), root, []MemoryFact{{ID: "fact", Statement: "retained"}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, MemoryFileRel))
	if err != nil {
		t.Fatal(err)
	}
	if util.ModeIsProtection() && info.Mode().Perm() != 0600 {
		t.Fatalf("private cache mode=%#o", info.Mode().Perm())
	}
	linked, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(linked, ".workingdir")); err != nil {
		t.Fatal(err)
	}
	if err := SaveLocalCacheContext(t.Context(), linked, nil); err == nil {
		t.Fatal("escaping directory accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("memory write escaped root")
	}
}
