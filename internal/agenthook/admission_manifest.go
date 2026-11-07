// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// pushManifestReadSlot bounds outstanding filesystem workers to one, including a syscall
// stalled after cancellation. Its worker releases the slot when the filesystem returns.
var pushManifestReadSlot = make(chan struct{}, 1)

// pushManifestReadResult carries bounded snapshot bytes and their read error to admission.
type pushManifestReadResult struct {
	data []byte
	err  error
}

// readPushManifest bounds admission's wait around a context-aware confined snapshot.
// Filesystem syscalls cannot always be interrupted; a buffered result lets that worker
// finish without waiting for its caller, while the single slot caps outstanding work.
func readPushManifest(ctx context.Context, path string, read func(context.Context, string) ([]byte, error)) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("manifest read requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, pushTargetTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case pushManifestReadSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	done := make(chan pushManifestReadResult, 1)
	go func() {
		defer func() { <-pushManifestReadSlot }()
		data, err := read(ctx, path)
		done <- pushManifestReadResult{data: data, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return result.data, result.err
	}
}

// decodePushManifestForge recovers only repository.forge after a structural manifest error.
// The shared YAML decoder still rejects malformed YAML, duplicate forge keys and extra
// documents; Forge.UnmarshalYAML validates the declaration. The original error stays visible.
func decodePushManifestForge(data []byte) (config.Forge, error) {
	manifest, manifestErr := config.DecodeManifest(data)
	if manifestErr == nil {
		return manifest.Repository.Forge, nil
	}
	var section struct {
		Repository struct {
			Forge config.Forge `yaml:"forge"`
		} `yaml:"repository"`
	}
	if err := util.DecodeYAMLDocument(data, &section, util.YAMLDocumentOptions{AllowEmpty: true}); err != nil {
		return "", errors.Join(manifestErr, err)
	}
	return section.Repository.Forge, manifestErr
}
