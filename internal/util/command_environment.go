package util

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
)

// gitRepositoryVariables bind a git process to a repository other than the one its working
// directory names. The set is `git rev-parse --local-env-vars` (git 2.55) without
// GIT_CONFIG_PARAMETERS and GIT_CONFIG_COUNT: exactly what git's own sanitize_repo_env
// (run-command.c) clears before it runs a command in another repository. Configuration
// passed with `git -c` is the caller's intent and travels on; the repository location does
// not.
var gitRepositoryVariables = map[string]struct{}{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
	"GIT_COMMON_DIR":                   {},
	"GIT_CONFIG":                       {},
	"GIT_DIR":                          {},
	"GIT_GRAFT_FILE":                   {},
	"GIT_IMPLICIT_WORK_TREE":           {},
	"GIT_INDEX_FILE":                   {},
	"GIT_NO_REPLACE_OBJECTS":           {},
	"GIT_OBJECT_DIRECTORY":             {},
	"GIT_PREFIX":                       {},
	"GIT_REPLACE_REF_BASE":             {},
	"GIT_SHALLOW_FILE":                 {},
	"GIT_WORK_TREE":                    {},
}

// isGitRepositoryVariable reports whether name is one of gitRepositoryVariables.
func isGitRepositoryVariable(name string) bool {
	_, ok := gitRepositoryVariables[name]
	return ok
}

// FilterEnvironment returns a copy of environ without the entries whose name drop reports
// true. Names reach drop folded to upper case on Windows, where environment names are
// case-insensitive, and unchanged elsewhere. An entry with no name, such as a Windows
// per-drive "=C:=C:\" record, is offered to drop as "". A nil drop keeps every entry.
func FilterEnvironment(environ []string, drop func(name string) bool) []string {
	filtered := make([]string, 0, len(environ))
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if runtime.GOOS == "windows" {
			name = strings.ToUpper(name)
		}
		if drop == nil || !drop(name) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// commandEnvironment returns the environment for cmd, whose Dir must already be set.
//
// WithCommandEnvironment selects exactly the caller's list. Otherwise the child inherits the
// ambient environment -- as cmd.Environ reports it, with PWD moved to cmd.Dir -- without
// gitRepositoryVariables. Praetor runs inside git hooks, where GIT_DIR and GIT_INDEX_FILE name
// the hook's repository; a child that inherited them, whether git itself or `go test` whose
// fixtures run git, would read and write that repository instead of the one in its working
// directory, down to setting core.bare=true in it (BUG-886).
func commandEnvironment(ctx context.Context, cmd *exec.Cmd) []string {
	if environment, ok := ctx.Value(commandEnvironmentKey{}).([]string); ok {
		return append([]string{}, environment...)
	}
	return FilterEnvironment(cmd.Environ(), isGitRepositoryVariable)
}
