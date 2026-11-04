// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

func TestScannerInstallGuide_3D(t *testing.T) {
	root := t.TempDir()
	const fallback = "existing install advice"
	if got := flavor.ScannerInstallGuide(root, "govulncheck", fallback); got != fallback {
		t.Fatalf("absent tools module: %s", got)
	}
	pin := filepath.Join(root, "tools/go/go.mod")
	if err := os.MkdirAll(pin, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := flavor.ScannerInstallGuide(root, "gosec", fallback); got != fallback {
		t.Fatalf("directory is not a pin file: %s", got)
	}
	if err := os.Remove(pin); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pin, []byte("module fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, scanner := range []string{"govulncheck", "gosec"} {
		got := flavor.ScannerInstallGuide(root, scanner, fallback)
		if !strings.Contains(got, " install -modfile=tools/go/go.mod ") || !strings.HasSuffix(got, "/cmd/"+scanner) {
			t.Errorf("%s pinned install: %s", scanner, got)
		}
	}
	if got := flavor.ScannerInstallGuide(root, "unknown", fallback); got != fallback {
		t.Fatalf("unknown scanner: %s", got)
	}
}
