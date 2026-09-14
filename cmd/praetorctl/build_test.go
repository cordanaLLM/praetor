package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/builder"
)

func TestBuildCLIRejectsUnexecutedBackend(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "absent-output")
	config := filepath.Join(dir, ".framework-build.yaml")
	manifest := fmt.Sprintf("version: 1\nproject: fixture\noutput_dir: %q\noptimize: true\ntargets:\n  app:\n    runtime: go\n    entrypoint: missing.go\n", output)
	if err := os.WriteFile(config, []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	text, err := captureStdout(t, func() error {
		return dispatchCommand("build", []string{"--config", config})
	})
	if !errors.Is(err, builder.ErrBackendUnavailable) {
		t.Fatalf("CLI lost backend rejection: %q, %v", text, err)
	}
	for _, fabricated := range []string{"[PASS]", "Successfully compiled", "Artifacts:", "Built static"} {
		if strings.Contains(text, fabricated) {
			t.Fatalf("CLI reported unexecuted work: %q", text)
		}
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("CLI created output directory: %v", err)
	}
}

func TestBuildCLIRejectsMissingConfiguration(t *testing.T) {
	text, err := captureStdout(t, func() error {
		return dispatchCommand("build", []string{"--config", filepath.Join(t.TempDir(), "missing.yaml")})
	})
	if !errors.Is(err, os.ErrNotExist) || text != "" {
		t.Fatalf("missing config must fail before build output: %q, %v", text, err)
	}
}

func TestBuildCLIRejectsUnsupportedRuntime(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "build.yaml", "targets:\n  app:\n    runtime: unknown\n")
	text, err := captureStdout(t, func() error {
		return dispatchCommand("build", []string{"--config", filepath.Join(dir, "build.yaml")})
	})
	if !errors.Is(err, builder.ErrUnsupportedRuntime) || strings.Contains(text, "[PASS]") {
		t.Fatalf("unknown runtime falsely accepted: %q, %v", text, err)
	}
}
