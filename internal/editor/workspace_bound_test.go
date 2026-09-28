package editor

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// largeWorkspace writes count Go files under src/ of a fresh workspace. Past
// maxLanguageObservations files it also proves the scan keeps each language once: a scan that
// recorded one observation per file refused the tree as too many observations.
func largeWorkspace(t *testing.T, count int) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "src")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		if err := os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)+".go"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// boundOptions targets one editor at root under the requested scan bound.
func boundOptions(root string, bound int) Options {
	opts := DefaultOptions()
	opts.WorkspaceRoot = root
	opts.Editors = []string{EditorVSCode}
	opts.MaxWorkspaceFiles = bound
	return opts
}

// Positive: a bound raised through Options.MaxWorkspaceFiles reaches the language scan, so a
// workspace above the 4096-file default synthesizes and still reports its language (issue #535).
func TestSynthesize_Positive_RaisedWorkspaceBoundScansALargeWorkspace(t *testing.T) {
	count := maxLanguageObservations + 1
	root := largeWorkspace(t, count)
	opts := boundOptions(root, count)
	if _, err := SynthesizeContext(t.Context(), opts); err != nil {
		t.Fatalf("raised bound %d refused a %d-file workspace: %v", count, count, err)
	}
	editors, _ := normalizeEditors(opts.Editors)
	plan, err := resolvePlan(t.Context(), opts, editors)
	if err != nil || !slices.Contains(plan.Languages, "go") {
		t.Fatalf("raised-bound plan = %v, %v; want go observed", plan.Languages, err)
	}
}

// Negative: the default bound still stops at 4096 files with ErrWorkspaceScanBound, and a bound
// outside 1..util.DiscoveryEntriesCeiling is refused before any file is read.
func TestSynthesize_Negative_WorkspaceBoundRefusals(t *testing.T) {
	root := largeWorkspace(t, util.DefaultDiscoveryEntries+1)
	_, err := SynthesizeContext(t.Context(), boundOptions(root, 0))
	if !errors.Is(err, ErrWorkspaceScanBound) || !strings.Contains(err.Error(), "4096 files") {
		t.Fatalf("default bound on a 4097-file workspace = %v; want ErrWorkspaceScanBound naming 4096 files", err)
	}
	for _, bound := range []int{-1, util.DiscoveryEntriesCeiling + 1} {
		_, err := SynthesizeContext(t.Context(), boundOptions(t.TempDir(), bound))
		if err == nil || errors.Is(err, ErrWorkspaceScanBound) || !strings.Contains(err.Error(), "must be 1..200000") {
			t.Errorf("bound %d = %v; want refusal naming the 1..200000 range", bound, err)
		}
	}
}

// Boundary: exactly the bound's worth of files passes and one more fails, at the default and at a
// raised value; the ceiling itself is admitted and zero selects the shared default.
func TestSynthesize_Boundary_WorkspaceBoundIsExact(t *testing.T) {
	root := largeWorkspace(t, util.DefaultDiscoveryEntries)
	if _, err := SynthesizeContext(t.Context(), boundOptions(root, 0)); err != nil {
		t.Fatalf("default bound refused exactly %d files: %v", util.DefaultDiscoveryEntries, err)
	}
	if _, err := SynthesizeContext(t.Context(), boundOptions(root, util.DefaultDiscoveryEntries-1)); !errors.Is(err, ErrWorkspaceScanBound) {
		t.Fatalf("bound %d admitted %d files: %v", util.DefaultDiscoveryEntries-1, util.DefaultDiscoveryEntries, err)
	}
	for requested, want := range map[int]int{0: util.DefaultDiscoveryEntries, util.DiscoveryEntriesCeiling: util.DiscoveryEntriesCeiling, 1: 1} {
		if got, err := workspaceFileBound(requested); err != nil || got != want {
			t.Errorf("workspaceFileBound(%d) = %d, %v; want %d", requested, got, err, want)
		}
	}
}
