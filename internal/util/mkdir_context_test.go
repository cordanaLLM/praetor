// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package util_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// TestMkdirAllContextPositiveAndBoundary pins nested creation, default permissions, and existing-directory reuse.
func TestMkdirAllContextPositiveAndBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		perm os.FileMode
	}{
		{"explicit permissions", 0o700},
		{"default permissions", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := testsupport.DeadlineContext(t)
			path := filepath.Join(t.TempDir(), "nested", "leaf")
			if err := util.MkdirAllContext(ctx, path, tc.perm); err != nil {
				t.Fatal(err)
			}
			if err := util.MkdirAllContext(ctx, path, tc.perm); err != nil {
				t.Fatalf("existing directory refused: %v", err)
			}
			if info, err := os.Stat(path); err != nil || !info.IsDir() {
				t.Fatalf("directory missing: %v", err)
			}
		})
	}
}

// TestMkdirAllContextNegative pins refusal before mutation for invalid contexts and insecure permissions.
func TestMkdirAllContextNegative(t *testing.T) {
	ctx := testsupport.DeadlineContext(t)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		perm os.FileMode
		want error
	}{
		{"nil context", nil, 0o700, nil},
		{"cancelled context", cancelled, 0o700, context.Canceled},
		{"world-writable permissions", ctx, 0o777, util.ErrInsecurePerm},
		{"non-permission bits", ctx, os.ModeDir | 0o700, util.ErrInsecurePerm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "refused")
			err := util.MkdirAllContext(tc.ctx, path, tc.perm)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Errorf("want refusal %v, got %v", tc.want, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("refused call mutated path: %v", err)
			}
		})
	}
}
