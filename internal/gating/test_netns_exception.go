// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// moduleTestExec resolves one module's -exec flag and isolation note for its race tests: a live
// go-test-netns entry naming the module's go.mod (config.ExceptionRuleGoTestNetns) runs its test
// binaries in the gate's own namespaces where goTestExec would wrap them, and the note names the
// entry. An expired entry fails the module before any test binary runs, so neither path stands in
// for the other; every other module gets goTestExec unchanged. Where goTestExec wraps nothing (CI,
// a toolchain container, a host without Linux namespaces) an entry changes nothing, and the audit
// fails an expired one (config.ExpiredExceptions).
func (c *stageConfig) moduleTestExec(ctx context.Context, module goModule) (string, string, error) {
	execFlag, isolation, err := c.goTestExec()
	if err != nil || execFlag == "" {
		return execFlag, isolation, err
	}
	entries, err := config.LoadExceptionsFor(ctx, c.repoDir, config.ExceptionRuleGoTestNetns)
	if err != nil {
		return "", "", fmt.Errorf("load %s exceptions: %w", config.ExceptionRuleGoTestNetns, err)
	}
	live, expired := config.ExceptionFor(entries, module.file, time.Now(), nil)
	if live == nil && expired != nil {
		return "", "", fmt.Errorf("exception %s for %s expired %s (category %s): its tests run neither wrapped nor unwrapped until the entry is renewed or removed",
			config.ExceptionRuleGoTestNetns, module.file, expired.Expires, expired.ExpiryCategory())
	}
	if live == nil {
		return execFlag, isolation, nil
	}
	return "", fmt.Sprintf("Go test binaries of %s: the gate's own namespaces, no loopback wrapper (exception %s, category %s, expires %s: %s)",
		module.file, config.ExceptionRuleGoTestNetns, live.ExpiryCategory(), live.Expires, live.Reason), nil
}
