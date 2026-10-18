// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestExport_WritesYAMLClientConfigs(t *testing.T) {
	dir := t.TempDir()
	paths, err := export(dir)
	if err != nil {
		t.Fatalf("export() error = %v", err)
	}
	if !slices.Contains(paths, ".continue/mcpServers/praetor.yaml") {
		t.Fatalf("export() paths = %v, want .continue/mcpServers/praetor.yaml", paths)
	}
	content, err := os.ReadFile(filepath.Join(dir, ".continue/mcpServers/praetor.yaml"))
	if err != nil {
		t.Fatalf("read exported config: %v", err)
	}
	if !bytes.HasPrefix(content, []byte("---\n")) {
		t.Fatalf("exported continue config missing document start: %s", content)
	}
}

func TestRun_ValidatesUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("run(nil) = %d, want 2", code)
	}
	stdout.Reset()
	stderr.Reset()
	dir := t.TempDir()
	if code := run([]string{dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(%s) = %d, want 0, stderr: %s", dir, code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(".continue/mcpServers/praetor.yaml")) {
		t.Fatalf("run() stdout = %s, want .continue/mcpServers/praetor.yaml", stdout.String())
	}
}
