// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// countPathMatches counts how many times rel appears in items.
func countPathMatches(items []string, rel string) int {
	count := 0
	for _, item := range items {
		if item == rel {
			count++
		}
	}
	return count
}

// countActionMatches counts how many action details match rel.
func countActionMatches(details []ActionDetail, rel string) int {
	count := 0
	for _, d := range details {
		if d.Path == rel {
			count++
		}
	}
	return count
}

// Positive: a polyglot fixture with a nested go.mod and no Go flavour gets .gosec.json written.
func TestAdopt_Positive_PolyglotWithNestedGoModGetsGosecConfig(t *testing.T) {
	repoPath := newTestRepo(t, "polyglot-nested-go")
	mustWrite(t, filepath.Join(repoPath, "package.json"), "{\"name\":\"polyglot-app\"}\n")
	trackGoModule(t, repoPath, "tooling/go.mod")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)

	gosecPath := filepath.Join(repoPath, ".gosec.json")
	content, err := os.ReadFile(gosecPath)
	if err != nil {
		t.Fatalf("expected .gosec.json to be written: %v", err)
	}
	if !strings.Contains(string(content), "\"exclude\"") {
		t.Fatalf("unexpected .gosec.json content: %s", content)
	}
	if !slices.Contains(rep.CreatedFiles, ".gosec.json") {
		t.Fatalf("expected .gosec.json in CreatedFiles: %v", rep.CreatedFiles)
	}
}

// Negative: a repository without any go.mod gets no .gosec.json.
func TestAdopt_Negative_RepositoryWithoutGoModGetsNoGosecConfig(t *testing.T) {
	repoPath := newTestRepo(t, "no-go-repo")
	mustWrite(t, filepath.Join(repoPath, "package.json"), "{\"name\":\"pure-node\"}\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)

	gosecPath := filepath.Join(repoPath, ".gosec.json")
	if _, err := os.Stat(gosecPath); !os.IsNotExist(err) {
		t.Fatalf("expected no .gosec.json when go.mod is absent, got stat err: %v", err)
	}
	if slices.Contains(rep.CreatedFiles, ".gosec.json") {
		t.Fatalf("unexpected .gosec.json in CreatedFiles: %v", rep.CreatedFiles)
	}
}

// Positive: a Go service repository still gets exactly one .gosec.json.
func TestAdopt_Positive_GoServiceGetsExactlyOneGosecConfig(t *testing.T) {
	repoPath := newTestRepo(t, "go-service-single-gosec")
	trackGoModule(t, repoPath, "go.mod")
	mustWrite(t, filepath.Join(repoPath, "cmd", "svc", "main.go"), "package main\n\nfunc main() {}\n")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)

	createdCount := countPathMatches(rep.CreatedFiles, ".gosec.json")
	if createdCount != 1 {
		t.Fatalf("expected exactly one .gosec.json in CreatedFiles, got %d in %v", createdCount, rep.CreatedFiles)
	}
	actionCount := countActionMatches(rep.ActionDetails, ".gosec.json")
	if actionCount != 1 {
		t.Fatalf("expected exactly one .gosec.json in ActionDetails, got %d in %+v", actionCount, rep.ActionDetails)
	}
}

// Positive: an edited existing .gosec.json is kept and not overwritten.
func TestAdopt_Positive_EditedExistingGosecConfigIsKept(t *testing.T) {
	repoPath := newTestRepo(t, "edited-gosec-repo")
	trackGoModule(t, repoPath, "tooling/go.mod")
	const custom = "{\n  \"global\": {\n    \"exclude\": \"G101\"\n  }\n}\n"
	mustWrite(t, filepath.Join(repoPath, ".gosec.json"), custom)

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}
	assertNoIssues(t, rep)

	after := mustRead(t, filepath.Join(repoPath, ".gosec.json"))
	if after != custom {
		t.Fatalf("edited .gosec.json was overwritten: got %q, want %q", after, custom)
	}
	if slices.Contains(rep.CreatedFiles, ".gosec.json") {
		t.Fatalf("kept .gosec.json must not appear in CreatedFiles: %v", rep.CreatedFiles)
	}
	kept := false
	for _, d := range rep.ActionDetails {
		if d.Path == ".gosec.json" && d.Action == actionSkip && strings.Contains(d.Details, "Existing file kept") {
			kept = true
			break
		}
	}
	if !kept {
		t.Fatalf("expected kept action detail for .gosec.json, got %+v", rep.ActionDetails)
	}
}

// Boundary: when working-dir-and-flavor is declined, .gosec.json is not written.
func TestAdopt_Boundary_DeclinedFlavorWritesNoGosecConfig(t *testing.T) {
	repoPath := newTestRepo(t, "declined-flavor-gosec")
	mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nadoption:\n  decline: [working-dir-and-flavor]\n")
	trackGoModule(t, repoPath, "tooling/go.mod")

	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	if err != nil {
		t.Fatalf("Adopt failed: %v", err)
	}

	gosecPath := filepath.Join(repoPath, ".gosec.json")
	if _, err := os.Stat(gosecPath); !os.IsNotExist(err) {
		t.Fatalf("declined working-dir-and-flavor must not write .gosec.json, stat err: %v", err)
	}
	if slices.Contains(rep.CreatedFiles, ".gosec.json") {
		t.Fatalf("declined .gosec.json must not appear in CreatedFiles: %v", rep.CreatedFiles)
	}
}
