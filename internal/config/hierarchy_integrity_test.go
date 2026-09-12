package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
