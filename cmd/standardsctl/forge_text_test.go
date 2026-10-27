// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// Order: engine-fr-rule-checks-20261027. The offline hook must refuse the planted reference.
func TestForgeText_CheckMessageForeign(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "message", "fix: reject accidental references\n\nSee foreign-org/tool#12.\n")
	err := dispatchCommand("forge", []string{"check-message", filepath.Join(dir, "message")})
	if err == nil || !strings.Contains(err.Error(), "foreign reference") {
		t.Fatalf("want foreign reference refusal: %v", err)
	}
}

func TestForgeText_CheckMessageOwn(t *testing.T) {
	dir := t.TempDir()
	env := initGitFixture(t, dir)
	if out, err := runFixtureGit(t, dir, env, "remote", "add", "origin", "https://github.com/acme/tool.git"); err != nil {
		t.Fatalf("remote: %s %v", out, err)
	}
	t.Setenv("GITHUB_REPOSITORY", "")
	writeFixtureFile(t, dir, ".standards.yaml", "forge:\n  own_organizations: [partners]\n")
	for _, text := range []string{"ACME/tool#12", "partners/tool!7", "#12", "`foreign-org/tool#12`"} {
		path := writeFixtureFile(t, dir, "message", "fix: keep own references\n\nSee "+text+".\n")
		if err := dispatchCommand("forge", []string{"check-message", "--repo=" + dir, path}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestForgeText_CheckCommitsForeign(t *testing.T) {
	dir := t.TempDir()
	env := initGitFixture(t, dir)
	writeFixtureFile(t, dir, "change", "fixture")
	gitCommitAll(t, dir, env, "fix: planted foreign reference\n\nSee foreign-org/tool#12.")
	err := dispatchCommand("forge", []string{"check-commits", "--repo=" + dir, "--base=HEAD~1"})
	if err == nil || !strings.Contains(err.Error(), "foreign reference") {
		t.Fatalf("want refusal, got %v", err)
	}
}

func TestForgeText_ValidatePR(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	path := writeFixtureFile(t, dir, "body", "First prose line\nsecond prose line.\n\nforeign-org/tool#12")
	err := dispatchCommand("forge", []string{"validate-pr", "--allow-unpinned", "--repo=" + dir, "--config=" + filepath.Join(dir, "missing.yaml"), path})
	if err == nil || !strings.Contains(err.Error(), "body line 2: hard-wrapped") || !strings.Contains(err.Error(), "foreign reference") {
		t.Fatalf("want both body refusals, got %v", err)
	}
}

func TestForgeText_OwnerEnvironmentAndInvalidManifest(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	t.Setenv("GITHUB_REPOSITORY", "acme/tool")
	path := writeFixtureFile(t, dir, "message", "fix: own reference\n\nSee ACME/tool#12.\n")
	args := []string{"check-message", "--repo=" + dir, path}
	if err := dispatchCommand("forge", args); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, dir, ".standards.yaml", "forge:\n  own_organizations: [a/b]\n")
	if err := dispatchCommand("forge", args); err == nil {
		t.Fatal("invalid manifest passed")
	}
}
