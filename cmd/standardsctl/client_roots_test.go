package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func setHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func mkdirAll(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHostClientEnvTakesNothingFromTheProcessForAForeignHome(t *testing.T) {
	t.Setenv("APPDATA", filepath.Join(t.TempDir(), "roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "local"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg"))
	home := t.TempDir()

	own := hostClientEnv(home, false)
	if own.GOOS != runtime.GOOS || own.Home != home || own.AppData == "" || own.LocalAppData == "" || own.ConfigHome == "" {
		t.Fatalf("own home must carry the process locations: %+v", own)
	}
	foreign := hostClientEnv(home, true)
	if foreign.Home != home || foreign.AppData != "" || foreign.LocalAppData != "" || foreign.ConfigHome != "" {
		t.Fatalf("foreign home must derive from the home alone: %+v", foreign)
	}
	for name, env := range map[string]func(string) string{"own": own.Getenv, "foreign": foreign.Getenv} {
		if env != nil {
			t.Fatalf("%s: unverified relocation variables must stay out of the harvester", name)
		}
	}
}

func TestHostClientEnvAnchorsARelativeHome(t *testing.T) {
	t.Chdir(t.TempDir())
	if got := hostClientEnv("home", true).Home; !filepath.IsAbs(got) || filepath.Base(got) != "home" {
		t.Fatalf("relative home was not anchored: %q", got)
	}
	if got := hostClientEnv("", true).Home; got != "" {
		t.Fatalf("an empty home must stay empty so the helper rejects it, got %q", got)
	}
}

func TestHarvestClientRoots(t *testing.T) {
	home := t.TempDir()
	ide := mkdirAll(t, filepath.Join(home, ".gemini", "antigravity-ide", "brain"))
	legacy := mkdirAll(t, filepath.Join(home, ".gemini", "antigravity", "brain"))

	roots, err := harvestClientRoots(hostClientEnv(home, true))
	if err != nil {
		t.Fatalf("harvestClientRoots: %v", err)
	}
	if roots.AGYConfig != filepath.Join(home, ".gemini", "config") {
		t.Fatalf("configuration root = %q", roots.AGYConfig)
	}
	if !slices.Equal(roots.AGYBrains, []string{legacy, ide}) {
		t.Fatalf("brain roots = %v, want both spellings in winning order", roots.AGYBrains)
	}
	if filepath.Base(roots.ClaudeDesktopConfig) != "claude_desktop_config.json" {
		t.Fatalf("desktop configuration = %q", roots.ClaudeDesktopConfig)
	}
	if (roots.PowerShellHistory != "") != (runtime.GOOS == "windows") {
		t.Fatalf("PowerShell history applies to Windows only, got %q", roots.PowerShellHistory)
	}
}

func TestHarvestClientRootsRejectsAnEmptyHome(t *testing.T) {
	t.Parallel()
	if _, err := harvestClientRoots(hostClientEnv("", true)); err == nil {
		t.Fatal("an empty home must be rejected")
	}
}

func TestResolveBrainRoots(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	got, err := resolveBrainRoots("")
	if err != nil || len(got) != 0 {
		t.Fatalf("no brain directory exists: got %v, %v", got, err)
	}
	cli := mkdirAll(t, filepath.Join(home, ".gemini", "antigravity-cli", "brain"))
	got, err = resolveBrainRoots("")
	if err != nil || !slices.Equal(got, []string{cli}) {
		t.Fatalf("got %v, %v; want %s", got, err, cli)
	}
	got, err = resolveBrainRoots("explicit/brain")
	if err != nil || !slices.Equal(got, []string{"explicit/brain"}) {
		t.Fatalf("an explicit directory is used as given: got %v, %v", got, err)
	}
}

func TestHarvestMemoryReadsEveryExistingBrain(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	mkdirAll(t, filepath.Join(home, ".gemini", "antigravity", "brain", "conv-a"))
	mkdirAll(t, filepath.Join(home, ".gemini", "antigravity-ide", "brain", "conv-b"))
	if err := dispatchCommand("harvest", []string{"memory"}); err != nil {
		t.Fatalf("harvest memory over two brain roots: %v", err)
	}
}
