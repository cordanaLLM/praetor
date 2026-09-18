package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestListCanonicalAgentsDistinguishesAbsentFromMisplaced: a repository without canonical
// personas projects none, but a regular file at .agents or .agents/agents is a broken layout and
// must be reported rather than projected as "no personas" -- which is what Windows did.
func TestListCanonicalAgentsDistinguishesAbsentFromMisplaced(t *testing.T) {
	if names, err := listCanonicalAgents(t.TempDir()); err != nil || len(names) != 0 {
		t.Fatalf("absent persona directory was not empty: %v %v", names, err)
	}
	for _, file := range []string{".agents", filepath.FromSlash(canonicalAgentsRel)} {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, file)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, file), []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := listCanonicalAgents(root); err == nil {
			t.Fatalf("a regular file at %s was read as an empty persona directory", file)
		}
	}
}
