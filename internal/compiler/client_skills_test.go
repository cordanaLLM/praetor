// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// The register skills Praetor ships are copied into the skill directory of every selected agent
// client that does not read .agents/skills (#235): Claude Code reads .claude/skills alone, so the
// skills the register block names were invisible to it.

// ownSkill is a skill of the repository's own, which compile-context never projects.
const ownSkill = "release-notes"

// writeClientSkillFixture builds a root carrying every bundle skill and one skill of its own
// under .agents/skills, with manifest as its .standards.yaml when set.
func writeClientSkillFixture(t *testing.T, manifest string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range append(config.RegisterSkillBundle(), ownSkill) {
		writeRegisterSkill(t, root, name)
	}
	if manifest != "" {
		writeRegisterFixture(t, root, ".standards.yaml", manifest)
	}
	return root
}

// relPaths lists the relative paths of files.
func relPaths(files []TargetFile) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.RelativePath)
	}
	return paths
}

// Positive: compile-context writes a copy of each bundle skill into .claude/skills, never one of
// the repository's own skills, and --verify accepts the copies beside a skill the client
// directory keeps of its own.
func TestCompileAgentSurfaces_Positive_ProjectsTheRegisterSkills(t *testing.T) {
	root := writeClientSkillFixture(t, "")
	files, err := CompileAgentSurfaces(t.Context(), io.Discard, root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".claude/skills/social-text/SKILL.md", ".claude/skills/caveman/SKILL.md", ".claude/skills/adhd-format/SKILL.md"}
	if got := relPaths(files); !slices.Equal(got, want) {
		t.Fatalf("projected %v, want %v", got, want)
	}
	for _, rel := range want {
		name := filepath.Base(filepath.Dir(rel))
		canonical := readRegisterFixture(t, filepath.Join(root, filepath.FromSlash(CanonicalSkillRel(name))))
		if got := readRegisterFixture(t, filepath.Join(root, filepath.FromSlash(rel))); got != canonical {
			t.Errorf("%s = %q, want the canonical text %q", rel, got, canonical)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "skills", ownSkill)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the repository's own skill was projected (stat err=%v)", err)
	}
	writeRegisterFixture(t, root, ".claude/skills/claude-only/SKILL.md", "---\nname: claude-only\n---\n")
	if verified, err := VerifyClientSkills(t.Context(), root); err != nil || verified != 3 {
		t.Fatalf("VerifyClientSkills = %d, %v; want 3 copies verified", verified, err)
	}
}

// Negative: a missing or edited copy fails verification as projection drift, a pending skill
// Praetor does not ship is refused, and a symlinked skill directory is refused before anything
// is written.
func TestVerifyClientSkills_Negative_MissingOrEditedCopy(t *testing.T) {
	root := writeClientSkillFixture(t, "")
	if _, err := VerifyClientSkills(t.Context(), root); err == nil || !strings.Contains(err.Error(), ".claude/skills/social-text/SKILL.md") {
		t.Fatalf("a missing copy verified: %v", err)
	}
	if _, err := CompileAgentSurfaces(t.Context(), io.Discard, root); err != nil {
		t.Fatal(err)
	}
	writeRegisterFixture(t, root, ".claude/skills/caveman/SKILL.md", "---\nname: caveman\n---\n\n# Edited\n")
	if _, err := VerifyClientSkills(t.Context(), root); !errors.Is(err, ErrAgentProjectionDrift) {
		t.Fatalf("an edited copy: err = %v, want ErrAgentProjectionDrift", err)
	}
	if _, err := PlanAgentSurfacesOver(t.Context(), root, PendingSources{Skills: map[string][]byte{ownSkill: []byte("x")}}); err == nil {
		t.Fatal("a pending skill Praetor does not ship was planned")
	}
	if err := CheckSkillTargets(t.Context(), root, []string{"../escape"}); err == nil {
		t.Fatal("CheckSkillTargets accepted a name Praetor does not ship")
	}
	linked := t.TempDir()
	writeRegisterSkill(t, linked, "caveman")
	if err := os.MkdirAll(filepath.Join(linked, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(linked, ".agents", "skills"), filepath.Join(linked, ".claude", "skills")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	if err := CheckSkillTargets(t.Context(), linked, nil); err == nil {
		t.Fatal("CheckSkillTargets accepted a symlinked .claude/skills")
	}
	if _, err := CompileAgentSurfaces(t.Context(), io.Discard, linked); err == nil {
		t.Fatal("compile-context wrote through a symlinked .claude/skills")
	}
}

// Boundary: a selection without Claude Code projects and verifies no copy, a repository that
// carries no bundle skill gets none, a pending skill is planned before it exists, and the
// skills about to be installed are checked on an empty repository.
func TestClientSkillProjections_Boundary(t *testing.T) {
	codex := writeClientSkillFixture(t, "version: 1\nagent_clients: [codex, gemini]\n")
	files, err := CompileAgentSurfaces(t.Context(), io.Discard, codex)
	if err != nil {
		t.Fatal(err)
	}
	if got := relPaths(files); slices.ContainsFunc(got, func(rel string) bool { return strings.HasSuffix(rel, "/"+SkillEntryName) }) {
		t.Fatalf("a selection without Claude Code projected skills: %v", got)
	}
	if verified, err := VerifyClientSkills(t.Context(), codex); err != nil || verified != 0 {
		t.Fatalf("VerifyClientSkills without Claude Code = %d, %v", verified, err)
	}
	empty := t.TempDir()
	if verified, err := VerifyClientSkills(t.Context(), empty); err != nil || verified != 0 {
		t.Fatalf("VerifyClientSkills without skills = %d, %v", verified, err)
	}
	planned, err := PlanAgentSurfacesOver(t.Context(), empty, PendingSources{Skills: map[string][]byte{"caveman": []byte("# pending\n")}})
	if err != nil {
		t.Fatal(err)
	}
	if got := relPaths(planned); !slices.Equal(got, []string{".claude/skills/caveman/SKILL.md"}) || planned[0].Content != "# pending\n" {
		t.Fatalf("pending caveman planned %+v", planned)
	}
	if err := CheckSkillTargets(t.Context(), empty, config.RegisterSkillBundle()); err != nil {
		t.Fatalf("CheckSkillTargets on an empty repository: %v", err)
	}
	if _, err := os.Stat(filepath.Join(empty, ".claude")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("planning and checking wrote .claude (stat err=%v)", err)
	}
}
