package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/cordanaLLM/praetor/internal/clientsetup"
	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/util"
)

// hostClientEnv describes a home directory for clientsetup, which performs no discovery
// of its own. A foreign home (the --home flag names another tree) takes nothing from
// this process: its APPDATA, XDG_CONFIG_HOME and relocation variables describe the
// running user, so every location then derives from the given home alone.
func hostClientEnv(home string, foreign bool) clientsetup.Env {
	// The helper accepts absolute paths only; a relative --home is anchored here.
	if abs, err := filepath.Abs(home); err == nil && home != "" {
		home = abs
	}
	env := clientsetup.Env{GOOS: runtime.GOOS, Home: home, DirExists: util.DirExists}
	if foreign {
		return env
	}
	env.AppData = os.Getenv("APPDATA")
	env.LocalAppData = os.Getenv("LOCALAPPDATA")
	env.ConfigHome = os.Getenv("XDG_CONFIG_HOME")
	// Getenv stays nil: the Antigravity relocation variables are unverified table data,
	// and the harvester has no native readback to confirm a root chosen through them.
	return env
}

// harvestClientRoots resolves every per-OS client location the bundler reads.
func harvestClientRoots(env clientsetup.Env) (harvester.ClientRoots, error) {
	agyConfig, err := clientsetup.Root(clientsetup.AGY, clientsetup.ScopeGlobal, env)
	if err != nil {
		return harvester.ClientRoots{}, fmt.Errorf("resolve Antigravity configuration root: %w", err)
	}
	brains, err := clientsetup.ExistingBrainRoots(env)
	if err != nil {
		return harvester.ClientRoots{}, fmt.Errorf("resolve Antigravity brain roots: %w", err)
	}
	desktop, err := clientsetup.Locate(clientsetup.LocationClaudeDesktopConfig, env)
	if err != nil {
		return harvester.ClientRoots{}, fmt.Errorf("resolve Claude desktop configuration: %w", err)
	}
	history, err := clientsetup.Locate(clientsetup.LocationPowerShellHistory, env)
	if err != nil && !errors.Is(err, clientsetup.ErrNotApplicable) {
		return harvester.ClientRoots{}, fmt.Errorf("resolve PowerShell history: %w", err)
	}
	return harvester.ClientRoots{AGYConfig: agyConfig, AGYBrains: brains, ClaudeDesktopConfig: desktop, PowerShellHistory: history}, nil
}

// resolveBrainRoots returns the explicit --brain directory, or every existing
// Antigravity brain directory under the user's home, winner first.
func resolveBrainRoots(explicit string) ([]string, error) {
	if explicit != "" {
		return []string{explicit}, nil
	}
	home, err := resolveHomeSubdir("", "--brain")
	if err != nil {
		return nil, err
	}
	return clientsetup.ExistingBrainRoots(hostClientEnv(home, false))
}
