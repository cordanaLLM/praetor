package util

import (
	"context"
	"os"
	"time"
)

// RunGitProbe isolates bounded read-only Git inspections from inherited Git
// configuration, hooks, filesystem monitors and lazy network fetches. Callers
// supply fixed inspection argv and must handle filters/submodules before status.
func RunGitProbe(ctx context.Context, dir string, maxBytes int, args ...string) (CommandBytes, error) {
	probeCtx, err := WithCommandEnvironment(ctx, []string{
		"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1",
	})
	if err != nil {
		return CommandBytes{}, err
	}
	probeCtx, cancel := context.WithTimeout(probeCtx, 5*time.Second)
	defer cancel()
	argv := append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull}, args...)
	return RunCommandBytes(probeCtx, dir, "git", maxBytes, argv...)
}
