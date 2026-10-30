// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"fmt"
	"path/filepath"
	"testing"
)

// BuildFakeGitleaks builds a stand-in gitleaks executable in dir that accepts detect
// arguments and writes a JSON array report to the --report-path argument. If reportPayload
// is provided, that payload is written; otherwise, an empty JSON array "[]" is written.
func BuildFakeGitleaks(t testing.TB, dir string, reportPayload ...string) string {
	t.Helper()
	payload := "[]"
	if len(reportPayload) > 0 && reportPayload[0] != "" {
		payload = reportPayload[0]
	}
	source := fmt.Sprintf(`package main

import (
	"os"
	"strings"
)

func main() {
	payload := []byte(%q)
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--report-path" && i+1 < len(os.Args) {
			_ = os.WriteFile(os.Args[i+1], payload, 0o600)
			os.Exit(0)
		}
		if strings.HasPrefix(arg, "--report-path=") {
			path := strings.TrimPrefix(arg, "--report-path=")
			_ = os.WriteFile(path, payload, 0o600)
			os.Exit(0)
		}
	}
	os.Exit(0)
}
`, payload)
	return BuildExecutable(t, dir, "gitleaks", source)
}

// BuildFakeMise builds a stand-in mise executable in dir. When invoked as gitleaks
// (via a shim symlink) with HOME different from expectedHome, it reports an untrusted
// configuration error on stderr and exits 1. When invoked as "which gitleaks", it
// prints realGitleaksPath to stdout and exits 0.
func BuildFakeMise(t testing.TB, dir, expectedHome, realGitleaksPath string) string {
	t.Helper()
	configPath := filepath.Join(expectedHome, ".config", "mise", "config.toml")
	source := fmt.Sprintf(`package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	base := filepath.Base(os.Args[0])
	if base == "gitleaks" || base == "gitleaks.exe" {
		if os.Getenv("HOME") != %q {
			fmt.Fprintf(os.Stderr, "mise ERROR error parsing config file: %%s / mise ERROR Config files in %%s are not trusted.\n", %q, %q)
			os.Exit(1)
		}
	}
	if len(os.Args) >= 3 && os.Args[1] == "which" && os.Args[2] == "gitleaks" {
		fmt.Println(%q)
		return
	}
	fmt.Fprintf(os.Stderr, "fake mise: unrecognized arguments: %%v\n", os.Args)
	os.Exit(2)
}
`, expectedHome, configPath, configPath, realGitleaksPath)
	return BuildExecutable(t, dir, "mise", source)
}
