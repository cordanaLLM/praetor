// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
)

// GoTestExec returns go test's -exec value and the isolation disposition. CI
// detection matches the CLI's hook audit; CI keeps its existing execution path.
// Go's cmd/internal/quoted parser does not unescape quoted strings.
func GoTestExec(goos, executable string) (string, string, error) {
	if os.Getenv("CI") == "true" || os.Getenv("GITHUB_ACTIONS") == "true" {
		return "", "Go test loopback isolation skipped: CI environment; CI execution unchanged", nil
	}
	if goos != "linux" {
		return "", fmt.Sprintf("Go test loopback isolation skipped: %s has no Linux network namespaces", goos), nil
	}
	if executable == "" {
		return "", "", errors.New("go test loopback namespace wrapper: empty executable path")
	}
	quote := "'"
	if strings.Contains(executable, quote) {
		quote = "\""
		if strings.Contains(executable, quote) {
			return "", "", errors.New("go test loopback namespace wrapper: executable path contains both quote types")
		}
	}
	return quote + executable + quote + " go test-exec", "Go test binaries: loopback-only network namespace (sandbox NetworkNone)", nil
}

func (c *stageConfig) goTestExec() (string, string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("go test loopback namespace wrapper: %w", err)
	}
	goos := c.goos
	if goos == "" {
		goos = runtime.GOOS
	}
	execFlag, note, err := GoTestExec(goos, executable)
	if err == nil && execFlag != "" && c.container != nil {
		// Disposable toolchain containers mount paths at their host locations. Make
		// this exact engine available to -exec there too; no PATH-selected version.
		if strings.ContainsAny(executable, ",\n\r") {
			return "", "", errors.New("go test loopback namespace wrapper: executable path cannot be bind-mounted")
		}
		if !slices.Contains(c.container.mounts, executable) {
			c.container.mounts = append(c.container.mounts, executable)
		}
	}
	return execFlag, note, err
}
