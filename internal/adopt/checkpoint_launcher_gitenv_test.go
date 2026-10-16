// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

func initGitTestRepo(t *testing.T, dir string) string {
	t.Helper()
	gitRunCmd(t, dir, "init", "-b", "main")
	gitRunCmd(t, dir, "config", "user.name", "test-user")
	gitRunCmd(t, dir, "config", "user.email", "test@example.invalid")
	file := filepath.Join(dir, "seed.txt")
	if err := os.WriteFile(file, []byte("seed content\n"), 0o600); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	gitRunCmd(t, dir, "add", "seed.txt")
	gitRunCmd(t, dir, "commit", "-m", "initial seed")
	return gitOutCmd(t, dir, "rev-parse", "HEAD")
}

func gitRunCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testsupport.HermeticGitEnv(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, string(out))
	}
}

func gitOutCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testsupport.HermeticGitEnv(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func writeGitEnvTestScript(t *testing.T, dir string) string {
	t.Helper()
	scriptPath := filepath.Join(dir, "test_gitenv_hook.py")
	script := "import os, subprocess, sys\n" +
		"temp_dir = sys.argv[1]\n" +
		"with open(os.path.join(temp_dir, 'nested.txt'), 'w') as f:\n" +
		"    f.write('from nested test\\n')\n" +
		"subprocess.run(['git', '-C', temp_dir, 'add', '.'], check=True)\n" +
		"subprocess.run(['git', '-C', temp_dir, 'commit', '-m', 'nested commit'], check=True)\n" +
		"subprocess.run(['git', '-C', temp_dir, 'config', 'core.bare', 'true'], check=True)\n" +
		"print('INDEX:' + str(os.environ.get('GIT_INDEX_FILE')))\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return scriptPath
}

func testLauncherDropsGitEnv(t *testing.T, shell, launcherPath string) {
	t.Helper()
	origRepo := t.TempDir()
	origTip := initGitTestRepo(t, origRepo)
	tempRepo := t.TempDir()
	initGitTestRepo(t, tempRepo)
	scriptPath := writeGitEnvTestScript(t, t.TempDir())

	cmd := exec.CommandContext(t.Context(), shell, launcherPath, scriptPath, tempRepo)
	cmd.Dir = tempRepo
	cmd.Env = append(testsupport.HermeticGitEnv(t),
		"GIT_DIR="+filepath.Join(origRepo, ".git"),
		"GIT_WORK_TREE="+origRepo,
		"GIT_INDEX_FILE="+filepath.Join(origRepo, ".git", "index"),
		"GIT_COMMON_DIR="+filepath.Join(origRepo, ".git"),
		"GIT_OBJECT_DIRECTORY="+filepath.Join(origRepo, ".git", "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES="+filepath.Join(origRepo, ".git", "objects"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launcher execution failed: %v\n%s", err, string(out))
	}
	assertOrigRepoUnchanged(t, origRepo, origTip)
	if !strings.Contains(string(out), "INDEX:None") {
		t.Fatalf("expected GIT_INDEX_FILE dropped, got output:\n%s", string(out))
	}
	tempTip := gitOutCmd(t, tempRepo, "rev-parse", "HEAD")
	if tempTip == "" || tempTip == origTip {
		t.Fatalf("temp repo should receive commit, got %s", tempTip)
	}
}

func testLauncherKeepIndexPreservesIndex(t *testing.T, shell, launcherPath string) {
	t.Helper()
	origRepo := t.TempDir()
	origTip := initGitTestRepo(t, origRepo)
	tempRepo := t.TempDir()
	initGitTestRepo(t, tempRepo)
	scriptPath := writeGitEnvTestScript(t, t.TempDir())

	keepIndexLock := filepath.Join(origRepo, ".git", "custom.index.lock")
	cmd := exec.CommandContext(t.Context(), shell, launcherPath, "--keep-index", scriptPath, tempRepo)
	cmd.Dir = tempRepo
	cmd.Env = append(testsupport.HermeticGitEnv(t),
		"GIT_DIR="+filepath.Join(origRepo, ".git"),
		"GIT_WORK_TREE="+origRepo,
		"GIT_INDEX_FILE="+keepIndexLock,
		"GIT_COMMON_DIR="+filepath.Join(origRepo, ".git"),
		"GIT_OBJECT_DIRECTORY="+filepath.Join(origRepo, ".git", "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES="+filepath.Join(origRepo, ".git", "objects"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launcher execution with --keep-index failed: %v\n%s", err, string(out))
	}
	assertOrigRepoUnchanged(t, origRepo, origTip)
	if !strings.Contains(string(out), "INDEX:"+keepIndexLock) {
		t.Fatalf("expected GIT_INDEX_FILE kept, got output:\n%s", string(out))
	}
}

func assertOrigRepoUnchanged(t *testing.T, origRepo, origTip string) {
	t.Helper()
	origTipAfter := gitOutCmd(t, origRepo, "rev-parse", "HEAD")
	if origTipAfter != origTip {
		t.Fatalf("original repository branch tip changed from %s to %s", origTip, origTipAfter)
	}
	origBare := gitOutCmd(t, origRepo, "config", "--get", "--default", "false", "core.bare")
	if origBare != "false" {
		t.Fatalf("original repository core.bare changed to %s", origBare)
	}
}

func TestCheckpointLauncher_GitEnvScrub_PlantedCase(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not on PATH")
	}
	launcherPath, err := filepath.Abs(filepath.Join("..", "..", filepath.FromSlash(checkpointLauncher)))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("default drops repository selection and protects original repository", func(t *testing.T) {
		testLauncherDropsGitEnv(t, shell, launcherPath)
	})

	t.Run("pre-commit switch keeps git index file while scrubbing git dir", func(t *testing.T) {
		testLauncherKeepIndexPreservesIndex(t, shell, launcherPath)
	})
}
