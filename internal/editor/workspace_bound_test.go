package editor

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// boundFiles is the scan bound these tests set through Options.MaxWorkspaceFiles in place of the
// default, so a fixture stays small whatever util.DefaultDiscoveryEntries is: the scan applies
// whichever bound it is given, and workspaceFileBound pins what zero resolves to.
const boundFiles = 64

// largeWorkspace writes count Go files under src/ of a fresh workspace.
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

// Positive: a bound set through Options.MaxWorkspaceFiles reaches the language scan, so a
// workspace at that bound synthesizes and still reports its language (issue #535). Past
// maxLanguageObservations files the scan keeps each language once: a scan that recorded one
// observation per file refused such a tree as too many observations. That many files are visited
// directly rather than written, since maxLanguageObservations grows with the default.
func TestSynthesize_Positive_RaisedWorkspaceBoundScansALargeWorkspace(t *testing.T) {
	root := largeWorkspace(t, boundFiles)
	opts := boundOptions(root, boundFiles)
	if _, err := SynthesizeContext(t.Context(), opts); err != nil {
		t.Fatalf("bound %d refused a %d-file workspace: %v", boundFiles, boundFiles, err)
	}
	editors, _ := normalizeEditors(opts.Editors)
	plan, err := resolvePlan(t.Context(), opts, editors)
	if err != nil || !slices.Contains(plan.Languages, "go") {
		t.Fatalf("bounded plan = %v, %v; want go observed", plan.Languages, err)
	}
	file := filepath.Join(root, "src", "f0.go")
	info, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	scan := workspaceLanguageScan{ctx: t.Context(), root: root, fileBound: maxLanguageObservations + 1}
	for i := 0; i <= maxLanguageObservations; i++ {
		if err := scan.visit(file, fs.FileInfoToDirEntry(info), nil); err != nil {
			t.Fatalf("visit %d of %d files: %v", i+1, maxLanguageObservations+1, err)
		}
	}
	if languages, err := normalizeLanguages(scan.languages); err != nil || !slices.Equal(languages, []string{"go"}) {
		t.Fatalf("%d Go files observed as %v, %v; want go once", maxLanguageObservations+1, languages, err)
	}
}

// Negative: the scan stops one file past its bound with ErrWorkspaceScanBound naming it, and a
// bound outside 1..util.DiscoveryEntriesCeiling is refused before any file is read.
func TestSynthesize_Negative_WorkspaceBoundRefusals(t *testing.T) {
	root := largeWorkspace(t, boundFiles+1)
	_, err := SynthesizeContext(t.Context(), boundOptions(root, boundFiles))
	if want := strconv.Itoa(boundFiles) + " files"; !errors.Is(err, ErrWorkspaceScanBound) || !strings.Contains(err.Error(), want) {
		t.Fatalf("bound %d on a %d-file workspace = %v; want ErrWorkspaceScanBound naming %s", boundFiles, boundFiles+1, err, want)
	}
	for _, bound := range []int{-1, util.DiscoveryEntriesCeiling + 1} {
		_, err := SynthesizeContext(t.Context(), boundOptions(t.TempDir(), bound))
		if err == nil || errors.Is(err, ErrWorkspaceScanBound) || !strings.Contains(err.Error(), "must be 1..200000") {
			t.Errorf("bound %d = %v; want refusal naming the 1..200000 range", bound, err)
		}
	}
}

// Boundary: exactly the bound's worth of files passes and one bound lower fails; zero selects the
// shared default, which admits the workspace, and the ceiling itself is admitted.
func TestSynthesize_Boundary_WorkspaceBoundIsExact(t *testing.T) {
	root := largeWorkspace(t, boundFiles)
	if _, err := SynthesizeContext(t.Context(), boundOptions(root, boundFiles)); err != nil {
		t.Fatalf("bound %d refused exactly %d files: %v", boundFiles, boundFiles, err)
	}
	if _, err := SynthesizeContext(t.Context(), boundOptions(root, boundFiles-1)); !errors.Is(err, ErrWorkspaceScanBound) {
		t.Fatalf("bound %d admitted %d files: %v", boundFiles-1, boundFiles, err)
	}
	if _, err := SynthesizeContext(t.Context(), boundOptions(root, 0)); err != nil {
		t.Fatalf("the default bound refused %d files: %v", boundFiles, err)
	}
	for requested, want := range map[int]int{0: util.DefaultDiscoveryEntries, util.DiscoveryEntriesCeiling: util.DiscoveryEntriesCeiling, 1: 1} {
		if got, err := workspaceFileBound(requested); err != nil || got != want {
			t.Errorf("workspaceFileBound(%d) = %d, %v; want %d", requested, got, err, want)
		}
	}
}
