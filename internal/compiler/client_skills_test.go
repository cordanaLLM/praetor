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

// licenseFixture is the LICENSE the licence projection tests write beside each skill.
const licenseFixture = "MIT License\n\nCopyright (c) 2026 Fixture Holder\n"

// Positive: a bundle skill's LICENSE travels with its SKILL.md into the client skill directory and
// into the plugin, byte for byte, and --verify accepts both. Without it the licence stayed in
// the checkout while the copies an agent client or the plugin installs outside it went without.
func TestSkillLicenseProjection_Positive_TravelsWithTheSkill(t *testing.T) {
	root := writeClientSkillFixture(t, "")
	writeRegisterFixture(t, root, PluginManifestRel, `{"name":"praetor"}`)
	for _, name := range config.RegisterSkillBundle() {
		writeRegisterFixture(t, root, CanonicalSkillLicenseRel(name), licenseFixture)
	}
	files, err := CompileAgentSurfaces(t.Context(), io.Discard, root)
	if err != nil {
		t.Fatal(err)
	}
	got := relPaths(files)
	for _, name := range config.RegisterSkillBundle() {
		for _, dir := range []string{".claude/skills", PluginSkillsRel} {
			rel := SkillLicenseRel(dir, name)
			if !slices.Contains(got, rel) {
				t.Errorf("%s not projected: %v", rel, got)
			} else if text := readRegisterFixture(t, filepath.Join(root, filepath.FromSlash(rel))); text != licenseFixture {
				t.Errorf("%s = %q, want the canonical licence", rel, text)
			}
		}
	}
	if skillOwn := SkillLicenseRel(".claude/skills", ownSkill); slices.Contains(got, skillOwn) {
		t.Errorf("the repository's own skill got a licence copy: %s", skillOwn)
	}
	if _, err := VerifyClientSkills(t.Context(), root); err != nil {
		t.Fatalf("VerifyClientSkills: %v", err)
	}
	if _, err := VerifyPluginSkills(t.Context(), root); err != nil {
		t.Fatalf("VerifyPluginSkills: %v", err)
	}
}

// compiledLicenseFixture compiles a repository whose caveman skill carries a LICENSE and ships
// the plugin, and returns its root.
func compiledLicenseFixture(t *testing.T) string {
	t.Helper()
	root := writeClientSkillFixture(t, "")
	writeRegisterFixture(t, root, PluginManifestRel, `{"name":"praetor"}`)
	writeRegisterFixture(t, root, CanonicalSkillLicenseRel("caveman"), licenseFixture)
	if _, err := VerifyClientSkills(t.Context(), root); err == nil {
		t.Fatal("a repository whose copies were never compiled verified")
	}
	if _, err := CompileAgentSurfaces(t.Context(), io.Discard, root); err != nil {
		t.Fatal(err)
	}
	return root
}

// Negative: an edited licence copy fails verification as projection drift, in the client directory
// and in the plugin alike.
func TestSkillLicenseProjection_Negative_EditedCopy(t *testing.T) {
	root := compiledLicenseFixture(t)
	for _, dir := range []string{".claude/skills", PluginSkillsRel} {
		writeRegisterFixture(t, root, SkillLicenseRel(dir, "caveman"), licenseFixture+"edited\n")
	}
	if _, err := VerifyClientSkills(t.Context(), root); !errors.Is(err, ErrAgentProjectionDrift) || !strings.Contains(err.Error(), ".claude/skills/caveman/LICENSE") {
		t.Fatalf("an edited client licence: err = %v", err)
	}
	if _, err := VerifyPluginSkills(t.Context(), root); !errors.Is(err, ErrAgentProjectionDrift) || !strings.Contains(err.Error(), PluginSkillsRel+"/caveman/LICENSE") {
		t.Fatalf("an edited plugin licence: err = %v", err)
	}
}

// Negative: a missing licence copy fails verification, in the client directory and in the plugin
// alike.
func TestSkillLicenseProjection_Negative_MissingCopy(t *testing.T) {
	root := compiledLicenseFixture(t)
	for _, dir := range []string{".claude/skills", PluginSkillsRel} {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(SkillLicenseRel(dir, "caveman")))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := VerifyClientSkills(t.Context(), root); err == nil || !strings.Contains(err.Error(), ".claude/skills/caveman/LICENSE") {
		t.Fatalf("a missing client licence: err = %v", err)
	}
	if _, err := VerifyPluginSkills(t.Context(), root); err == nil || !strings.Contains(err.Error(), PluginSkillsRel+"/caveman/LICENSE") {
		t.Fatalf("a missing plugin licence: err = %v", err)
	}
}

// Boundary: a pending licence is planned before it exists, a pending licence for a skill Praetor
// does not ship is refused, and CheckSkillTargets covers the licence targets.
func TestSkillLicenseProjection_Boundary_PendingAndTargets(t *testing.T) {
	empty := t.TempDir()
	planned, err := PlanAgentSurfacesOver(t.Context(), empty, PendingSources{
		Skills:   map[string][]byte{"caveman": []byte("# pending\n")},
		Licenses: map[string][]byte{"caveman": []byte(licenseFixture)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := relPaths(planned); !slices.Equal(got, []string{".claude/skills/caveman/SKILL.md", ".claude/skills/caveman/LICENSE"}) {
		t.Fatalf("pending caveman planned %v", got)
	}
	if _, err := PlanAgentSurfacesOver(t.Context(), empty, PendingSources{Licenses: map[string][]byte{ownSkill: []byte("x")}}); err == nil {
		t.Fatal("a pending licence for a skill Praetor does not ship was planned")
	}
	linked := t.TempDir()
	writeRegisterSkill(t, linked, "caveman")
	writeRegisterFixture(t, linked, ".claude/skills/caveman/SKILL.md", "x")
	if err := os.Symlink(filepath.Join(linked, "elsewhere"), filepath.Join(linked, ".claude", "skills", "caveman", "LICENSE")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	if err := CheckSkillTargets(t.Context(), linked, []string{"caveman"}); err == nil || !strings.Contains(err.Error(), "LICENSE") {
		t.Fatalf("CheckSkillTargets accepted a symlinked licence target: %v", err)
	}
}
