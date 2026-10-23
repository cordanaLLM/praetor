// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestPlantedReadmeBaselineTreeInput(t *testing.T) {
	dir := newHermeticGitRepo(t)
	const file = ".readme-baseline.json"
	before := subtractiveInputs(dir)
	if slices.Contains(before, file) {
		t.Fatal("absent README baseline included as optional input")
	}
	commitFile(t, dir, ".gitignore", file+"\n")
	writeFile(t, filepath.Join(dir, file), "{}\n")
	after := subtractiveInputs(dir)
	if !slices.Contains(after, file) || len(after) != len(before)+1 {
		t.Fatalf("README baseline does not change gate inputs: before=%v after=%v", before, after)
	}
	refusedRun(t, dir, file+" is not tracked")
	commitFile(t, dir, file, "{}\n")
	if tree := inspectTree(t.Context(), dir); tree.problem != "" {
		t.Fatalf("committed baseline refused: %s", tree.problem)
	}
	writeFile(t, filepath.Join(dir, file), "{\"total_infractions\":1}\n")
	refusedRun(t, dir, file)
}
