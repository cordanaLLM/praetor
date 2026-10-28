// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// agyValidationFinding represents an individual finding reported by agy plugin validate.
type agyValidationFinding struct {
	Severity string `json:"severity"`
	File     string `json:"file"`
	Message  string `json:"message"`
}

// agyValidationReport captures the parsed JSON output from agy plugin validate.
type agyValidationReport struct {
	Findings []agyValidationFinding `json:"findings"`
}

// maxPluginCopyFiles bounds the number of filesystem entries copied (HISS-02).
const maxPluginCopyFiles = 256

// copyPluginDir copies every entry under src into dst bounded by maxPluginCopyFiles without recursion.
func copyPluginDir(t *testing.T, src, dst string) {
	t.Helper()
	seen := 0
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		seen++
		if seen > maxPluginCopyFiles {
			return fmt.Errorf("plugin copy exceeds %d entries (HISS-02)", maxPluginCopyFiles)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copyPluginDir(%s, %s): %v", src, dst, err)
	}
}

// shippedPluginDir resolves the repository plugin directory from PluginManifestRel's parent.
func shippedPluginDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", filepath.Dir(filepath.FromSlash(PluginManifestRel)))
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("shipped plugin directory not found at %s: %v", dir, err)
	}
	return dir
}

// writePluginFile writes content to path or fails the test.
func writePluginFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("writeFile(%s): %v", path, err)
	}
}

// replaceInFile replaces oldStr with newStr in the file at path or fails the test.
func replaceInFile(t *testing.T, path, oldStr, newStr string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readFile(%s): %v", path, err)
	}
	if !bytes.Contains(data, []byte(oldStr)) {
		t.Fatalf("%s does not contain target %q to replace", path, oldStr)
	}
	replaced := bytes.ReplaceAll(data, []byte(oldStr), []byte(newStr))
	if err := os.WriteFile(path, replaced, 0o644); err != nil {
		t.Fatalf("writeFile(%s): %v", path, err)
	}
}

// runAgyPluginValidate runs `agy plugin validate <dir> --strict --json` with a 60s timeout,
// returning the process exit code and parsed findings.
func runAgyPluginValidate(t *testing.T, dir string) (int, []agyValidationFinding) {
	t.Helper()
	agyBin, err := exec.LookPath("agy")
	if err != nil {
		t.Skip("agy not on PATH: Antigravity plugin validation needs the Antigravity CLI")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	out, err := util.RunCommandBytes(ctx, "", agyBin, 1<<20, "plugin", "validate", dir, "--strict", "--json")
	rc := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("exec agy plugin validate: %v (stderr: %s)", err, out.Stderr)
		}
		rc = exitErr.ExitCode()
	}

	var report agyValidationReport
	if err := json.Unmarshal(out.Stdout, &report); err != nil && rc == 0 {
		head := string(out.Stdout)
		if len(head) > 200 {
			head = head[:200]
		}
		t.Fatalf("unparsable JSON on rc 0 (head %q): %v", head, err)
	}
	return rc, report.Findings
}

// Positive: repository plugin dir validates with rc 0 and zero error findings.
func TestAgyPluginValidate_Positive_ShippedPlugin(t *testing.T) {
	dir := shippedPluginDir(t)
	rc, findings := runAgyPluginValidate(t, dir)
	if rc != 0 {
		t.Fatalf("expected rc 0 for shipped plugin, got %d with findings: %+v", rc, findings)
	}
	for i := 0; i < len(findings); i++ {
		if findings[i].Severity == "error" {
			t.Errorf("unexpected error finding: %s: %s", findings[i].File, findings[i].Message)
		}
	}
}

// Negative: three planted invalid copies in t.TempDir each produce non-zero rc.
func TestAgyPluginValidate_Negative_PlantedInvalidManifests(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, dir string)
	}{
		{
			name: "broken_plugin_json",
			mutate: func(t *testing.T, dir string) {
				writePluginFile(t, filepath.Join(dir, "plugin.json"), []byte(`{"name": 5`))
			},
		},
		{
			name: "unknown_hook_event",
			mutate: func(t *testing.T, dir string) {
				replaceInFile(t, filepath.Join(dir, "hooks.json"), "PreToolUse", "NotAnEvent")
			},
		},
		{
			name: "invalid_plugin_name",
			mutate: func(t *testing.T, dir string) {
				replaceInFile(t, filepath.Join(dir, "plugin.json"), `"name": "praetor"`, `"name": "Bad Name!"`)
			},
		},
	}
	srcDir := shippedPluginDir(t)
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "plugin")
			copyPluginDir(t, srcDir, dst)
			tc.mutate(t, dst)
			rc, _ := runAgyPluginValidate(t, dst)
			if rc == 0 {
				t.Fatalf("planted invalid manifest %s unexpectedly returned rc 0", tc.name)
			}
		})
	}
}

// Boundary: unmodified copy of the plugin directory validates with rc 0 and zero error findings.
func TestAgyPluginValidate_Boundary_UnmodifiedCopy(t *testing.T) {
	srcDir := shippedPluginDir(t)
	dst := filepath.Join(t.TempDir(), "plugin")
	copyPluginDir(t, srcDir, dst)
	rc, findings := runAgyPluginValidate(t, dst)
	if rc != 0 {
		t.Fatalf("expected rc 0 for unmodified copy, got %d with findings: %+v", rc, findings)
	}
	for i := 0; i < len(findings); i++ {
		if findings[i].Severity == "error" {
			t.Errorf("unexpected error finding in copy: %s: %s", findings[i].File, findings[i].Message)
		}
	}
}
