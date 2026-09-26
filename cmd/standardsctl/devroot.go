package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// Workstation dev-root environment names. PRAETOR_DEV_ROOT is the canonical name used by
// AGENTS.md and scripts/adopt_priority_repos.sh; PRAETOR_DEV_DIR is the earlier name, kept
// as a fallback so an operator environment that still sets it keeps selecting the same tree.
const (
	devRootEnv       = "PRAETOR_DEV_ROOT"
	legacyDevRootEnv = "PRAETOR_DEV_DIR"
	frameworkDirEnv  = "PRAETOR_FRAMEWORK_DIR"
)

// devRootUsageDefault is the default clause every dev-root flag prints in its usage line.
const devRootUsageDefault = "(default: $" + devRootEnv + ", else $" + legacyDevRootEnv + ", else <home>/dev)"

// resolveDevRootDir is the one resolver for the workstation dev root: explicit when it is
// set, else $PRAETOR_DEV_ROOT, else $PRAETOR_DEV_DIR, else <home>/dev. Without a usable
// home directory it returns an error naming flagName instead of a path relative to the
// working directory.
func resolveDevRootDir(explicit, flagName string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	for _, name := range []string{devRootEnv, legacyDevRootEnv} {
		if dir := os.Getenv(name); dir != "" {
			return dir, nil
		}
	}
	root, err := resolveHomeSubdir("", flagName, "dev")
	if err != nil {
		return "", fmt.Errorf("%s and %s are unset: %w", devRootEnv, legacyDevRootEnv, err)
	}
	return root, nil
}

// frameworkUsageDefault is the default clause of every needs --framework flag.
const frameworkUsageDefault = "(default: $" + frameworkDirEnv + ", else <dev root>/golusoris/golusoris; \"\" selects the declared catalog)"

// selectFrameworkDir returns the Golusoris checkout a needs command inspects: the
// --framework value when the flag was given (an explicit "" selects the declared catalog),
// else $PRAETOR_FRAMEWORK_DIR, else <dev root>/golusoris/golusoris. The needs engine
// reports a selected path that does not exist.
func selectFrameworkDir(fs *flag.FlagSet, value string) (string, error) {
	if flagWasSet(fs, "framework") {
		return value, nil
	}
	if dir := os.Getenv(frameworkDirEnv); dir != "" {
		return dir, nil
	}
	root, err := resolveDevRootDir("", "--framework")
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "golusoris", "golusoris"), nil
}
