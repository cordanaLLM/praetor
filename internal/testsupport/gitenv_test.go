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
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
}

// runGit runs git in dir with env and returns its trimmed combined output.
func runGit(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	return runTool(t, "git", dir, env, args...)
}

// runTool runs the named program in dir with env, under the bound every fixture command
// gets, and returns its trimmed combined output.
func runTool(t *testing.T, name, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), fixtureGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
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

// TestHermeticGitEnv_Positive_AutomaticMaintenanceIsOff checks what git itself resolves: under
// the hermetic environment a fixture repository has automatic maintenance and automatic gc
// off, and without the two pairs both are unset, so git's detaching default applies.
func TestHermeticGitEnv_Positive_AutomaticMaintenanceIsOff(t *testing.T) {
	requireGit(t)
	fixture := t.TempDir()
	env := HermeticGitEnv(t)
	if out, err := runGit(t, fixture, env, "init", "-q"); err != nil {
		t.Fatalf("init fixture: %v: %s", err, out)
	}
	for key, want := range map[string]string{"maintenance.auto": "false", "gc.auto": "0"} {
		if got, err := runGit(t, fixture, env, "config", "--get", key); err != nil || got != want {
			t.Errorf("git config %s = %q (err %v), want %q", key, got, err, want)
		}
	}
	var unpaired []string
	for _, entry := range env {
		if !strings.HasPrefix(entry, "GIT_CONFIG_COUNT=") && !strings.HasPrefix(entry, "GIT_CONFIG_KEY_") &&
			!strings.HasPrefix(entry, "GIT_CONFIG_VALUE_") {
			unpaired = append(unpaired, entry)
		}
	}
	if got, err := runGit(t, fixture, unpaired, "config", "--get", "maintenance.auto"); err == nil {
		t.Fatalf("without the pairs maintenance.auto resolved to %q; the positive check proves nothing", got)
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
		if key == "" {
			// Windows keeps each drive's working directory as a hidden entry ("=C:=C:\\dir"),
			// and os.Environ returns them; they are not variables a caller sets.
			continue
		}
		if _, dup := set[key]; dup {
			t.Errorf("%s is set twice", key)
		}
		set[key] = value
	}
	for _, key := range []string{"GIT_CONFIG_PARAMETERS", "GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE"} {
		if value, ok := set[key]; ok {
			t.Errorf("inherited %s=%q survived", key, value)
		}
	}
	// The inherited signing pairs sit at the same indexes; only the hermetic pairs may remain.
	required := map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_SYSTEM": os.DevNull, "GIT_CONFIG_GLOBAL": os.DevNull,
		"GIT_CONFIG_COUNT": "2", "GIT_CONFIG_KEY_0": "maintenance.auto", "GIT_CONFIG_VALUE_0": "false",
		"GIT_CONFIG_KEY_1": "gc.auto", "GIT_CONFIG_VALUE_1": "0",
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

// withoutCeiling returns env without its GIT_CEILING_DIRECTORIES entry: the environment
// HermeticGitEnv returned before it bounded repository discovery.
func withoutCeiling(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		if !strings.HasPrefix(entry, "GIT_CEILING_DIRECTORIES=") {
			kept = append(kept, entry)
		}
	}
	return kept
}

// sameDirectory reports whether git's answer names the directory want, compared by file
// identity, so a resolved symlink, a short name or git's forward slashes on Windows do not
// make one directory look like two.
func sameDirectory(got, want string) bool {
	gotInfo, gotErr := os.Stat(got)
	wantInfo, wantErr := os.Stat(want)
	return gotErr == nil && wantErr == nil && os.SameFile(gotInfo, wantInfo)
}

// commitFixture makes dir a repository with one commit, a stand-in for a real checkout.
func commitFixture(t *testing.T, dir string) {
	t.Helper()
	env := HermeticGitEnv(t)
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "checkout"}} {
		if out, err := runGit(t, dir, env, args...); err != nil {
			t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
		}
	}
}

// TestHermeticGitEnv_Positive_ScratchRepositoryFoundFromItsSubdirectory checks the ceiling
// stops discovery above the scratch directories, not inside them: git run from a
// subdirectory of a scratch repository still finds that repository.
func TestHermeticGitEnv_Positive_ScratchRepositoryFoundFromItsSubdirectory(t *testing.T) {
	requireGit(t)
	scratch := t.TempDir()
	commitFixture(t, scratch)
	nested := filepath.Join(scratch, "a", "b")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("create a subdirectory of the scratch repository: %v", err)
	}
	top, err := runGit(t, nested, HermeticGitEnv(t), "rev-parse", "--show-toplevel")
	if err != nil || !sameDirectory(top, scratch) {
		t.Fatalf("git from %s found %q (err %v), want the scratch repository %s", nested, top, err, scratch)
	}
}

// TestHermeticGitEnv_Negative_ScratchUnderACheckoutDoesNotFindIt is the leak the ceiling
// closes. t.TempDir reads GOTMPDIR on every platform, so pointing it into a committed
// repository puts a subtest's scratch directories inside that checkout, as on a runner whose
// temporary directory lies in its workspace. Without the ceiling, git run from a scratch
// directory climbs into the checkout and reads it; with HermeticGitEnv it answers that the
// scratch directory is not a repository.
func TestHermeticGitEnv_Negative_ScratchUnderACheckoutDoesNotFindIt(t *testing.T) {
	requireGit(t)
	checkout := t.TempDir()
	commitFixture(t, checkout)
	temporary := filepath.Join(checkout, "tmp")
	if err := os.Mkdir(temporary, 0o750); err != nil {
		t.Fatalf("create the temporary directory inside the checkout: %v", err)
	}
	t.Setenv("GOTMPDIR", temporary)
	t.Run("scratch", func(t *testing.T) {
		scratch := t.TempDir()
		if !strings.HasPrefix(scratch, temporary+string(filepath.Separator)) {
			t.Fatalf("scratch directory %s is not inside %s; the fixture proves nothing", scratch, temporary)
		}
		env := HermeticGitEnv(t)
		top, err := runGit(t, scratch, withoutCeiling(env), "rev-parse", "--show-toplevel")
		if err != nil || !sameDirectory(top, checkout) {
			t.Fatalf("without the ceiling git found %q (err %v), want the enclosing checkout %s", top, err, checkout)
		}
		for _, args := range [][]string{{"rev-parse", "--show-toplevel"}, {"log", "-1", "--format=%H"}} {
			if out, err := runGit(t, scratch, env, args...); err == nil {
				t.Errorf("git %v from a scratch directory read the enclosing checkout: %s", args, out)
			}
		}
	})
}

// TestHermeticGitEnv_Boundary_CeilingIsTheScratchParent pins the ceiling's value: one entry,
// replacing an inherited one, naming the resolved parent of the calling test's own scratch
// directories, so a subtest gets the parent of its own and not of its parent test's.
func TestHermeticGitEnv_Boundary_CeilingIsTheScratchParent(t *testing.T) {
	requireGit(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", os.DevNull)
	ceilingOf := func(t *testing.T) string {
		t.Helper()
		var values []string
		for _, entry := range HermeticGitEnv(t) {
			if value, ok := strings.CutPrefix(entry, "GIT_CEILING_DIRECTORIES="); ok {
				values = append(values, value)
			}
		}
		want, err := filepath.EvalSymlinks(filepath.Dir(t.TempDir()))
		if err != nil || len(values) != 1 || values[0] != want {
			t.Fatalf("GIT_CEILING_DIRECTORIES entries = %q, want exactly [%q] (err %v)", values, want, err)
		}
		return values[0]
	}
	parent := ceilingOf(t)
	t.Run("subtest", func(t *testing.T) {
		if child := ceilingOf(t); child == parent {
			t.Fatalf("a subtest shares its parent's ceiling %q", parent)
		}
	})
}
