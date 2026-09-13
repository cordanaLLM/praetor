package repairrun

import (
	"context"
	"errors"

	"github.com/cordanaLLM/praetor/internal/util"
)

const maxLogBytes = 1 << 20

func command(ctx context.Context, dir string, env []string, limit int, executable string, args ...string) ([]byte, error) {
	ctx, err := util.WithCommandEnvironment(ctx, env)
	if err != nil {
		return nil, err
	}
	output, err := util.RunCommandBytes(ctx, dir, executable, limit, args...)
	data := append(output.Stdout, output.Stderr...)
	if len(data) > limit {
		return data[:limit], errors.Join(err, errors.New("combined subprocess output bound exceeded"))
	}
	return data, err
}

func gitEnvironment() []string {
	return []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "HOME=/nonexistent", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=", "GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_VALUE_0=false", "GIT_CONFIG_KEY_1=core.attributesFile", "GIT_CONFIG_VALUE_1=/dev/null"}
}
