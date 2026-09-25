// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"os"
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
func HermeticGitEnv(t testing.TB) []string {
	t.Helper()
	home := t.TempDir()
	inherited := os.Environ()
	env := make([]string, 0, len(inherited)+12)
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
