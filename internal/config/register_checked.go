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
const registerRoutingRel = router.DefaultConfigPath

// LoadCheckedRegisterAuthority resolves the text register policy of the repository at root and
// checks the task rows the manifest wrote against the routing vocabulary. The manifest and the
// routing configuration are both optional: without a manifest the defaults govern, and without
// a routing.yaml the router's built-in labels are the vocabulary. Only the rows the manifest
// wrote are checked; a default row is never an error in a repository that declared its own
// labels, and without declared rows routing is not read at all.
func LoadCheckedRegisterAuthority(ctx context.Context, root string) (RegisterAuthority, error) {
	authority, _, _, err := loadCheckedRegisterAuthority(ctx, root, false)
	return authority, err
}

// LoadRegisterTaskAuthority returns the checked manifest snapshot at root together with the
// target_tasks vocabulary that governs it, read once. A runtime boundary that receives a task
// label from untrusted text checks the label against this vocabulary and resolves it through
// the same digest-bound authority compile-context renders.
func LoadRegisterTaskAuthority(ctx context.Context, root string) (RegisterAuthority, []string, error) {
	authority, labels, _, err := loadCheckedRegisterAuthority(ctx, root, true)
	return authority, labels, err
}

// LoadRegisterDispatchAuthority also returns the offload rule from the same routing snapshot.
func LoadRegisterDispatchAuthority(ctx context.Context, root string) (RegisterAuthority, []string, *router.OffloadConfig, error) {
	authority, labels, routing, err := loadCheckedRegisterAuthority(ctx, root, true)
	if routing == nil {
		return authority, labels, nil, err
	}
	return authority, labels, routing.Offload, err
}

// LoadRegisterRoutingAuthority returns register policy and the complete routing snapshot.
func LoadRegisterRoutingAuthority(ctx context.Context, root string) (RegisterAuthority, []string, *router.RoutingConfig, error) {
	return loadCheckedRegisterAuthority(ctx, root, true)
}

func loadCheckedRegisterAuthority(ctx context.Context, root string, withLabels bool) (RegisterAuthority, []string, *router.RoutingConfig, error) {
	if ctx == nil {
		return RegisterAuthority{}, nil, nil, errors.New("text register requires a context")
	}
	if err := ctx.Err(); err != nil {
		return RegisterAuthority{}, nil, nil, err
	}
	authority, err := LoadRegisterAuthority(ctx, root)
	if err != nil {
		return RegisterAuthority{}, nil, nil, err
	}
	if !withLabels && !authority.HasDeclaredTasks() {
		return authority, nil, nil, nil
	}
	labels, routing, err := registerTaskLabels(ctx, root)
	if err != nil {
		return RegisterAuthority{}, nil, nil, err
	}
	if err := authority.ValidateTaskLabels(labels); err != nil {
		return RegisterAuthority{}, nil, nil, fmt.Errorf("%s: %w", registerManifestName, err)
	}
	return authority, labels, routing, nil
}

// registerTaskLabels returns the target_tasks vocabulary that governs root.
func registerTaskLabels(ctx context.Context, root string) ([]string, *router.RoutingConfig, error) {
	path := filepath.Join(root, filepath.FromSlash(registerRoutingRel))
	if !util.FileExists(path) {
		return router.DefaultTaskLabels(), nil, nil
	}
	cfg, err := router.LoadRoutingConfigContext(ctx, path)
	if err != nil {
		return nil, nil, fmt.Errorf("text register task labels: %w", err)
	}
	return router.DeclaredTaskLabels(cfg), cfg, nil
}
