// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

func fixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	envCtx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	output, err := util.RunGit(envCtx, dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func initAdoptedFixture(t *testing.T, dir string) {
	t.Helper()
	fixtureGit(t, dir, "init")
	fixtureGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "init")

	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(adopt.ManagedGitIgnoreBlock()), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	trackedFile := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(trackedFile, []byte("tracked\n"), 0o644); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}
	fixtureGit(t, dir, "add", ".gitignore", "tracked.txt")
	fixtureGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "adopt gitignore and tracked file")

	if _, err := state.InitWorkingDirIfAbsentContext(t.Context(), dir); err != nil {
		t.Fatalf("init working dir: %v", err)
	}
}

// Positive: an adopted fixture holding .claude/settings.local.json reports a clean working tree (issue #1389).
func TestInspectStateClientLocalSettings_Positive(t *testing.T) {
	dir := t.TempDir()
	initAdoptedFixture(t, dir)

	claudeLocal := filepath.Join(dir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(claudeLocal), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	if err := os.WriteFile(claudeLocal, []byte(`{"permissions":{"allow":["Bash"]}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write settings.local.json: %v", err)
	}

	snap, err := state.InspectState(t.Context(), dir)
	if err != nil {
		t.Fatalf("InspectState failed: %v", err)
	}
	if !snap.Clean || snap.DirtyCount != 0 {
		t.Fatalf("client-local settings left working tree dirty: clean=%v dirty_count=%d", snap.Clean, snap.DirtyCount)
	}
}

// Negative: a tracked modified file still counts dirty when client-local settings are ignored.
func TestInspectStateClientLocalSettings_Negative(t *testing.T) {
	dir := t.TempDir()
	initAdoptedFixture(t, dir)

	claudeLocal := filepath.Join(dir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(claudeLocal), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	if err := os.WriteFile(claudeLocal, []byte(`{"permissions":{"allow":["Bash"]}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write settings.local.json: %v", err)
	}

	trackedFile := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(trackedFile, []byte("modified\n"), 0o644); err != nil {
		t.Fatalf("modify tracked file: %v", err)
	}

	snap, err := state.InspectState(t.Context(), dir)
	if err != nil {
		t.Fatalf("InspectState failed: %v", err)
	}
	if snap.Clean || snap.DirtyCount != 1 {
		t.Fatalf("tracked modified file must count dirty: clean=%v dirty_count=%d", snap.Clean, snap.DirtyCount)
	}
}

// Boundary: a repository whose block is v3 is upgraded to v4 by adopt and a v3 block still verifies.
func TestInspectStateClientLocalSettings_Boundary(t *testing.T) {
	dir := t.TempDir()
	fixtureGit(t, dir, "init")
	fixtureGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "init")

	v3Block := "bin/\n\n# BEGIN praetor private artifacts (praetorctl adopt)\n.workingdir/\n.workingdir2/\n/.standards/worktrees/\n/.agents/mcp_config.json\n/.standards/cache/\n# END praetor private artifacts\n"
	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte(v3Block), 0o644); err != nil {
		t.Fatalf("write v3 .gitignore: %v", err)
	}
	fixtureGit(t, dir, "add", ".gitignore")
	fixtureGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "v3 ignore")

	// Boundary condition 1: a v3 block still verifies.
	if !adopt.HasManagedGitIgnoreTail(dir, v3Block) {
		t.Fatal("v3 block must verify via HasManagedGitIgnoreTail")
	}
	match, ok := adopt.LookupManagedGitIgnoreTail(dir, v3Block)
	if !ok || !match.Matched || match.Version != "v3" || match.Current {
		t.Fatalf("v3 block lookup: ok=%v match=%+v, want Matched=true Version=v3 Current=false", ok, match)
	}

	// Boundary condition 2: adopt upgrades the repository block from v3 to v4.
	if _, err := adopt.Adopt(t.Context(), adopt.AdoptOptions{
		Path:              dir,
		SkipGitValidation: true,
	}); err != nil && !strings.Contains(err.Error(), "lock source") {
		t.Fatalf("adopt upgrade: %v", err)
	}

	upgraded, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read upgraded .gitignore: %v", err)
	}
	upgradedText := string(upgraded)
	if !strings.HasSuffix(upgradedText, adopt.ManagedGitIgnoreBlock()) {
		t.Fatalf("upgraded .gitignore does not end with canonical v4 block:\n%s", upgradedText)
	}
	matchAfter, okAfter := adopt.LookupManagedGitIgnoreTail(dir, upgradedText)
	if !okAfter || !matchAfter.Matched || matchAfter.Version != "v4" || !matchAfter.Current {
		t.Fatalf("upgraded block lookup: ok=%v match=%+v, want Matched=true Version=v4 Current=true", okAfter, matchAfter)
	}

	// Commit adoption changes so working tree is clean before adding client-local settings.
	fixtureGit(t, dir, "add", "-A")
	fixtureGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "commit adoption")

	// Boundary condition 3: client-local settings in the upgraded repo report clean working tree.
	if _, err := state.InitWorkingDirIfAbsentContext(t.Context(), dir); err != nil {
		t.Fatalf("init working dir: %v", err)
	}
	claudeLocal := filepath.Join(dir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(claudeLocal), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	if err := os.WriteFile(claudeLocal, []byte(`{}`+"\n"), 0o644); err != nil {
		t.Fatalf("write settings.local.json: %v", err)
	}
	snap, err := state.InspectState(t.Context(), dir)
	if err != nil {
		t.Fatalf("InspectState failed: %v", err)
	}
	if !snap.Clean || snap.DirtyCount != 0 {
		t.Fatalf("upgraded repository with client-local settings must be clean: clean=%v dirty_count=%d", snap.Clean, snap.DirtyCount)
	}
}
