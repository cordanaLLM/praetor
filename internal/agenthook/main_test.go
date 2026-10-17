// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

var testConfigRoot string

// TestMain points the user configuration directory at a fresh temporary directory across
// Linux (XDG_CONFIG_HOME/HOME), macOS (HOME), and Windows (AppData/APPDATA) so tests never touch
// the developer's real configuration or claim keys.
func TestMain(m *testing.M) {
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
	p, err := forge.DefaultClaimKeyPath()
	if err != nil {
		t.Fatalf("DefaultClaimKeyPath: %v", err)
	}
	rel, under, err := testsupport.PathUnder(testConfigRoot, filepath.Dir(p))
	if err != nil || !under {
		t.Fatalf("DefaultClaimKeyPath %s does not lie under testConfigRoot %s (rel: %s, %v)", p, testConfigRoot, rel, err)
	}
}
