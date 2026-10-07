// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hermeticGitName and hermeticGitEmail are the author and committer every HermeticGitEnv
// fixture commit carries.
const (
	hermeticGitName  = "praetor-test"
	hermeticGitEmail = "test@example.invalid"
)

// replacedByHermeticGitEnv names the non-Git variables HermeticGitEnv sets itself, so an
// inherited copy is dropped instead of left beside the replacement.
var replacedByHermeticGitEnv = map[string]bool{"HOME": true, "USERPROFILE": true}

// HermeticGitEnv returns an environment for fixture git commands that nothing outside the
// fixture can configure, and a fresh empty home for each call.
//
// Every inherited GIT_* variable is dropped, not only the configuration files. A test run
// from inside a git hook, or under `git -c`, inherits GIT_CONFIG_COUNT with its indexed
// GIT_CONFIG_KEY_n/GIT_CONFIG_VALUE_n pairs or GIT_CONFIG_PARAMETERS, which apply on top of
// any configuration file, and GIT_DIR, GIT_WORK_TREE and GIT_INDEX_FILE, which point the
// fixture's commands at the enclosing repository. What remains is set here: no system or
// global configuration, a fixed identity, and no credential or terminal prompt. os.DevNull
// keeps it correct on Windows, where that path is NUL.
//
// Repository discovery stops above the test's scratch directories: GIT_CEILING_DIRECTORIES
// names their parent (hermeticGitCeiling), so git run from a scratch directory that is not
// itself a repository answers "not a git repository" instead of climbing into a checkout that
// encloses the temporary directory, as one does on a runner whose TMPDIR or GOTMPDIR lies in
// its workspace. A scratch repository and its subdirectories are still found. A working
// directory outside the test's temporary directory is not covered: a test that runs git or a
// gate from the checkout itself still reads the checkout, so such a test runs from a scratch
// directory.
//
// Automatic maintenance is off as well. Git's own default, with no configuration at all,
// makes every commit start a detached `git maintenance run --auto`, which takes
// .git/objects/maintenance.lock after the commit has returned; when the test ends first,
// t.TempDir's cleanup meets that lock file and fails with "directory not empty".
func HermeticGitEnv(t testing.TB) []string {
	t.Helper()
	home := t.TempDir()
	ceiling := hermeticGitCeiling(t, home)
	inherited := os.Environ()
	env := make([]string, 0, len(inherited)+18)
	for _, entry := range inherited {
		key, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "GIT_") || replacedByHermeticGitEnv[upper] {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=maintenance.auto",
		"GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=gc.auto",
		"GIT_CONFIG_VALUE_1=0",
		"GIT_CEILING_DIRECTORIES="+ceiling,
		"HOME="+home,
		"USERPROFILE="+home,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"GIT_AUTHOR_NAME="+hermeticGitName,
		"GIT_AUTHOR_EMAIL="+hermeticGitEmail,
		"GIT_COMMITTER_NAME="+hermeticGitName,
		"GIT_COMMITTER_EMAIL="+hermeticGitEmail,
	)
}

// hermeticGitCeiling returns the directory git must not climb into while it looks for a
// repository: the parent of home. home comes from t.TempDir, and every t.TempDir of one test
// is a numbered child of the same per-test directory, so that parent is the parent of the
// test's scratch repository. It is a single entry, so the list separator (':', ';' on
// Windows) never appears. Git resolves each entry before it compares it with its working
// directory; EvalSymlinks gives that resolved spelling up front, and on Windows it also
// expands an 8.3 short name such as RUNNER~1 that the temporary directory can carry.
func hermeticGitCeiling(t testing.TB, home string) string {
	t.Helper()
	ceiling, err := filepath.EvalSymlinks(filepath.Dir(home))
	if err != nil {
		t.Fatalf("testsupport: resolve the git ceiling above %s: %v", home, err)
	}
	return ceiling
}
