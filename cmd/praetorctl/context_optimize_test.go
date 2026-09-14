// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

func TestContextOptimizeCLIWritesRealPrivateCandidate(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "sources")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	content := bytes.Repeat([]byte("private rule; do not expose\n"), 100)
	for _, name := range []string{"rules.md", "memory.md"} {
		if err := os.WriteFile(filepath.Join(root, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"--root", root, "rules.md", "memory.md"}
	var metadata bytes.Buffer
	if err := contextOptimize(context.Background(), args, &metadata); err != nil {
		t.Fatal(err)
	}
	var report contextopt.Report
	if err := json.Unmarshal(metadata.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.SavedBytes <= 0 || len(report.Documents) != 1 || bytes.Contains(metadata.Bytes(), []byte("private rule")) {
		t.Fatal("incorrect or leaking analysis")
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 1 {
		t.Fatalf("analysis wrote a candidate: %v", err)
	}
	output := filepath.Join(base, "review-candidate")
	metadata.Reset()
	args = []string{"--root", root, "--output-dir", output, "rules.md", "memory.md"}
	if err := contextOptimize(context.Background(), args, &metadata); err != nil {
		t.Fatal(err)
	}
	pack, err := os.ReadFile(filepath.Join(output, "context.md"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(pack, content) != 1 || len(pack) != report.PackBytes {
		t.Fatal("candidate did not reduce duplicate bytes")
	}
	for _, name := range []string{"rules.md", "memory.md"} {
		actual, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || !bytes.Equal(actual, content) {
			t.Fatalf("source changed: %v", err)
		}
	}
	if _, ok := commandTable()["context-optimize"]; !ok {
		t.Fatal("command not registered")
	}
}

func TestContextOptimizeCLIRejectsInvalidArgumentsAndBounds(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{nil, {"--root", root}, {"--root", root, "../escape.md"}, {"--unknown"}, {"--root", root, "missing.md"}} {
		var output bytes.Buffer
		if err := contextOptimize(context.Background(), args, &output); err == nil || output.Len() != 0 {
			t.Fatalf("invalid request reported success: %v", args)
		}
	}
	args := []string{"--root", root}
	for i := 0; i < contextopt.MaxSources; i++ {
		name := fmt.Sprintf("%d.md", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("bounded"), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, name)
	}
	var output bytes.Buffer
	if err := contextOptimize(context.Background(), args, &output); err != nil {
		t.Fatalf("exact source limit: %v", err)
	}
	output.Reset()
	if err := contextOptimize(context.Background(), append(args, "overflow.md"), &output); err == nil || output.Len() != 0 {
		t.Fatal("source count overflow accepted")
	}
}
