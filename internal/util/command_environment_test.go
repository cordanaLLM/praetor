package util

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCommandEnvironmentIsCopiedAndDoesNotInherit(t *testing.T) {
	if _, err := os.Stat("/usr/bin/env"); err != nil {
		t.Skipf("/usr/bin/env is not present on this host (%v); the behaviour under test "+
			"is platform-independent and covered where the tool exists", err)
	}
	t.Setenv("PRAETOR_TEST_AMBIENT", "private-sentinel")
	env := []string{"PRAETOR_TEST_EXPLICIT=original"}
	ctx, err := WithCommandEnvironment(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	env[0] = "PRAETOR_TEST_EXPLICIT=mutated"
	output, err := RunCommand(ctx, "", "/usr/bin/env")
	if err != nil {
		t.Fatal(err)
	}
	if output != "PRAETOR_TEST_EXPLICIT=original" {
		t.Fatalf("environment leaked or mutated: %q", output)
	}
	ambient, err := RunCommand(context.Background(), "", "/usr/bin/env")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ambient, "PRAETOR_TEST_AMBIENT=private-sentinel") {
		t.Fatal("context override changed ambient process environment")
	}
}

func TestFilterEnvironment_3D(t *testing.T) {
	// Positive: every repository variable git itself clears is dropped.
	for name := range gitRepositoryVariables {
		if kept := FilterEnvironment([]string{name + "=x"}, isGitRepositoryVariable); len(kept) != 0 {
			t.Errorf("%s survived the scrub: %q", name, kept)
		}
	}
	// Negative: configuration passed with `git -c`, config selectors, look-alike names and
	// unrelated or nameless entries travel on.
	kept := []string{
		"PATH=/bin", "GIT_CONFIG_PARAMETERS='core.x'='y'", "GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.x", "GIT_CONFIG_VALUE_0=y", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_SSH_COMMAND=ssh", "GIT_DIRECTORY=x", `=C:=C:\`, "NOVALUE",
	}
	environ := append([]string{"GIT_DIR=/hook/.git"}, kept...)
	environ = append(environ, "GIT_INDEX_FILE=/hook/.git/index")
	got := FilterEnvironment(environ, isGitRepositoryVariable)
	if strings.Join(got, "\n") != strings.Join(kept, "\n") {
		t.Errorf("scrub = %q, want %q", got, kept)
	}
	// Boundary: nil input, nil predicate, a returned copy, and the platform's name case.
	if empty := FilterEnvironment(nil, isGitRepositoryVariable); empty == nil || len(empty) != 0 {
		t.Errorf("nil input = %#v, want an empty non-nil slice", empty)
	}
	all := FilterEnvironment(environ, nil)
	if len(all) != len(environ) {
		t.Errorf("nil predicate dropped entries: %q", all)
	}
	all[0] = "MUTATED=1"
	if environ[0] != "GIT_DIR=/hook/.git" {
		t.Error("result aliases the input")
	}
	folded := FilterEnvironment([]string{"git_dir=/hook/.git"}, isGitRepositoryVariable)
	if wantKept := runtime.GOOS != "windows"; (len(folded) == 1) != wantKept {
		t.Errorf("git_dir on %s: kept=%v, want %v", runtime.GOOS, len(folded) == 1, wantKept)
	}
}

// environmentNames indexes the names of an environment printed one entry per line.
func environmentNames(printed string) map[string]string {
	names := make(map[string]string)
	for _, line := range strings.Split(printed, "\n") {
		name, value, _ := strings.Cut(line, "=")
		names[name] = value
	}
	return names
}

// Praetor runs inside git hooks, which export GIT_DIR and GIT_INDEX_FILE for the hook's
// repository. They must not reach a child unless the caller asks for them (BUG-886).
func TestRunCommandScrubsAmbientGitRepositoryVariables(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRAETOR_COMMAND_BYTES_TEST", "environ")
	t.Setenv("GOCOVERDIR", t.TempDir())
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "index"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.hookspath'='none'")
	dir := t.TempDir()
	out, err := RunCommand(context.Background(), dir, binary, "-test.run=^TestCommandBytesHelper$")
	if err != nil {
		t.Fatal(err)
	}
	bytesResult, err := RunCommandBytes(context.Background(), dir, binary, 1<<20, "-test.run=^TestCommandBytesHelper$")
	if err != nil {
		t.Fatal(err)
	}
	for runner, printed := range map[string]string{"RunCommand": out, "RunCommandBytes": string(bytesResult.Stdout)} {
		names := environmentNames(printed)
		for _, name := range []string{"GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE"} {
			if _, leaked := names[name]; leaked {
				t.Errorf("%s handed the child an ambient %s", runner, name)
			}
		}
		if _, kept := names["GIT_CONFIG_PARAMETERS"]; !kept {
			t.Errorf("%s dropped git -c configuration", runner)
		}
		// Boundary: the scrubbed environment still moves PWD to the child's directory,
		// as the inherited one did.
		if pwd, want := names["PWD"], dir; runtime.GOOS != "windows" && pwd != want {
			t.Errorf("%s left PWD at %q, want %q", runner, pwd, want)
		}
	}
}

// WithCommandEnvironment is the opt-in: an explicit environment is used exactly, GIT_DIR
// included.
func TestRunCommandExplicitEnvironmentKeepsGitDir(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := WithCommandEnvironment(context.Background(), []string{
		"PRAETOR_COMMAND_BYTES_TEST=environ", "GOCOVERDIR=" + t.TempDir(), "GIT_DIR=/explicit/.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := RunCommand(ctx, "", binary, "-test.run=^TestCommandBytesHelper$")
	if err != nil {
		t.Fatal(err)
	}
	if got := environmentNames(out)["GIT_DIR"]; got != "/explicit/.git" {
		t.Fatalf("explicit GIT_DIR = %q", got)
	}
}

// The regression the scrub exists for: from inside a hook, initialising and configuring
// another repository must not write into the hook's repository. The test fails rather than
// skips when the leak is present.
func TestRunGitIgnoresAmbientGitDirWhenConfiguringAnotherRepository(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	hook, target := t.TempDir(), t.TempDir()
	if _, err := RunGit(ctx, hook, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(hook, ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(hook, ".git", "index"))
	if _, err := RunGit(ctx, target, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	if _, err := RunGit(ctx, target, "config", "core.bare", "true"); err != nil {
		t.Fatal(err)
	}
	hookConfig, err := os.ReadFile(filepath.Join(hook, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(hookConfig), "bare = true") {
		t.Fatalf("ambient GIT_DIR redirected core.bare into the hook repository:\n%s", hookConfig)
	}
	targetConfig, err := os.ReadFile(filepath.Join(target, ".git", "config"))
	if err != nil || !strings.Contains(string(targetConfig), "bare = true") {
		t.Fatalf("the target repository did not receive core.bare: %q, %v", targetConfig, err)
	}
}

func TestCommandEnvironmentEmptyAndBounds(t *testing.T) {
	ctx, err := WithCommandEnvironment(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Only this block needs /usr/bin/env to print the child's environment. The context and
	// bound checks below do not, so they run on every platform rather than being skipped
	// with it.
	if _, statErr := os.Stat("/usr/bin/env"); statErr != nil {
		t.Logf("/usr/bin/env is not present on this host (%v); empty-environment readback skipped", statErr)
	} else if output, err := RunCommand(ctx, "", "/usr/bin/env"); err != nil || output != "" {
		t.Fatalf("explicit empty environment inherited values: %q, %v", output, err)
	}
	var nilContext context.Context
	if _, err := WithCommandEnvironment(nilContext, nil); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := WithCommandEnvironment(context.Background(), make([]string, 256)); err != nil {
		t.Fatal(err)
	}
	if _, err := WithCommandEnvironment(context.Background(), make([]string, 257)); err == nil {
		t.Fatal("oversized environment accepted")
	}
}
