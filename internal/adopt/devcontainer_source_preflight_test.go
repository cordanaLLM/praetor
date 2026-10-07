package adopt

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/util"
)

// sourceExportWithModule is a lock source bundle holding the Praetor go.mod outside any Git
// checkout, as a git archive export of a Praetor checkout is (#839).
func sourceExportWithModule(t *testing.T) string {
	t.Helper()
	source := newAdoptLockSource(t)
	if present, err := util.GitWorktreePresent(t.Context(), source); err != nil || present {
		t.Skipf("temporary directory sits inside a Git work tree (%v, %v); the refusal needs one outside", present, err)
	}
	mustWrite(t, filepath.Join(source, "go.mod"), "module github.com/cordanaLLM/praetor\n\ngo 1.27\n")
	return source
}

// TestAdoptRefusesUncapturableSourceBeforeWriting covers the negative case of the dev-container
// preflight (#839, HISS-15): a lock source the bootstrap cannot capture stops a real adoption
// before its first step writes, naming the source and the cause, and the target gains no file.
// Checked only in the dev-container step, the run wrote the manifest, the ignore rules, the
// lock, the catalog, the baseline and the agent harness first.
func TestAdoptRefusesUncapturableSourceBeforeWriting(t *testing.T) {
	source := sourceExportWithModule(t)
	repo := t.TempDir()
	initTestGit(t, repo)
	before, err := os.ReadDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Adopt(t.Context(), AdoptOptions{Path: repo, LockSourceRoot: source})
	var refused *devcontainer.SourceError
	if !errors.As(err, &refused) || refused.Root != source || !errors.Is(err, devcontainer.ErrSourceNotGitCheckout) {
		t.Fatalf("Adopt with a source export = %v, want a SourceError for %q wrapping ErrSourceNotGitCheckout", err, source)
	}
	if !strings.Contains(err.Error(), "prepare devcontainer bootstrap: bootstrap source") || strings.Contains(err.Error(), "--lock-source-root") {
		t.Fatalf("refusal = %v, want the step's context and no CLI flag name", err)
	}
	after, readErr := os.ReadDir(repo)
	if readErr != nil || len(after) != len(before) {
		t.Fatalf("refused adoption wrote into the target: %v -> %v (%v)", before, after, readErr)
	}
	if report != nil && len(report.CreatedFiles)+len(report.ReconciledFiles) != 0 {
		t.Fatalf("refused adoption reports writes: created %v, reconciled %v", report.CreatedFiles, report.ReconciledFiles)
	}
}

// TestPreflightDevContainerSource_3D covers which runs the preflight checks (HISS-15). Positive:
// the same bundle inside a Git checkout passes. Negative: a forced run over an existing
// DevContainer regenerates it, so the export is refused; a checkout git cannot list passes the
// preflight and fails in the step under the step's context, naming no CLI flag. Boundary: a
// declined dev-container step, a preserved DevContainer and a run without a lock source capture
// nothing and pass.
func TestPreflightDevContainerSource_3D(t *testing.T) {
	export := sourceExportWithModule(t)
	checkout := sourceExportWithModule(t)
	initTestGit(t, checkout)
	if err := preflightDevContainerSource(t.Context(), bootstrapAdoptSession(t, checkout, false), nil); err != nil {
		t.Fatalf("preflight of a checkout = %v", err)
	}

	if err := preflightDevContainerSource(t.Context(), bootstrapAdoptSession(t, export, true), map[string]bool{devContainerStep: true}); err != nil {
		t.Fatalf("declined dev-container step checked the source: %v", err)
	}
	if err := preflightDevContainerSource(t.Context(), bootstrapAdoptSession(t, "", false), nil); err != nil {
		t.Fatalf("run without a lock source checked one: %v", err)
	}
	preserved := bootstrapAdoptSession(t, export, false)
	mustWrite(t, filepath.Join(preserved.repoPath, devcontainerFile), "{\"image\":\"operator/custom:tag\"}\n")
	if err := preflightDevContainerSource(t.Context(), preserved, nil); err != nil {
		t.Fatalf("preserved DevContainer checked the source: %v", err)
	}

	preserved.opts.Force = true
	if err := preflightDevContainerSource(t.Context(), preserved, nil); !errors.Is(err, devcontainer.ErrSourceNotGitCheckout) {
		t.Fatalf("forced regeneration from a source export = %v, want ErrSourceNotGitCheckout", err)
	}

	// A .git file naming no repository: the metadata is there, and git itself refuses the root.
	broken := adoptBootstrapSource(t)
	if err := os.RemoveAll(filepath.Join(broken, ".git")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(broken, ".git"), "gitdir: "+filepath.Join(t.TempDir(), "absent")+"\n")
	unlisted := bootstrapAdoptSession(t, broken, false)
	if err := preflightDevContainerSource(t.Context(), unlisted, nil); err != nil {
		t.Fatalf("preflight ran git or refused checkout metadata: %v", err)
	}
	err := reconcileDevContainer(t.Context(), unlisted)
	want := "prepare devcontainer bootstrap: bootstrap source " + strconv.Quote(broken) + ": inventory with git ls-files"
	var exit *exec.ExitError
	if !errors.As(err, &exit) || !strings.HasPrefix(err.Error(), want) || errors.Is(err, devcontainer.ErrSourceNotGitCheckout) {
		t.Fatalf("step over a broken checkout = %v, want prefix %q, git's exit status and no claim of its own", err, want)
	}
}
