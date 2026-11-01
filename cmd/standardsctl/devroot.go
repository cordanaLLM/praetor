package main

import (
	"github.com/cordanaLLM/praetor/internal/needs"
	"github.com/cordanaLLM/praetor/internal/topology"
)

// Workstation dev-root environment names. PRAETOR_DEV_ROOT is the canonical name used by
// AGENTS.md and scripts/adopt_repos.sh; PRAETOR_DEV_DIR is the earlier name, kept
// as a fallback so an operator environment that still sets it keeps selecting the same tree.
// frameworkDirEnv names the go framework checkout (needs.SelectFrameworkSource).
const (
	devRootEnv       = topology.DevRootEnv
	legacyDevRootEnv = topology.LegacyDevRootEnv
	frameworkDirEnv  = needs.FrameworkDirEnv
)

// devRootUsageDefault is the default clause every dev-root flag prints in its usage line.
const devRootUsageDefault = "(default: $" + devRootEnv + ", else $" + legacyDevRootEnv + ", else <home>/dev)"

var (
	configuredDevRoot = topology.ConfiguredDevRoot
	resolveDevRootDir = topology.ResolveDevRootDir
	resolveHomeSubdir = topology.ResolveHomeSubdir
)

// frameworkUsageDefault is the default clause of every needs --framework flag.
const frameworkUsageDefault = "(default: $" + frameworkDirEnv + ", else framework.targets.go.checkout, " +
	"else framework.targets.go.contract, else framework.targets.go.module; \"\" selects the declaration)"
