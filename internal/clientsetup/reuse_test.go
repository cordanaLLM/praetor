// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package clientsetup

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	strictREUSEToml = "version = 1\n\n[[annotations]]\npath = [\"LICENSES/**\"]\nSPDX-License-Identifier = \"EUPL-1.2\"\n"
	fixtureLicense  = "European Union Public Licence (EUPL) v1.2\n"
)

// setupStrictREUSERepo initializes a temporary git repository with a strict REUSE.toml
// without catch-all annotations, ensuring reuse lint checks each file individually.
func setupStrictREUSERepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	licensesDir := filepath.Join(root, "LICENSES")
	if err := os.MkdirAll(licensesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(licensesDir, "EUPL-1.2.txt"), []byte(fixtureLicense), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "REUSE.toml"), []byte(strictREUSEToml), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func runReuseLint(t *testing.T, root string) (string, bool) {
	t.Helper()
	reusePath, err := exec.LookPath("reuse")
	if err != nil {
		t.Skip("reuse is not installed on PATH: skipping reuse lint verification")
	}
	res, err := util.RunCommandBytes(t.Context(), root, reusePath, util.MaxCommandOutputBytes, "--root", root, "lint")
	return string(res.Stdout) + string(res.Stderr), err == nil
}

// Planted: verify that the files main's clientsetup writes (without comments or sidecars)
// are refused by reuse lint in a strict repository without a catch-all glob (#1342).
func TestPlantedMainEmittedFilesFailReuseLint(t *testing.T) {
	root := setupStrictREUSERepo(t)
	continuePath := filepath.Join(root, ".continue", "mcpServers", "praetor.yaml")
	if err := os.MkdirAll(filepath.Dir(continuePath), 0o755); err != nil {
		t.Fatal(err)
	}
	mainContinueYAML := "name: Praetor MCP\nversion: 1.0.0\nschema: v1\nmcpServers:\n  - name: asdecided\n    command: praetorctl\n    args:\n      - decisions\n      - serve\n"
	if err := os.WriteFile(continuePath, []byte(mainContinueYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	mcpJSON := filepath.Join(root, ".mcp.json")
	if err := os.WriteFile(mcpJSON, []byte("{\n  \"mcpServers\": {}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opencodeJSON := filepath.Join(root, "opencode.json")
	if err := os.WriteFile(opencodeJSON, []byte("{\n  \"mcp\": {}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, passed := runReuseLint(t, root)
	if passed {
		t.Fatal("expected reuse lint to fail on main's emitted files, but it passed")
	}
	for _, expectedFile := range []string{
		filepath.ToSlash(filepath.Join(".continue", "mcpServers", "praetor.yaml")),
		".mcp.json",
		"opencode.json",
	} {
		if !strings.Contains(out, expectedFile) {
			t.Errorf("expected reuse lint to report missing license for %s, output was:\n%s", expectedFile, out)
		}
	}
}

// Positive: every client configuration emitted by clientsetup carries licence information
// that reuse lint accepts in a repository without a catch-all annotation (#1342).
func TestEmittedClientConfigsPassReuseLint(t *testing.T) {
	root := setupStrictREUSERepo(t)
	reg := Registry{Version: 1, Servers: []Server{{Name: "asdecided", Command: "praetorctl", Args: []string{"decisions", "serve"}}}}
	clients := []Client{Continue, Claude, OpenCodeV1, Gemini}
	for i := 0; i < len(clients) && i < 8; i++ {
		client := clients[i]
		plan, err := BuildProjectPlan(t.Context(), reg, client, nil)
		if err != nil {
			t.Fatalf("build project plan for %s: %v", client, err)
		}
		target := filepath.Join(root, filepath.FromSlash(plan.RelativePath))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, plan.Content, 0o644); err != nil {
			t.Fatal(err)
		}
		if plan.SidecarPath != "" {
			sidecarTarget := filepath.Join(root, filepath.FromSlash(plan.SidecarPath))
			if err := os.WriteFile(sidecarTarget, plan.SidecarContent, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	out, passed := runReuseLint(t, root)
	if !passed {
		t.Fatalf("reuse lint failed on emitted client configs:\n%s", out)
	}
	if !strings.Contains(out, "Files with copyright information: 4 / 4") &&
		!strings.Contains(out, "Files with copyright information: 5 / 5") {
		t.Logf("reuse lint output:\n%s", out)
	}
}

// Positive: sidecars are kept in step and rewritten when JSON configuration is rewritten.
func TestSidecarRewrittenWhenJSONRewritten(t *testing.T) {
	root := setupStrictREUSERepo(t)
	reg := Registry{Version: 1, Servers: []Server{{Name: "asdecided", Command: "praetorctl", Args: []string{"decisions", "serve"}}}}
	plan, err := BuildProjectPlan(t.Context(), reg, Claude, nil)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, plan.RelativePath)
	sidecar := filepath.Join(root, plan.SidecarPath)
	if err := os.WriteFile(target, plan.Content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecar, plan.SidecarContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Update registry with another server and rewrite plan
	reg2 := Registry{Version: 1, Servers: []Server{
		{Name: "asdecided", Command: "praetorctl", Args: []string{"decisions", "serve"}},
		{Name: "second", Command: "praetorctl", Args: []string{"second"}},
	}}
	plan2, err := BuildProjectPlan(t.Context(), reg2, Claude, plan.Content)
	if err != nil {
		t.Fatal(err)
	}
	if !plan2.Changed {
		t.Fatal("expected plan to report changed for new server")
	}
	if err := os.WriteFile(target, plan2.Content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecar, plan2.SidecarContent, 0o644); err != nil {
		t.Fatal(err)
	}
	sidecarBytes, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sidecarBytes, []byte(LicenseSidecar)) {
		t.Fatalf("sidecar content differs: %q", sidecarBytes)
	}
}

// Boundary: an adopter-owned sidecar with custom text is recognized and kept.
func TestIsAdopterOwnedSidecar(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want bool
	}{
		{"empty", nil, false},
		{"whitespace only", []byte("   \n\t  "), false},
		{"canonical engine sidecar", []byte(LicenseSidecar), false},
		{"canonical engine sidecar CRLF", []byte(strings.ReplaceAll(LicenseSidecar, "\n", "\r\n")), false},
		{"canonical without trailing newline", []byte(strings.TrimSpace(LicenseSidecar)), false},
		{"adopter custom copyright", []byte("SPDX-FileCopyrightText: 2024 Acme Corp\nSPDX-License-Identifier: MIT\n"), true},
		{"adopter custom licence", []byte("SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>\nSPDX-License-Identifier: Apache-2.0\n"), true},
		{"adopter additional notes", []byte(LicenseSidecar + "# Custom adopter note\n"), true},
	}
	for i := 0; i < len(cases) && i < 16; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAdopterOwnedSidecar(tc.raw); got != tc.want {
				t.Errorf("IsAdopterOwnedSidecar(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
