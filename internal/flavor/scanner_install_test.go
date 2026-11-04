// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// TestScannerInstallGuide_3D tests absent, invalid and undeclared tools module fallbacks.
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
	mustWriteFile(t, pin, "module fixture\n")
	for _, scanner := range []string{"govulncheck", "gosec"} {
		if got := flavor.ScannerInstallGuide(root, scanner, fallback); got != fallback {
			t.Errorf("undeclared %s: %s", scanner, got)
		}
	}
	if got := flavor.ScannerInstallGuide(root, "unknown", fallback); got != fallback {
		t.Fatalf("unknown scanner: %s", got)
	}
}

// TestScannerInstallGuide_UnpinnedManifest refuses tool directives without scanner requirements.
func TestScannerInstallGuide_UnpinnedManifest(t *testing.T) {
	for _, scanner := range []struct{ binary, pkg string }{
		{"govulncheck", "golang.org/x/vuln/cmd/govulncheck"},
		{"gosec", "github.com/securego/gosec/v2/cmd/gosec"},
	} {
		root := t.TempDir()
		manifest := "module fixture\ntool " + scanner.pkg + "\n"
		writeSecurityFixture(t, root, map[string]string{"tools/go/go.mod": manifest})
		if got := flavor.ScannerInstallGuide(root, scanner.binary, "fallback"); got != "fallback" {
			t.Errorf("tool directive without %s requirement: %s", scanner.binary, got)
		}
	}
}

// TestScannerInstallGuide_DeclaredScanners verifies each scanner requirement selects the pinned command.
func TestScannerInstallGuide_DeclaredScanners(t *testing.T) {
	root := t.TempDir()
	pin := filepath.Join(root, "tools/go/go.mod")
	manifest := "module fixture\nrequire (\n" +
		"golang.org/x/vuln v1.0.0\n" +
		"github.com/securego/gosec/v2 v2.0.0\n)\n"
	writeSecurityFixture(t, root, map[string]string{"tools/go/go.mod": manifest})
	for _, scanner := range []string{"govulncheck", "gosec"} {
		got := flavor.ScannerInstallGuide(root, scanner, "fallback")
		if !strings.Contains(got, fmt.Sprintf("go -C %q install ", filepath.Dir(pin))) ||
			!strings.HasSuffix(got, "/cmd/"+scanner) {
			t.Errorf("%s pinned install: %s", scanner, got)
		}
	}
}

// TestScannerInstallGuide_Layouts verifies tools-only and workspace repositories use the tools module.
func TestScannerInstallGuide_Layouts(t *testing.T) {
	for _, layout := range []string{"tools-only", "workspace"} {
		t.Run(layout, func(t *testing.T) {
			root := t.TempDir()
			tools := filepath.Join(root, "tools", "go")
			manifest := "module fixture\nrequire golang.org/x/vuln v1.0.0\n"
			writeSecurityFixture(t, root, map[string]string{"tools/go/go.mod": manifest})
			if layout == "workspace" {
				mustWriteFile(t, filepath.Join(root, "go.work"), "go 1.27\nuse .\n")
			}
			prefix := "GOWORK=off "
			if runtime.GOOS == "windows" {
				prefix = "set \"GOWORK=off\" && "
			}
			want := fmt.Sprintf("%sgo -C %q install golang.org/x/vuln/cmd/govulncheck", prefix, tools)
			if got := flavor.ScannerInstallGuide(root, "govulncheck", "fallback"); got != want {
				t.Errorf("restore lacks isolated module context: got %q, want %q", got, want)
			}
		})
	}
}
