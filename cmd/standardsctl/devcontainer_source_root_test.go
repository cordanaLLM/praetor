package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/util"
)

// TestDevContainerGenerateSourceRootRefusals_3D covers which generate errors name --source-root
// (#839, HISS-15). Positive: a checkout prepares a bundle. Negative: a source export holding
// go.mod is refused naming the flag, the path and the cause, and an undigested builder image,
// which is no source failure, keeps its own subject without the flag. Boundary: git missing from
// PATH names the flag and the git command, and claims no cause git did not report.
func TestDevContainerGenerateSourceRootRefusals_3D(t *testing.T) {
	manifest, output := cliBootstrapPaths(t)
	checkout := cliBootstrapSource(t)
	base := []string{"generate", "--config", manifest, "--output", output}
	if _, err := captureStdout(t, func() error { return runDevContainer(append(base, "--source-root", checkout)) }); err != nil {
		t.Fatalf("generate from a checkout: %v", err)
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}

	export := t.TempDir()
	if present, err := util.GitWorktreePresent(t.Context(), export); err != nil || present {
		t.Skipf("temporary directory sits inside a Git work tree (%v, %v); the refusal needs one outside", present, err)
	}
	writeFixtureFile(t, export, "go.mod", "module github.com/cordanaLLM/praetor\n\ngo 1.27\n")
	err := runDevContainer(append(base, "--source-root", export))
	if !errors.Is(err, devcontainer.ErrSourceNotGitCheckout) || !strings.Contains(err.Error(), nonGitSourceRefusal("--source-root", export)) {
		t.Fatalf("generate from a source export = %v, want %q", err, nonGitSourceRefusal("--source-root", export))
	}
	if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("refused generate wrote %s: %v", output, statErr)
	}

	err = runDevContainer(append(base, "--source-root", checkout, "--builder-image", "golang:bad"))
	if err == nil || !strings.HasPrefix(err.Error(), "prepare devcontainer bootstrap: ") || strings.Contains(err.Error(), "--source-root") {
		t.Fatalf("image refusal = %v, want its own subject without --source-root", err)
	}

	t.Setenv("PATH", t.TempDir())
	err = runDevContainer(append(base, "--source-root", checkout))
	if !errors.Is(err, exec.ErrNotFound) || errors.Is(err, devcontainer.ErrSourceNotGitCheckout) ||
		!strings.Contains(err.Error(), "--source-root: prepare devcontainer bootstrap: bootstrap source "+quoted(checkout)+": inventory with git ls-files") {
		t.Fatalf("generate without git = %v, want the flag, the path and the git command only", err)
	}
}
