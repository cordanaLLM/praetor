package contextopt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSnapshotCreatesAndReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "artifact")
	for _, value := range []string{"original", "replacement"} {
		if err := WriteSnapshot(t.Context(), path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		actual, err := ReadSnapshot(t.Context(), path)
		if err != nil || string(actual) != value {
			t.Fatalf("snapshot not published: %q %v", actual, err)
		}
	}
}

func TestWriteSnapshotRejectsInvalidInputBeforeCreatingParents(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"oversize", []byte(strings.Repeat("x", MaxSourceBytes+1)), 0o600},
		{"invalid UTF8", []byte{0xff}, 0o600},
		{"executable", []byte("text"), 0o755},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "new")
			if err := WriteSnapshot(t.Context(), filepath.Join(parent, "artifact"), tc.data, tc.mode); err == nil {
				t.Fatal("invalid input accepted")
			}
			if _, err := os.Stat(parent); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid input created parents: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := WriteSnapshot(ctx, filepath.Join(t.TempDir(), "artifact"), []byte("x"), 0o600); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	var absent context.Context
	if err := WriteSnapshot(absent, "ignored", []byte("x"), 0o600); err == nil {
		t.Fatal("nil context accepted")
	}
}
