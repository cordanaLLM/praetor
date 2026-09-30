package util

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// configureFilters writes each key and value with git config, into file when it is set and
// into dir's repository configuration otherwise, so git escapes the values itself.
func configureFilters(t *testing.T, dir, file string, entries [][2]string) {
	t.Helper()
	for _, entry := range entries {
		args := []string{"config"}
		if file != "" {
			args = append(args, "--file", file)
		}
		statusGit(t, dir, append(args, entry[0], entry[1])...)
	}
}

// markerFilter is a driver whose every command creates marker, so a test can tell whether git
// ran it. The path is quoted for the shell git runs filter commands through, with forward
// slashes, which Git for Windows' shell reads as well.
func markerFilter(driver, marker string) [][2]string {
	command := "touch '" + filepath.ToSlash(marker) + "'"
	return [][2]string{
		{"filter." + driver + ".clean", command},
		{"filter." + driver + ".smudge", command},
		{"filter." + driver + ".process", command},
		{"filter." + driver + ".required", "true"},
	}
}

func assertNotExecuted(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a filter command ran during the probe: %v", err)
	}
}

// TestRefuseGitStatusFilters_Positive_OperatorDriversNeverReachTheProbe: drivers the system and
// global files define -- where Git for Windows and `git lfs install` put theirs -- are dropped
// with those files, so even a tracked path whose attribute names them is probed without running
// them.
func TestRefuseGitStatusFilters_Positive_OperatorDriversNeverReachTheProbe(t *testing.T) {
	dir := statusRepo(t)
	marker := filepath.Join(t.TempDir(), "filter-ran")
	system := filepath.Join(t.TempDir(), "gitconfig")
	global := filepath.Join(t.TempDir(), "global-gitconfig")
	configureFilters(t, dir, system, markerFilter("lfs", marker))
	configureFilters(t, dir, global, markerFilter("lfs", marker))
	writeStatusFile(t, dir, ".gitattributes", "*.txt filter=lfs\n")
	writeStatusFile(t, dir, "a.txt", "edited\n")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "")
	t.Setenv("GIT_CONFIG_SYSTEM", system)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if out, err := RunGit(t.Context(), dir, "config", "--get-all", "filter.lfs.clean"); err != nil || len(strings.Split(out, "\n")) != 2 {
		t.Fatalf("fixture: plain git must see the operator-level drivers: %q (%v)", out, err)
	}
	if err := RefuseGitStatusFilters(t.Context(), dir); err != nil {
		t.Fatalf("drivers outside the repository's configuration were refused: %v", err)
	}
	got := mustChanges(t, dir)
	if !slices.Equal(got, []string{" M a.txt", "?? .gitattributes"}) {
		t.Fatalf("changes = %q", got)
	}
	assertNotExecuted(t, marker)
}
