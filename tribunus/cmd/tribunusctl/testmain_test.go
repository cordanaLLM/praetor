package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the user state folder at a throwaway directory, so no test
// of this package writes a ceiling store into the real one. GOCACHE is pinned
// first because Go's Windows default lives below LOCALAPPDATA.
func TestMain(m *testing.M) {
	os.Exit(runIsolated(m))
}

func runIsolated(m *testing.M) int {
	state, err := os.MkdirTemp("", "tribunusctl-state-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() {
		if removeErr := os.RemoveAll(state); removeErr != nil {
			fmt.Fprintln(os.Stderr, removeErr)
		}
	}()
	if os.Getenv("GOCACHE") == "" {
		cache, cacheErr := os.UserCacheDir()
		if cacheErr != nil {
			fmt.Fprintln(os.Stderr, cacheErr)
			return 1
		}
		if setErr := os.Setenv("GOCACHE", filepath.Join(cache, "go-build")); setErr != nil {
			fmt.Fprintln(os.Stderr, setErr)
			return 1
		}
	}
	for _, name := range []string{"LOCALAPPDATA", "XDG_STATE_HOME"} {
		if setErr := os.Setenv(name, state); setErr != nil {
			fmt.Fprintln(os.Stderr, setErr)
			return 1
		}
	}
	return m.Run()
}
