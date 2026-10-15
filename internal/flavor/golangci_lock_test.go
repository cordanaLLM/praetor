// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// golangciRunsSerialised reports whether text runs golangci-lint (a "golangci-lint" mention
// followed by its run subcommand on the same line or list) only with --allow-serial-runners.
func golangciRunsSerialised(text string) (bool, string) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(line, "golangci-lint") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		// A run spelled over two lines, as the Python list in checks.py is.
		window := line
		if i+1 < len(lines) {
			window += " " + lines[i+1]
		}
		runs := strings.Contains(window, " run") || strings.Contains(window, `"run"`)
		if runs && !strings.Contains(window, "--allow-serial-runners") {
			return false, strings.TrimSpace(line)
		}
	}
	return true, ""
}

// #855: a second golangci-lint started while another holds its lock on the same host waits for
// it instead of failing the gate with "parallel golangci-lint is running". Every invocation
// Praetor runs or prints passes --allow-serial-runners (never --allow-parallel-runners, which
// drops the lock).
func TestGolangciInvocations_Boundary_WaitForTheLock(t *testing.T) {
	for _, rel := range []string{"Makefile", ".config/lefthook/scripts/checks.py"} {
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if ok, line := golangciRunsSerialised(string(data)); !ok {
			t.Errorf("%s runs golangci-lint without --allow-serial-runners: %s", rel, line)
		}
	}
	for _, tool := range (&GoServiceFlavor{}).RequiredToolchains() {
		if ok, line := golangciRunsSerialised(tool.InstallGuide); !ok {
			t.Errorf("the go-service flavor prints a golangci-lint run without --allow-serial-runners: %s", line)
		}
	}
	for text, want := range map[string]bool{
		"go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...":                         false,
		"go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run --allow-serial-runners ./...": true,
		"\"lint\": [\"go\", \"run\", \"golangci-lint@latest\",\n    \"run\", *local],":                             false,
		"golangci-lint version": true,
	} {
		if got, _ := golangciRunsSerialised(text); got != want {
			t.Errorf("golangciRunsSerialised(%q) = %v, want %v", text, got, want)
		}
	}
}
