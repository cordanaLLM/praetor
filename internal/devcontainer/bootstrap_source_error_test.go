package devcontainer

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TestRunSourceGitCarriesGitStderr_3D covers runSourceGit's diagnostic (#839, HISS-15).
// Positive: an inventory of a checkout succeeds. Negative: a command git refuses names the
// command and carries git's own standard error, and git missing from PATH keeps exec's error;
// neither claims a cause git did not report. Boundary: no arguments are refused before git runs.
func TestRunSourceGitCarriesGitStderr_3D(t *testing.T) {
	root := bootstrapSourceFixture(t)
	if _, err := runSourceGit(t.Context(), root, "ls-files", "-z"); err != nil {
		t.Fatalf("inventory of a checkout failed: %v", err)
	}

	_, err := runSourceGit(t.Context(), root, "ls-files", "--error-unmatch", "--", "absent.go")
	if err == nil {
		t.Fatal("ls-files --error-unmatch of an absent path succeeded")
	}
	for _, phrase := range []string{"inventory with git ls-files", "absent.go"} {
		if !strings.Contains(err.Error(), phrase) {
			t.Fatalf("git failure lacks %q: %v", phrase, err)
		}
	}
	if strings.Contains(err.Error(), "not a Git checkout") {
		t.Fatalf("git failure in a checkout claims it is none: %v", err)
	}

	if _, err := runSourceGit(t.Context(), root); err == nil || !strings.Contains(err.Error(), "requires arguments") {
		t.Fatalf("argument-less git command = %v, want a refusal", err)
	}

	t.Setenv("PATH", t.TempDir())
	_, err = runSourceGit(t.Context(), root, "ls-files", "-z")
	if !errors.Is(err, exec.ErrNotFound) || !strings.Contains(err.Error(), "inventory with git ls-files") || strings.Contains(err.Error(), "not a Git checkout") {
		t.Fatalf("git missing from PATH = %v, want the named command and exec.ErrNotFound only", err)
	}
}

// TestCaptureBootstrapSourceNamesTheCause_3D covers the capture's refusals (#839, HISS-15).
// Positive: a checkout passes CheckSource and captures. Negative: go.mod outside any Git checkout
// is refused up front as ErrSourceNotGitCheckout naming the root, and a checkout whose git cannot
// run is a SourceError that does not claim the root is no checkout. Boundary: an empty root and
// a root without go.mod are no bootstrap source and pass.
func TestCaptureBootstrapSourceNamesTheCause_3D(t *testing.T) {
	root := bootstrapSourceFixture(t)
	if err := CheckSource(t.Context(), root); err != nil {
		t.Fatalf("CheckSource(checkout) = %v", err)
	}
	if files, err := captureBootstrapSource(t.Context(), root); err != nil || len(files) == 0 {
		t.Fatalf("capture of a checkout = %d files, %v", len(files), err)
	}

	for _, empty := range []string{"", t.TempDir()} {
		if err := CheckSource(t.Context(), empty); err != nil {
			t.Fatalf("CheckSource(%q) without go.mod = %v, want nil", empty, err)
		}
		if files, err := captureBootstrapSource(t.Context(), empty); err != nil || files != nil {
			t.Fatalf("capture of %q without go.mod = %v, %v; want nothing", empty, files, err)
		}
	}

	export := t.TempDir()
	if present, err := util.GitWorktreePresent(t.Context(), export); err != nil || present {
		t.Skipf("temporary directory sits inside a Git work tree (%v, %v); the refusal needs one outside", present, err)
	}
	writeBootstrapFile(t, export, "go.mod", "module github.com/cordanaLLM/praetor\n\ngo 1.27\n")
	for name, err := range map[string]error{"CheckSource": CheckSource(t.Context(), export), "capture": captureErr(t, export)} {
		var source *SourceError
		if !errors.As(err, &source) || source.Root != export || !errors.Is(err, ErrSourceNotGitCheckout) {
			t.Fatalf("%s of a source export = %v, want a SourceError for %q wrapping ErrSourceNotGitCheckout", name, err, export)
		}
		want := "bootstrap source " + strconv.Quote(export) + ": not a Git checkout; the bootstrap inventories it with git ls-files"
		if err.Error() != want {
			t.Fatalf("%s refusal = %q, want %q", name, err, want)
		}
	}

	t.Setenv("PATH", t.TempDir())
	err := captureErr(t, root)
	var source *SourceError
	if !errors.As(err, &source) || errors.Is(err, ErrSourceNotGitCheckout) || !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("capture without git = %v, want a SourceError carrying exec.ErrNotFound and no checkout claim", err)
	}
}

func captureErr(t *testing.T, root string) error {
	t.Helper()
	_, err := captureBootstrapSource(t.Context(), root)
	return err
}

// TestNameSource_3D covers NameSource (#839, HISS-15). Positive: a wrapped SourceError gains the
// flag or parameter name and keeps its chain. Negative: PrepareBundle's refusal of a builder
// image, which is no source failure, is returned unchanged. Boundary: a nil error and an empty
// name are returned unchanged.
func TestNameSource_3D(t *testing.T) {
	wrapped := fmt.Errorf("prepare devcontainer bootstrap: %w", &SourceError{Root: "/praetor", Err: ErrSourceNotGitCheckout})
	named := NameSource(wrapped, "--source-root")
	want := `--source-root: prepare devcontainer bootstrap: bootstrap source "/praetor": not a Git checkout; the bootstrap inventories it with git ls-files`
	if named == nil || named.Error() != want || !errors.Is(named, ErrSourceNotGitCheckout) {
		t.Fatalf("NameSource = %v, want %q with the chain kept", named, want)
	}

	_, err := PrepareBundle(t.Context(), "app", nil, nil, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t), BuilderImage: "golang:bad"})
	if err == nil {
		t.Fatal("PrepareBundle accepted an undigested builder image")
	}
	if got := NameSource(err, "--source-root"); got.Error() != err.Error() || strings.Contains(got.Error(), "--source-root") {
		t.Fatalf("NameSource renamed an image refusal: %v", got)
	}

	if got := NameSource(nil, "--source-root"); got != nil {
		t.Fatalf("NameSource(nil) = %v", got)
	}
	if got := NameSource(wrapped, ""); got == nil || got.Error() != wrapped.Error() {
		t.Fatalf("NameSource without a name = %v, want the error unchanged", got)
	}
}
