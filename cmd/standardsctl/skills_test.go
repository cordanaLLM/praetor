// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

func createUpstreamSkillFixture(t *testing.T, tag, skillName, skillMD, licenseText string) (string, string) {
	t.Helper()
	env := testsupport.HermeticGitEnv(t)
	dir := t.TempDir()
	if _, err := runFixtureGit(t, dir, env, "init", "-q", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := runFixtureGit(t, dir, env, "config", "user.name", "test"); err != nil {
		t.Fatalf("git config user.name: %v", err)
	}
	if _, err := runFixtureGit(t, dir, env, "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("git config user.email: %v", err)
	}

	skillDir := filepath.Join(dir, ".agents", "skills", skillName)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0644); err != nil {
		t.Fatal(err)
	}
	if licenseText != "" {
		if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte(licenseText), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runFixtureGit(t, dir, env, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := runFixtureGit(t, dir, env, "commit", "-q", "-m", "release "+tag); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	head, err := runFixtureGit(t, dir, env, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	head = strings.TrimSpace(head)
	if _, err := runFixtureGit(t, dir, env, "tag", "-a", tag, "-m", "annotated release "+tag); err != nil {
		t.Fatalf("git tag: %v", err)
	}
	return dir, head
}

func setupAdopterRepo(t *testing.T, upstreamRepo, tag, commit, skillName string) string {
	t.Helper()
	root := t.TempDir()
	manifestContent := fmt.Sprintf(`version: 1
agent_clients:
  - claude
  - codex
skills:
  upstream:
    - repo: %s
      tag: %s
      commit: %s
      skills:
        - %s
`, upstreamRepo, tag, commit, skillName)
	if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte(manifestContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Context\n\nagent context\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSkillsPinSync_Positive_FetchedRecordedProjected(t *testing.T) {
	tag := "v1.0.0"
	skillName := "external-helper"
	skillMD := "---\nname: external-helper\ndescription: Helper skill.\n---\n# External Helper\n\nCommand runner.\n"
	license := "MIT License\n\nCopyright 2026 Upstream Author\n"

	upstreamRepo, commit := createUpstreamSkillFixture(t, tag, skillName, skillMD, license)
	adopterRoot := setupAdopterRepo(t, upstreamRepo, tag, commit, skillName)

	if err := runSkills([]string{"pin-sync", "--root", adopterRoot}); err != nil {
		t.Fatalf("pin-sync failed: %v", err)
	}

	canonicalDir := filepath.Join(adopterRoot, ".agents", "skills", skillName)
	if _, err := os.Stat(filepath.Join(canonicalDir, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not fetched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(canonicalDir, "LICENSE")); err != nil {
		t.Fatalf("LICENSE not fetched: %v", err)
	}

	record, exists, err := caveman.ReadSkillPinRecord(canonicalDir)
	if err != nil || !exists {
		t.Fatalf("ReadSkillPinRecord failed: exists=%v, err=%v", exists, err)
	}
	if record.Repo != upstreamRepo || record.Tag != tag || record.Commit != commit {
		t.Errorf("pin record mismatch: %+v", record)
	}
	if record.EffectiveLicense() != "MIT" {
		t.Errorf("expected licence MIT, got %q", record.EffectiveLicense())
	}
	if len(record.Files) != 2 {
		t.Errorf("expected 2 recorded files, got %d: %+v", len(record.Files), record.Files)
	}

	if err := runSkills([]string{"pin-verify", "--root", adopterRoot}); err != nil {
		t.Fatalf("pin-verify failed: %v", err)
	}

	if err := runCompileContext([]string{"-target-dir", adopterRoot, "-source", filepath.Join(adopterRoot, "AGENTS.md")}); err != nil {
		t.Fatalf("runCompileContext failed: %v", err)
	}

	claudeSkillDir := filepath.Join(adopterRoot, ".claude", "skills", skillName)
	if _, err := os.Stat(filepath.Join(claudeSkillDir, "SKILL.md")); err != nil {
		t.Fatalf("projected claude SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(claudeSkillDir, "LICENSE")); err != nil {
		t.Fatalf("projected claude LICENSE missing: %v", err)
	}

	if _, err := compiler.VerifyClientSkills(t.Context(), adopterRoot); err != nil {
		t.Fatalf("VerifyClientSkills failed: %v", err)
	}
}

func TestSkillsPinSync_Positive_LongPinnedSkillSkipsCavemanLint(t *testing.T) {
	tag := "v1.1.0"
	skillName := "long-upstream-skill"
	// Generate a skill text with > 600 words of prose that would normally fail caveman lint
	proseBuilder := strings.Builder{}
	proseBuilder.WriteString("---\nname: long-upstream-skill\ndescription: A comprehensive guide.\n---\n# Guide\n\n")
	for i := 0; i < 700; i++ {
		fmt.Fprintf(&proseBuilder, "paragraph word sequence number %d in the upstream documentation document text. ", i)
	}
	skillMD := proseBuilder.String()
	license := "Apache License, Version 2.0\n"

	upstreamRepo, commit := createUpstreamSkillFixture(t, tag, skillName, skillMD, license)
	adopterRoot := setupAdopterRepo(t, upstreamRepo, tag, commit, skillName)

	if err := runSkills([]string{"pin-sync", "--root", adopterRoot}); err != nil {
		t.Fatalf("pin-sync failed: %v", err)
	}

	if _, err := compiler.LintCanonicalSkillFiles(t.Context(), adopterRoot); err != nil {
		t.Fatalf("LintCanonicalSkillFiles failed for valid pinned skill: %v", err)
	}
}

func TestSkillsPinSync_Negative_TamperedPinnedSkillRefused(t *testing.T) {
	tag := "v1.0.0"
	skillName := "tamper-check"
	skillMD := "---\nname: tamper-check\ndescription: Test tamper.\n---\n# Content\n"
	license := "MIT License\n"

	upstreamRepo, commit := createUpstreamSkillFixture(t, tag, skillName, skillMD, license)
	adopterRoot := setupAdopterRepo(t, upstreamRepo, tag, commit, skillName)

	if err := runSkills([]string{"pin-sync", "--root", adopterRoot}); err != nil {
		t.Fatalf("pin-sync failed: %v", err)
	}

	skillFile := filepath.Join(adopterRoot, ".agents", "skills", skillName, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte(skillMD+"\n# Tampered Modification\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := runSkills([]string{"pin-verify", "--root", adopterRoot}); err == nil {
		t.Fatal("pin-verify expected to fail on tampered file, got nil")
	}

	if _, err := compiler.LintCanonicalSkillFiles(t.Context(), adopterRoot); err == nil {
		t.Fatal("LintCanonicalSkillFiles expected to fail on tampered file, got nil")
	}
}

func TestSkillsPinSync_Negative_ManifestCommitNot40HexRefused(t *testing.T) {
	root := t.TempDir()
	manifestContent := `version: 1
skills:
  upstream:
    - repo: https://github.com/example/skills
      tag: v1.0.0
      commit: not-40-hex
      skills:
        - helper
`
	if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte(manifestContent), 0644); err != nil {
		t.Fatal(err)
	}

	err := runSkills([]string{"pin-sync", "--root", root})
	if err == nil || !strings.Contains(err.Error(), "must be 40 hex characters") {
		t.Fatalf("expected 40-hex commit refusal, got %v", err)
	}
}

func TestSkillsPinSync_Negative_CloneHeadMismatchRefused(t *testing.T) {
	tag := "v1.0.0"
	skillName := "head-mismatch"
	skillMD := "---\nname: head-mismatch\ndescription: Test head mismatch.\n---\n# Content\n"

	upstreamRepo, _ := createUpstreamSkillFixture(t, tag, skillName, skillMD, "MIT License\n")
	fakeCommit := "0123456789abcdef0123456789abcdef01234567"
	adopterRoot := setupAdopterRepo(t, upstreamRepo, tag, fakeCommit, skillName)

	err := runSkills([]string{"pin-sync", "--root", adopterRoot})
	if err == nil || !strings.Contains(err.Error(), "does not match pinned commit") {
		t.Fatalf("expected clone head mismatch refusal, got %v", err)
	}
}

func TestSkillsPinSync_Boundary_EmptyUpstreamChangesNothing(t *testing.T) {
	root := t.TempDir()
	manifestContent := `version: 1
skills:
  upstream: []
`
	if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte(manifestContent), 0644); err != nil {
		t.Fatal(err)
	}

	if err := runSkills([]string{"pin-sync", "--root", root}); err != nil {
		t.Fatalf("pin-sync on empty upstream failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
		t.Errorf("expected .agents not to exist, got %v", err)
	}
}

func TestSkillsPinSync_Negative_NoLicenseRefused(t *testing.T) {
	tag := "v1.0.0"
	skillName := "unlicensed-skill"
	// No license in front matter, no LICENSE file in repo
	skillMD := "---\nname: unlicensed-skill\ndescription: Test no license.\n---\n# Content\n"

	upstreamRepo, commit := createUpstreamSkillFixture(t, tag, skillName, skillMD, "")
	adopterRoot := setupAdopterRepo(t, upstreamRepo, tag, commit, skillName)

	err := runSkills([]string{"pin-sync", "--root", adopterRoot})
	if err == nil {
		t.Fatal("expected pin-sync to fail when no licence identified, got nil")
	}
	if !strings.Contains(err.Error(), skillName) {
		t.Errorf("expected error to name skill %q, got: %v", skillName, err)
	}
	if !strings.Contains(err.Error(), "licence") {
		t.Errorf("expected error to mention licence, got: %v", err)
	}

	canonicalDir := filepath.Join(adopterRoot, ".agents", "skills", skillName)
	if _, err := os.Stat(filepath.Join(canonicalDir, caveman.SkillPinRecordName)); !os.IsNotExist(err) {
		t.Errorf("expected no pin record to be written in %s, got: %v", canonicalDir, err)
	}
}

func TestSkillsPinSync_Positive_FrontMatterLicenseDetected(t *testing.T) {
	tag := "v1.0.0"
	skillName := "fm-licensed-skill"
	// No LICENSE file in repo, but front matter declares license
	skillMD := "---\nname: fm-licensed-skill\ndescription: Test front matter license.\nlicense: Apache-2.0\n---\n# Content\n"

	upstreamRepo, commit := createUpstreamSkillFixture(t, tag, skillName, skillMD, "")
	adopterRoot := setupAdopterRepo(t, upstreamRepo, tag, commit, skillName)

	if err := runSkills([]string{"pin-sync", "--root", adopterRoot}); err != nil {
		t.Fatalf("pin-sync failed on front matter license: %v", err)
	}

	canonicalDir := filepath.Join(adopterRoot, ".agents", "skills", skillName)
	record, exists, err := caveman.ReadSkillPinRecord(canonicalDir)
	if err != nil || !exists {
		t.Fatalf("ReadSkillPinRecord failed: exists=%v, err=%v", exists, err)
	}
	if record.EffectiveLicense() != "Apache-2.0" {
		t.Errorf("expected licence Apache-2.0, got %q", record.EffectiveLicense())
	}
}

// Negative (planted test): a fixture clone whose skill folder holds a symlink to /etc/passwd is refused.
func TestSkillsPinSync_Negative_SymlinkToPasswdRefused(t *testing.T) {
	tag := "v1.0.0"
	skillName := "symlink-skill"
	skillMD := "---\nname: symlink-skill\ndescription: Test symlink refusal.\n---\n# Content\n"
	license := "MIT License\n"

	env := testsupport.HermeticGitEnv(t)
	dir := t.TempDir()
	if _, err := runFixtureGit(t, dir, env, "init", "-q", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := runFixtureGit(t, dir, env, "config", "user.name", "test"); err != nil {
		t.Fatalf("git config user.name: %v", err)
	}
	if _, err := runFixtureGit(t, dir, env, "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("git config user.email: %v", err)
	}

	skillDir := filepath.Join(dir, ".agents", "skills", skillName)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte(license), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(skillDir, "leak")); err != nil {
		t.Skipf("cannot create symlink on this filesystem: %v", err)
	}
	if _, err := runFixtureGit(t, dir, env, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := runFixtureGit(t, dir, env, "commit", "-q", "-m", "release "+tag); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	head, err := runFixtureGit(t, dir, env, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	head = strings.TrimSpace(head)
	if _, err := runFixtureGit(t, dir, env, "tag", "-a", tag, "-m", "annotated release "+tag); err != nil {
		t.Fatalf("git tag: %v", err)
	}

	adopterRoot := setupAdopterRepo(t, dir, tag, head, skillName)
	err = runSkills([]string{"pin-sync", "--root", adopterRoot})
	if err == nil {
		t.Fatal("expected pin-sync to fail on symlink in skill folder, got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected error mentioning symlink, got: %v", err)
	}
}

// Negative (planted test): manifest declaring skill name ../evil is refused.
func TestSkillsPinSync_Negative_SkillNameEvilRefused(t *testing.T) {
	root := t.TempDir()
	manifestContent := `version: 1
skills:
  upstream:
    - repo: https://github.com/example/skills
      tag: v1.0.0
      commit: 0123456789abcdef0123456789abcdef01234567
      skills:
        - ../evil
`
	if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte(manifestContent), 0644); err != nil {
		t.Fatal(err)
	}
	err := runSkills([]string{"pin-sync", "--root", root})
	if err == nil || !strings.Contains(err.Error(), "not a valid skill name") {
		t.Fatalf("expected refusal of skill name ../evil, got: %v", err)
	}
}
