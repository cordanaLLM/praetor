// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"errors"
	"fmt"
	"os"
	"runtime"
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
	return quote + executable + quote + " go test-exec", "Go test binaries: loopback-only network namespace (sandbox NetworkNone; with a container engine socket, pasta forwards connects to host ports of the kernel's ephemeral range, where engines publish container ports)", nil
}

// ContainerTestIsolation names the race stage's isolation when its Go tests run in a toolchain
// container. The container runs on its runtime's default network (devcontainer.Image.RunArgs
// passes no --network), a network namespace of its own, so workstation processes cannot reach a
// test server on its loopback. Bubblewrap inside the container could not create a user namespace
// (EPERM; every package of the #1816 gate run of 2026-11-05 failed closed on it), so the -exec
// wrapper adds no isolation there and is not used.
const ContainerTestIsolation = "Go test binaries: the toolchain container's own network namespace (the runtime's default network, never the host's)"

// goTestExec resolves the -exec flag for stage test execution: none in a toolchain container,
// whose own network namespace isolates the test binaries (ContainerTestIsolation), else GoTestExec.
func (c *stageConfig) goTestExec() (string, string, error) {
	if c.container != nil {
		return "", ContainerTestIsolation, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("go test loopback namespace wrapper: %w", err)
	}
	goos := c.goos
	if goos == "" {
		goos = runtime.GOOS
	}
	return GoTestExec(goos, executable)
}
