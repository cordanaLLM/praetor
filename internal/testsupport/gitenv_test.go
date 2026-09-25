// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureGitTimeout bounds one fixture git command (HISS-02).
const fixtureGitTimeout = 30 * time.Second

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
}

// runGit runs git in dir with env and returns its trimmed combined output.
func runGit(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), fixtureGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// hostileGitEnvironment makes the process environment carry what a developer or an
// enclosing git hook can: command-line configuration that demands signing through a
// program that does not exist, and a GIT_DIR/GIT_INDEX_FILE naming another repository.
// It returns that other repository.
func hostileGitEnvironment(t *testing.T) string {
	t.Helper()
	outer := t.TempDir()
	if out, err := runGit(t, outer, HermeticGitEnv(t), "init", "-q"); err != nil {
		t.Fatalf("init outer repository: %v: %s", err, out)
	}
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "commit.gpgsign")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	t.Setenv("GIT_CONFIG_KEY_1", "gpg.program")
	t.Setenv("GIT_CONFIG_VALUE_1", filepath.Join(outer, "no-such-signing-program"))
	t.Setenv("GIT_CONFIG_PARAMETERS", "'user.name'='Inherited'")
	t.Setenv("GIT_DIR", filepath.Join(outer, ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(outer, ".git", "index"))
	t.Setenv("GIT_WORK_TREE", outer)
	return outer
}

// TestHermeticGitEnv_Positive_CommitsUnderAHostileEnvironment is the property every caller
// relies on: a fixture repository initialises and commits in its own directory with the
// fixed identity, whatever the process environment carries.
func TestHermeticGitEnv_Positive_CommitsUnderAHostileEnvironment(t *testing.T) {
	requireGit(t)
	outer := hostileGitEnvironment(t)
	fixture := t.TempDir()
	env := HermeticGitEnv(t)
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "fixture"}} {
		if out, err := runGit(t, fixture, env, args...); err != nil {
			t.Fatalf("git %v under the hermetic environment: %v: %s", args, err, out)
		}
	}
	author, err := runGit(t, fixture, env, "log", "-1", "--format=%an <%ae>")
	if err != nil || author != hermeticGitName+" <"+hermeticGitEmail+">" {
		t.Fatalf("fixture commit author = %q (err %v)", author, err)
	}
	if _, err := os.Stat(filepath.Join(fixture, ".git")); err != nil {
		t.Fatalf("fixture repository was not created in its own directory: %v", err)
	}
	if out, err := runGit(t, outer, env, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		t.Fatalf("the fixture commit landed in the enclosing repository: %s", out)
	}
}

// TestHermeticGitEnv_Negative_InheritedEnvironmentIsHostile proves the positive case is
// evidence: the same commit with the inherited environment fails, and the inherited GIT_DIR
// redirects git to the enclosing repository.
func TestHermeticGitEnv_Negative_InheritedEnvironmentIsHostile(t *testing.T) {
	requireGit(t)
	outer := hostileGitEnvironment(t)
	fixture := t.TempDir()
	inherited := os.Environ()
	gitDir, err := runGit(t, fixture, inherited, "rev-parse", "--absolute-git-dir")
	want, wantErr := filepath.EvalSymlinks(filepath.Join(outer, ".git"))
	got, gotErr := filepath.EvalSymlinks(gitDir)
	if err != nil || wantErr != nil || gotErr != nil || got != want {
		t.Fatalf("inherited GIT_DIR did not redirect git: %q (err %v, %v, %v)", gitDir, err, wantErr, gotErr)
	}
	if out, err := runGit(t, fixture, inherited, "commit", "-q", "--allow-empty", "-m", "inherited"); err == nil {
		t.Fatalf("inherited signing configuration did not break the commit: %s", out)
	}
}

// TestHermeticGitEnv_Boundary_EnvironmentContents pins the environment itself: no inherited
// GIT_* variable survives, every isolation setting appears exactly once, ordinary variables
// are kept, and each call gets its own home away from the developer's.
func TestHermeticGitEnv_Boundary_EnvironmentContents(t *testing.T) {
	requireGit(t)
	hostileGitEnvironment(t)
	t.Setenv("PRAETOR_TESTSUPPORT_KEEP", "kept")
	env := HermeticGitEnv(t)
	set := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if _, dup := set[key]; dup {
			t.Errorf("%s is set twice", key)
		}
		set[key] = value
	}
	for _, key := range []string{"GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0",
		"GIT_CONFIG_KEY_1", "GIT_CONFIG_VALUE_1", "GIT_CONFIG_PARAMETERS",
		"GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE"} {
		if value, ok := set[key]; ok {
			t.Errorf("inherited %s=%q survived", key, value)
		}
	}
	required := map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_SYSTEM": os.DevNull, "GIT_CONFIG_GLOBAL": os.DevNull,
		"GIT_TERMINAL_PROMPT": "0", "GIT_ASKPASS": "", "PRAETOR_TESTSUPPORT_KEEP": "kept",
		"GIT_AUTHOR_NAME": hermeticGitName, "GIT_COMMITTER_EMAIL": hermeticGitEmail,
	}
	for key, want := range required {
		if got, ok := set[key]; !ok || got != want {
			t.Errorf("%s = %q (present %v), want %q", key, got, ok, want)
		}
	}
	other := HermeticGitEnv(t)
	if set["HOME"] == "" || set["HOME"] == os.Getenv("HOME") || set["USERPROFILE"] != set["HOME"] {
		t.Errorf("HOME must be a fresh directory shared with USERPROFILE, got %q / %q", set["HOME"], set["USERPROFILE"])
	}
	for _, entry := range other {
		if entry == "HOME="+set["HOME"] {
			t.Errorf("two calls share one home %q", set["HOME"])
		}
	}
}
