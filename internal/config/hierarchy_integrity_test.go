package config

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCascadingRunnerConfigRejectsUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	for _, org := range []string{"../outside", "nested/org", ".", "/absolute"} {
		if _, err := LoadCascadingRunnerConfigContext(t.Context(), root, org); err == nil {
			t.Fatalf("accepted organization path %q", org)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := LoadCascadingRunnerConfigContext(ctx, root, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "fleet.yaml")
	if err := os.WriteFile(outside, []byte("runners:\n  default: external\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".config", "fleet.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCascadingRunnerConfigContext(t.Context(), root, ""); err == nil {
		t.Fatal("followed fleet symlink")
	}
}

func loadRunnerTiers(t *testing.T, root, org string) *RunnerPolicy {
	t.Helper()
	policy, err := LoadCascadingRunnerConfigContext(t.Context(), root, org)
	if err != nil {
		t.Fatalf("load runner tiers: %v", err)
	}
	return policy
}

// An overridden platform takes the tier's route and every platform the tier leaves out
// keeps the built-in route.
func TestCascadingRunnerConfig_Positive_OverrideKeepsOtherRoutes(t *testing.T) {
	root := t.TempDir()
	writePolicyFile(t, root, ".config/fleet.yaml",
		"runners:\n  routing:\n    darwin/arm64:\n      type: github-hosted\n      runs_on: [macos-15]\n      ephemeral: true\n")
	policy := loadRunnerTiers(t, root, "")
	defaults := DefaultRunnerPolicy()
	if got := policy.Routing["darwin/arm64"].RunsOn; len(got) != 1 || got[0] != "macos-15" {
		t.Errorf("fleet override lost: %v", got)
	}
	if len(policy.Routing) != len(defaults.Routing) {
		t.Errorf("override changed the route count: %v", policy.Routing)
	}
	if got := policy.Routing["linux/gpu"].RunsOn; len(got) != 1 || got[0] != defaults.Routing["linux/gpu"].RunsOn[0] {
		t.Errorf("untouched built-in route changed: %v", got)
	}
}

// A null route removes the inherited entry in every YAML spelling of null, and a later
// tier can add the platform back.
func TestCascadingRunnerConfig_Negative_NullRemovesRoute(t *testing.T) {
	root := t.TempDir()
	writePolicyFile(t, root, ".config/fleet.yaml",
		"runners:\n  routing:\n    linux/gpu: null\n    linux/arm64: ~\n    darwin/amd64:\n")
	policy := loadRunnerTiers(t, root, "")
	for _, platform := range []string{"linux/gpu", "linux/arm64", "darwin/amd64"} {
		if spec, ok := policy.Routing[platform]; ok {
			t.Errorf("null did not remove %s: %+v", platform, spec)
		}
	}
	if _, ok := policy.Routing["darwin/arm64"]; !ok {
		t.Error("removal dropped a platform the tier did not name")
	}

	writePolicyFile(t, root, ".config/orgs/example.yaml",
		"runners:\n  routing:\n    linux/gpu:\n      type: self-hosted-arc\n      runs_on: [example-gpu]\n      ephemeral: true\n")
	readded := loadRunnerTiers(t, root, "example")
	if got := readded.Routing["linux/gpu"].RunsOn; len(got) != 1 || got[0] != "example-gpu" {
		t.Errorf("org tier could not restore the removed route: %v", got)
	}
}

// Removing every route leaves an empty routing table and the default runner intact, and
// removing a platform that has no route is a no-op.
func TestCascadingRunnerConfig_Boundary_RemoveEveryRoute(t *testing.T) {
	root := t.TempDir()
	var fleet strings.Builder
	fleet.WriteString("runners:\n  routing:\n")
	for _, platform := range slices.Sorted(maps.Keys(DefaultRunnerPolicy().Routing)) {
		fleet.WriteString("    " + platform + ": null\n")
	}
	fleet.WriteString("    windows/amd64: null\n")
	writePolicyFile(t, root, ".config/fleet.yaml", fleet.String())
	writePolicyFile(t, root, ".standards.yaml", "runners:\n  routing:\n    linux/gpu: null\n")

	policy := loadRunnerTiers(t, root, "")
	if policy.Routing == nil || len(policy.Routing) != 0 {
		t.Errorf("want an empty, non-nil routing table, got %#v", policy.Routing)
	}
	if policy.Default != DefaultRunnerPolicy().Default {
		t.Errorf("removing routes changed the default runner: %q", policy.Default)
	}
}
