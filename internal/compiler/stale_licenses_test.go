// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adopterOwnedCaveman leaves a repository whose canonical caveman skill is absent and whose
// .claude/skills/caveman is the adopter's own copy, with a LICENSE of its own.
func adopterOwnedCaveman(t *testing.T) string {
	t.Helper()
	root := writeClientSkillFixture(t, "")
	if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(CanonicalSkillsRel), "caveman")); err != nil {
		t.Fatal(err)
	}
	return root
}

func exists(t *testing.T, root, rel string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// Negative: a client copy of a skill the repository does not carry belongs to the adopter. Verify
// passes, and compile keeps its SKILL.md and LICENSE.
func TestStaleClientLicenses_Negative_AdopterOwnedCopyIsLeftAlone(t *testing.T) {
	root := adopterOwnedCaveman(t)
	writeRegisterFixture(t, root, ".claude/skills/caveman/SKILL.md", "own skill\n")
	writeRegisterFixture(t, root, ".claude/skills/caveman/LICENSE", licenseFixture)
	if _, err := CompileAgentSurfaces(t.Context(), io.Discard, root); err != nil {
		t.Fatalf("compile touched an adopter-owned copy: %v", err)
	}
	if _, err := VerifyClientSkills(t.Context(), root); err != nil {
		t.Fatalf("verify refused an adopter-owned copy: %v", err)
	}
	if !exists(t, root, ".claude/skills/caveman/LICENSE") || !exists(t, root, ".claude/skills/caveman/SKILL.md") {
		t.Fatal("the adopter's own caveman copy lost a file")
	}
}

// Negative: a symlinked client directory of a skill the repository does not carry is the
// adopter's too: neither verify nor compile follows or refuses it.
func TestStaleClientLicenses_Negative_SymlinkedAdopterCopyIsLeftAlone(t *testing.T) {
	root := adopterOwnedCaveman(t)
	elsewhere := t.TempDir()
	writeRegisterFixture(t, elsewhere, "LICENSE", licenseFixture)
	link := filepath.Join(root, ".claude", "skills", "caveman")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Skipf("symlinks are unavailable on this platform: %v", err)
	}
	if _, err := CompileAgentSurfaces(t.Context(), io.Discard, root); err != nil {
		t.Fatalf("compile refused a symlinked adopter copy: %v", err)
	}
	if _, err := VerifyClientSkills(t.Context(), root); err != nil {
		t.Fatalf("verify refused a symlinked adopter copy: %v", err)
	}
	if !exists(t, elsewhere, "LICENSE") {
		t.Fatal("compile removed a file behind the adopter's symlink")
	}
}

// Positive: a skill Praetor ships whose canonical LICENSE was dropped gets its stale client copy
// removed, and the plan lists the removal (Remove) before anything is written.
func TestStaleClientLicenses_Positive_ShippedSkillCopyIsRemovedAndReported(t *testing.T) {
	root := writeClientSkillFixture(t, "")
	writeRegisterFixture(t, root, ".claude/skills/caveman/LICENSE", licenseFixture)
	planned, err := PlanAgentSurfaces(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	var removals []string
	for _, file := range planned {
		if file.Remove {
			removals = append(removals, file.RelativePath)
		}
	}
	if len(removals) != 1 || removals[0] != ".claude/skills/caveman/LICENSE" {
		t.Fatalf("planned removals = %v", removals)
	}
	if !exists(t, root, removals[0]) {
		t.Fatal("planning removed the file")
	}
	var report bytes.Buffer
	if _, err := CompileAgentSurfaces(t.Context(), &report, root); err != nil {
		t.Fatal(err)
	}
	if exists(t, root, removals[0]) {
		t.Fatal("the stale LICENSE copy survived the compile")
	}
	if !strings.Contains(report.String(), "[COMPILED] removed 1 stale skill licence copies (.claude/skills/caveman/LICENSE)") {
		t.Fatalf("report does not name the removal:\n%s", report.String())
	}
}

// Negative: a symlinked client directory of a skill Praetor ships is refused with an error
// naming it, by plan, verify and compile alike, and compile writes nothing.
func TestStaleClientLicenses_Negative_SymlinkedShippedSkillIsRefused(t *testing.T) {
	root := writeClientSkillFixture(t, "")
	elsewhere := t.TempDir()
	writeRegisterFixture(t, elsewhere, "LICENSE", licenseFixture)
	if err := os.MkdirAll(filepath.Join(root, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(root, ".claude", "skills", "caveman")); err != nil {
		t.Skipf("symlinks are unavailable on this platform: %v", err)
	}
	if _, err := PlanAgentSurfaces(t.Context(), root); err == nil || !strings.Contains(err.Error(), "caveman") {
		t.Fatalf("plan: err = %v", err)
	}
	if _, err := CompileAgentSurfaces(t.Context(), io.Discard, root); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("compile: err = %v", err)
	}
	if exists(t, root, ".claude/skills/social-text/SKILL.md") || !exists(t, elsewhere, "LICENSE") {
		t.Fatal("a refused compile wrote or removed a file")
	}
}
