package util

import (
	"context"
	"os"
	"slices"
	"testing"
)

// TestGitProbeArgs_PrefixesOverrides checks that every inspection argv starts with the
// filesystem-monitor and hooks overrides, followed by the caller's arguments unchanged.
func TestGitProbeArgs_PrefixesOverrides(t *testing.T) {
	got := GitProbeArgs("ls-files", "--cached", "-z")
	want := []string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull, "ls-files", "--cached", "-z"}
	if !slices.Equal(got, want) {
		t.Fatalf("GitProbeArgs = %q, want %q", got, want)
	}
}

// TestGitProbeArgs_Boundary_NoArguments keeps the overrides when the caller passes nothing.
func TestGitProbeArgs_Boundary_NoArguments(t *testing.T) {
	if got := GitProbeArgs(); len(got) != 4 || got[1] != "core.fsmonitor=false" {
		t.Fatalf("GitProbeArgs() = %q, want only the four override arguments", got)
	}
}

// TestWithGitProbeEnvironment_SetsIsolation checks that the context carries the probe
// environment RunGitProbe uses: no system or global configuration and no lazy fetch.
func TestWithGitProbeEnvironment_SetsIsolation(t *testing.T) {
	ctx, err := WithGitProbeEnvironment(context.Background())
	if err != nil {
		t.Fatalf("WithGitProbeEnvironment: %v", err)
	}
	env, ok := ctx.Value(commandEnvironmentKey{}).([]string)
	if !ok {
		t.Fatal("context carries no command environment")
	}
	for _, want := range []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_NO_LAZY_FETCH=1", "GIT_OPTIONAL_LOCKS=0"} {
		if !slices.Contains(env, want) {
			t.Errorf("probe environment lacks %q: %q", want, env)
		}
	}
}

// TestWithGitProbeEnvironment_Negative_NilContext refuses a nil context instead of panicking.
func TestWithGitProbeEnvironment_Negative_NilContext(t *testing.T) {
	//nolint:staticcheck // the nil context is the case under test
	if _, err := WithGitProbeEnvironment(nil); err == nil {
		t.Fatal("WithGitProbeEnvironment(nil) returned no error")
	}
}
