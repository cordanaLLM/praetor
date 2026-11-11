// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestContextFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b", "a"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := DeadlineContext(t)
	entries, err := ReadDirContext(ctx, dir, 2)
	if err != nil || len(entries) != 2 || entries[0].Name() != "a" {
		t.Fatalf("entries = %v, %v", entries, err)
	}
	if _, err := ReadDirContext(ctx, dir, 1); err == nil {
		t.Fatal("entry overflow accepted")
	}
	if _, err := StatContext(ctx, filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing stat: %v", err)
	}
	if _, err := StatContext(ctx, filepath.Join(dir, "a")); err != nil {
		t.Fatal(err)
	}
}

func TestContextFilesCancellation(t *testing.T) {
	dir := t.TempDir()
	ctx := DeadlineContext(t)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ReadDirContext(cancelled, dir, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
	if _, err := StatContext(cancelled, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stat: %v", err)
	}
	if _, err := ReadDirContext(ctx, dir, 0); err == nil {
		t.Fatal("zero limit accepted")
	}
}
