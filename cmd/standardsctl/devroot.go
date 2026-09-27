package main

import (
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/needs"
)

// Workstation dev-root environment names. PRAETOR_DEV_ROOT is the canonical name used by
// AGENTS.md and scripts/adopt_repos.sh; PRAETOR_DEV_DIR is the earlier name, kept
// as a fallback so an operator environment that still sets it keeps selecting the same tree.
// frameworkDirEnv names the go framework checkout (needs.SelectFrameworkSource).
const (
	devRootEnv       = "PRAETOR_DEV_ROOT"
	legacyDevRootEnv = "PRAETOR_DEV_DIR"
	frameworkDirEnv  = needs.FrameworkDirEnv
)

// devRootUsageDefault is the default clause every dev-root flag prints in its usage line.
const devRootUsageDefault = "(default: $" + devRootEnv + ", else $" + legacyDevRootEnv + ", else <home>/dev)"

// configuredDevRoot returns the dev root the environment names: $PRAETOR_DEV_ROOT, else
// $PRAETOR_DEV_DIR, else "" when neither is set.
func configuredDevRoot() string {
	for _, name := range []string{devRootEnv, legacyDevRootEnv} {
		if dir := os.Getenv(name); dir != "" {
			return dir
		}
	}
	return ""
}

// resolveDevRootDir is the one resolver for the workstation dev root: explicit when it is
// set, else $PRAETOR_DEV_ROOT, else $PRAETOR_DEV_DIR, else <home>/dev. Without a usable
// home directory it returns an error naming flagName instead of a path relative to the
// working directory.
func resolveDevRootDir(explicit, flagName string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if dir := configuredDevRoot(); dir != "" {
		return dir, nil
	}
	root, err := resolveHomeSubdir("", flagName, "dev")
	if err != nil {
		return "", fmt.Errorf("%s and %s are unset: %w", devRootEnv, legacyDevRootEnv, err)
	}
	return root, nil
}

// frameworkUsageDefault is the default clause of every needs --framework flag.
const frameworkUsageDefault = "(default: $" + frameworkDirEnv + ", else framework.targets.go.checkout, " +
	"else framework.targets.go.contract, else framework.targets.go.module; \"\" selects the declaration)"
