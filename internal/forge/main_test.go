// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

var testConfigRoot string

// TestMain keeps the package's tests independent of the CI runner: GitHub Actions sets
// GITHUB_WORKFLOW_REF, which makes the run-health check skip the named workflow (#1331); a test
// that wants it sets it with t.Setenv.
// It points the user configuration directory at a fresh temporary directory across
// Linux (XDG_CONFIG_HOME/HOME), macOS (HOME), and Windows (AppData/APPDATA) so tests never touch
// the developer's real configuration or claim keys.
func TestMain(m *testing.M) {
	if err := os.Unsetenv("GITHUB_WORKFLOW_REF"); err != nil {
		fmt.Fprintln(os.Stderr, "unset GITHUB_WORKFLOW_REF:", err)
		os.Exit(2)
	}

	root, cleanup, err := testsupport.IsolateUserConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "isolate user config: %v\n", err)
		os.Exit(2)
	}
	testConfigRoot = root

	code := m.Run()
	cleanup()
	os.Exit(code)
}

func TestClaimKey_DefaultPathUnderTempRoot(t *testing.T) {
	if testConfigRoot == "" {
		t.Fatal("testConfigRoot not initialized by TestMain")
	}
	p, err := DefaultClaimKeyPath()
	if err != nil {
		t.Fatalf("DefaultClaimKeyPath: %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(testConfigRoot)
	if err != nil {
		canonicalRoot = testConfigRoot
	}
	canonicalPath, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		canonicalPath = filepath.Dir(p)
	}
	rel, err := filepath.Rel(canonicalRoot, canonicalPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("DefaultClaimKeyPath %s does not lie under testConfigRoot %s (rel: %s)", p, testConfigRoot, rel)
	}
}
