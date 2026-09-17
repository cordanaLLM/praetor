package agenthook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveRoot(t *testing.T) {
	root := repository(t, true)
	nested := filepath.Join(root, "nested directory", "deeper")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	other := repository(t, false)
	for _, tc := range []struct {
		name       string
		workspaces []string
		workDir    string
		want       string
	}{
		{"process directory", nil, root, root},
		{"nested directory with a space", nil, nested, root},
		{"payload workspace wins", []string{nested, other}, other, root},
		{"empty payload entry falls back", []string{""}, other, other},
	} {
		got, err := ResolveRoot(context.Background(), tc.workspaces, tc.workDir)
		if err != nil || got != tc.want {
			t.Errorf("%s: %q %v, want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestResolveRootFailures(t *testing.T) {
	root := repository(t, true)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveRoot(context.Background(), nil, outside); err != nil || got != "" {
		t.Errorf("no repository must be an empty root without an error: %q %v", got, err)
	}
	for name, dir := range map[string]string{
		"empty": "", "relative": "relative", "missing": filepath.Join(root, "missing"),
		"a file": filepath.Join(root, manifestName),
	} {
		if got, err := ResolveRoot(context.Background(), nil, dir); err == nil || got != "" {
			t.Errorf("%s resolved to %q", name, got)
		}
	}
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if got, err := ResolveRoot(expired, nil, root); err == nil || got != "" {
		t.Errorf("expired context resolved to %q", got)
	}
}

func TestGoverned(t *testing.T) {
	if !Governed(repository(t, true)) {
		t.Error("manifest at the root is not governed")
	}
	if Governed(repository(t, false)) || Governed("") || Governed(filepath.Join(t.TempDir(), "missing")) {
		t.Error("a root without a manifest is governed")
	}
	directoryNamedLikeTheManifest := t.TempDir()
	if err := os.Mkdir(filepath.Join(directoryNamedLikeTheManifest, manifestName), 0o700); err != nil {
		t.Fatal(err)
	}
	if Governed(directoryNamedLikeTheManifest) {
		t.Error("a directory named like the manifest is governed")
	}
}

func TestResolveRootWithoutGitFailsClosed(t *testing.T) {
	root := repository(t, true)
	t.Setenv("PATH", t.TempDir())
	if got, err := ResolveRoot(context.Background(), nil, root); err == nil || got != "" {
		t.Fatalf("a host without git resolved %q; that must be an error, not an absent repository", got)
	}
	response := Run(context.Background(), Invocation{
		Client: "claude", Event: "pre-tool", Stdin: strings.NewReader(`{"tool_input":{"command":"go version"}}`),
		Getenv: noEnvironment, WorkDir: root, Policy: policy(t),
	})
	if response.ExitCode != 2 {
		t.Fatalf("a host without git allowed a command: %s", response)
	}
}
