// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

type manifestSnapshotKey struct{}

type manifestSnapshot struct {
	path string
	data []byte
}

// WithManifestSnapshot binds read-only planning to a manifest snapshot at one absolute path.
// Readers still run normal validation; writers observe the real file and compare before writing.
func WithManifestSnapshot(ctx context.Context, path string, data []byte) context.Context {
	return context.WithValue(ctx, manifestSnapshotKey{}, manifestSnapshot{path: filepath.Clean(path), data: bytes.Clone(data)})
}

func observeManifestSnapshot(ctx context.Context, path string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("observe manifest snapshot: %w", err)
	}
	if snapshot, ok := ctx.Value(manifestSnapshotKey{}).(manifestSnapshot); ok && filepath.Clean(path) == snapshot.path {
		return bytes.Clone(snapshot.data), true, nil
	}
	return contextopt.ObserveSnapshot(ctx, path)
}
