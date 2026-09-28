// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/router"
	"github.com/cordanaLLM/praetor/internal/util"
)

// registerRoutingRel is the routing configuration whose target_tasks are the task vocabulary a
// manifest's register rows may name.
const registerRoutingRel = ".config/models/routing.yaml"

// LoadCheckedRegisterAuthority resolves the text register policy of the repository at root and
// checks the task rows the manifest wrote against the routing vocabulary. The manifest and the
// routing configuration are both optional: without a manifest the defaults govern, and without
// a routing.yaml the router's built-in labels are the vocabulary. Only the rows the manifest
// wrote are checked; a default row is never an error in a repository that declared its own
// labels, and without declared rows routing is not read at all.
func LoadCheckedRegisterAuthority(ctx context.Context, root string) (RegisterAuthority, error) {
	authority, _, err := loadCheckedRegisterAuthority(ctx, root, false)
	return authority, err
}

// LoadRegisterTaskAuthority returns the checked manifest snapshot at root together with the
// target_tasks vocabulary that governs it, read once. A runtime boundary that receives a task
// label from untrusted text checks the label against this vocabulary and resolves it through
// the same digest-bound authority compile-context renders.
func LoadRegisterTaskAuthority(ctx context.Context, root string) (RegisterAuthority, []string, error) {
	return loadCheckedRegisterAuthority(ctx, root, true)
}

func loadCheckedRegisterAuthority(ctx context.Context, root string, withLabels bool) (RegisterAuthority, []string, error) {
	if ctx == nil {
		return RegisterAuthority{}, nil, errors.New("text register requires a context")
	}
	if err := ctx.Err(); err != nil {
		return RegisterAuthority{}, nil, err
	}
	authority, err := LoadRegisterAuthority(ctx, root)
	if err != nil {
		return RegisterAuthority{}, nil, err
	}
	if !withLabels && !authority.HasDeclaredTasks() {
		return authority, nil, nil
	}
	labels, err := registerTaskLabels(ctx, root)
	if err != nil {
		return RegisterAuthority{}, nil, err
	}
	if err := authority.ValidateTaskLabels(labels); err != nil {
		return RegisterAuthority{}, nil, fmt.Errorf("%s: %w", registerManifestName, err)
	}
	return authority, labels, nil
}

// registerTaskLabels returns the target_tasks vocabulary that governs root.
func registerTaskLabels(ctx context.Context, root string) ([]string, error) {
	path := filepath.Join(root, filepath.FromSlash(registerRoutingRel))
	if !util.FileExists(path) {
		return router.DefaultTaskLabels(), nil
	}
	cfg, err := router.LoadRoutingConfigContext(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("text register task labels: %w", err)
	}
	return router.DeclaredTaskLabels(cfg), nil
}
