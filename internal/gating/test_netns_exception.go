// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// moduleTestExec resolves one module's -exec flag and isolation note for its race tests: a live
// go-test-netns entry naming the module's go.mod (config.ExceptionRuleGoTestNetns) runs its test
// binaries in the gate's own namespaces where goTestExec would wrap them, and the note names the
// entry. An expired entry fails the module before any test binary runs, wherever the stage runs, so
// neither path stands in for the other; every other module gets goTestExec unchanged. Where
// goTestExec wraps nothing (CI, a toolchain container, a host without Linux namespaces) a live
// entry changes nothing.
func (c *stageConfig) moduleTestExec(ctx context.Context, module goModule) (string, string, error) {
	execFlag, isolation, err := c.goTestExec()
	if err != nil {
		return "", "", err
	}
	entries, err := c.goTestNetnsEntries(ctx)
	if err != nil {
		return "", "", err
	}
	live, expired := config.ExceptionFor(entries, module.file, time.Now(), nil)
	if live == nil && expired != nil {
		return "", "", fmt.Errorf("exception %s for %s expired %s (category %s): its tests run neither wrapped nor unwrapped until the entry is renewed or removed",
			config.ExceptionRuleGoTestNetns, module.file, expired.Expires, expired.ExpiryCategory())
	}
	if live == nil || execFlag == "" {
		return execFlag, isolation, nil
	}
	return "", fmt.Sprintf("Go test binaries of %s: the gate's own namespaces, no loopback wrapper (exception %s, category %s, expires %s: %s)",
		module.file, config.ExceptionRuleGoTestNetns, live.ExpiryCategory(), live.Expires, live.Reason), nil
}

// goTestNetnsEntries reads the go-test-netns entries of the repository manifest once per stage
// configuration, as goModules discovers modules once, so the stale check and every module run
// judge the same entries.
func (c *stageConfig) goTestNetnsEntries(ctx context.Context) ([]config.Exception, error) {
	if c.netnsLoaded {
		return c.netnsEntries, nil
	}
	entries, err := config.LoadExceptionsFor(ctx, c.repoDir, config.ExceptionRuleGoTestNetns)
	if err != nil {
		return nil, fmt.Errorf("load %s exceptions: %w", config.ExceptionRuleGoTestNetns, err)
	}
	c.netnsEntries, c.netnsLoaded = entries, true
	return entries, nil
}

// staleNetnsEntries fails the race stage before any module runs when a go-test-netns entry names
// no tracked Go module, as a stale gosec entry fails the security stage: an entry left behind
// after its module moved or went excuses nothing and would linger unseen until it expires.
func staleNetnsEntries(ctx context.Context, cfg *stageConfig) error {
	modules, err := cfg.goModules(ctx)
	if err != nil {
		return err
	}
	entries, err := cfg.goTestNetnsEntries(ctx)
	if err != nil {
		return err
	}
	for index := 0; index < len(entries) && index < config.MaxExceptions; index++ {
		path := entries[index].Path
		if !slices.ContainsFunc(modules, func(module goModule) bool { return module.file == path }) {
			return fmt.Errorf("exception %s names %s, which is no tracked Go module: remove the entry or name the module's go.mod",
				config.ExceptionRuleGoTestNetns, path)
		}
	}
	return nil
}
