// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// The text register block names a register skill only where the repository carries its
// .agents/skills/<name>/SKILL.md (#235): a name with no skill behind it sent every adopter's
// agents to instructions the repository did not hold.

const (
	namedSocialSkill  = "`social-text` skill:"
	namedCavemanSkill = "`caveman` skill:"
	cavemanBriefShape = "`caveman` brief shape"
)

func writeRegisterSkill(t *testing.T, root, name string) {
	t.Helper()
	writeRegisterFixture(t, root, CanonicalSkillRel(name), "---\nname: "+name+"\ndescription: fixture\n---\n\n# Fixture\n")
}

// Positive: a root carrying both register skills renders the block every earlier release
// rendered, naming both skills and the caveman brief shape.
func TestLoadRegisterBlock_Positive_NamesTheSkillsTheRepositoryCarries(t *testing.T) {
	root := t.TempDir()
	for _, name := range config.RegisterSkills() {
		writeRegisterSkill(t, root, name)
	}
	_, block, err := LoadRegisterBlock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want, err := config.RenderRegisterBlock(config.DefaultRegisterPolicy(), false)
	if err != nil {
		t.Fatal(err)
	}
	if block != want {
		t.Fatalf("block with both skills:\n%s\nwant:\n%s", block, want)
	}
	for _, named := range []string{namedSocialSkill, namedCavemanSkill, cavemanBriefShape} {
		if !strings.Contains(block, named) {
			t.Errorf("block does not name %q", named)
		}
	}
}

// Negative: a root without the skills gets a block that names none of them, and a block that
// still names a skill the repository removed fails compile-context --verify.
func TestLoadRegisterBlock_Negative_NoDanglingSkillName(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_, block, err := LoadRegisterBlock(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, named := range []string{"`social-text`", "`caveman`"} {
		if strings.Contains(block, named) {
			t.Errorf("block without skills names %s:\n%s", named, block)
		}
	}
	if !strings.Contains(block, "Subagent launch brief: internal register with `task:` = routing label.") {
		t.Errorf("brief rule without the caveman skill missing:\n%s", block)
	}
	for _, name := range config.RegisterSkills() {
		writeRegisterSkill(t, root, name)
	}
	agents := writeRegisterFixture(t, root, "AGENTS.md", registerTestSource)
	if _, err := SyncRegisterBlock(ctx, root, agents, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(CanonicalSkillRel("caveman")))); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncRegisterBlock(ctx, root, agents, false); !errors.Is(err, ErrRegisterBlockOutOfSync) {
		t.Fatalf("a block naming a removed skill verified: %v", err)
	}
}

// Boundary: each skill is decided on its own, a skill the caller is about to write counts as
// present, and a skill path behind a symlink is an error rather than a guess.
func TestLoadRegisterBlock_Boundary_EachSkillOnItsOwn(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeRegisterSkill(t, root, "caveman")
	_, block, err := LoadRegisterBlock(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(block, namedSocialSkill) || !strings.Contains(block, namedCavemanSkill) || !strings.Contains(block, cavemanBriefShape) {
		t.Fatalf("only caveman carried:\n%s", block)
	}
	_, pending, err := LoadRegisterBlockOver(ctx, root, []string{"social-text"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pending, namedSocialSkill) {
		t.Fatalf("a pending social-text skill is not named:\n%s", pending)
	}
	linked := t.TempDir()
	target := t.TempDir()
	writeRegisterSkill(t, target, "caveman")
	if err := os.MkdirAll(filepath.Join(linked, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(target, ".agents", "skills"), filepath.Join(linked, ".agents", "skills")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	if _, _, err := LoadRegisterBlock(ctx, linked); err == nil || !strings.Contains(err.Error(), "text register: read") {
		t.Fatalf("a symlinked skills directory: err = %v", err)
	}
}

// Positive: ReadCanonicalSkill, the one reader of a canonical skill, returns a carried skill's
// bytes, and AbsentRegisterSkills, which the block, the Paperclip harness and adoption share,
// then leaves it out.
func TestAbsentRegisterSkills_Positive_CarriedSkillIsRead(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeRegisterSkill(t, root, "caveman")
	data, exists, err := ReadCanonicalSkill(ctx, root, "caveman")
	if err != nil || !exists || !strings.Contains(string(data), "name: caveman") {
		t.Fatalf("ReadCanonicalSkill = %q, %v, %v", data, exists, err)
	}
	absent, err := AbsentRegisterSkills(ctx, root, nil)
	if err != nil || len(absent) != 1 || absent[0] != "social-text" {
		t.Fatalf("AbsentRegisterSkills = %v, %v; want only social-text", absent, err)
	}
}

// Negative: a directory where SKILL.md belongs is refused by both, never read as absent or
// carried.
func TestAbsentRegisterSkills_Negative_UnreadableSkillIsAnError(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(CanonicalSkillRel("social-text"))), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadCanonicalSkill(ctx, root, "social-text"); err == nil || !strings.Contains(err.Error(), "read canonical skill social-text") {
		t.Fatalf("ReadCanonicalSkill of a directory: %v", err)
	}
	if absent, err := AbsentRegisterSkills(ctx, root, nil); err == nil {
		t.Fatalf("AbsentRegisterSkills of a directory = %v, want an error", absent)
	}
}

// Boundary: a root without .agents reads every skill as absent without an error, a pending name
// counts as carried, and the path helpers agree on the canonical location.
func TestAbsentRegisterSkills_Boundary_NoSkillsDirectoryAndPending(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if _, exists, err := ReadCanonicalSkill(ctx, root, "caveman"); err != nil || exists {
		t.Fatalf("ReadCanonicalSkill without .agents = %v, %v", exists, err)
	}
	absent, err := AbsentRegisterSkills(ctx, root, nil)
	if err != nil || strings.Join(absent, ",") != "social-text,caveman" {
		t.Fatalf("AbsentRegisterSkills without .agents = %v, %v", absent, err)
	}
	if absent, err := AbsentRegisterSkills(ctx, root, config.RegisterSkills()); err != nil || len(absent) != 0 {
		t.Fatalf("AbsentRegisterSkills with every skill pending = %v, %v", absent, err)
	}
	if got := CanonicalSkillRel("caveman"); got != ".agents/skills/caveman/SKILL.md" || got != SkillEntryRel(CanonicalSkillsRel, "caveman") {
		t.Fatalf("CanonicalSkillRel = %q", got)
	}
}
