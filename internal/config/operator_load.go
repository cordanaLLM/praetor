// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"errors"
	"time"
)

// LoadOperatorSettings resolves the operator settings from the fleet and/or workstation
// documents a SettingsSelection names, independent of any repository manifest: `hook`,
// `clients`, `workstation` and `needs` run outside a governed repository as often as inside
// one (an ungoverned or missing workspace is still a valid hook call, 3.2 of the rollout
// spec), so this does not go through LoadEffectivePolicyContext, which requires one. Neither
// document configured returns the built-in defaults. audit and gate never call this: they
// take explicit flags only (install_manifest.go).
func LoadOperatorSettings(ctx context.Context, selection SettingsSelection) (OperatorSettings, error) {
	policy, err := LoadOperatorPolicy(ctx, selection)
	if err != nil {
		return OperatorSettings{}, err
	}
	return policy.OperatorSettings(), nil
}

// LoadOperatorPolicy is LoadOperatorSettings keeping the resolved policy, so a caller can
// resolve a relative document path against the layer file that set it (ResolveOperatorPath).
// Neither document configured returns a nil policy, whose OperatorSettings are the built-in
// defaults.
func LoadOperatorPolicy(ctx context.Context, selection SettingsSelection) (*EffectivePolicy, error) {
	if ctx == nil {
		return nil, errors.New("operator settings load requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	layers, err := operatorLayers(ctx, selection)
	if err != nil {
		return nil, err
	}
	if len(layers) == 0 {
		return nil, nil
	}
	return ResolvePolicy(ctx, layers)
}

// SelectOperatorPolicy selects the documents request names (SelectOperatorSettings) and
// loads them (LoadOperatorPolicy): the one path every command and MCP tool that reads
// operator settings takes.
func SelectOperatorPolicy(ctx context.Context, request SettingsRequest) (*EffectivePolicy, error) {
	selection, err := SelectOperatorSettings(ctx, request)
	if err != nil {
		return nil, err
	}
	return LoadOperatorPolicy(ctx, selection)
}

// operatorLayers decodes whichever of the fleet and workstation documents are configured, in
// that order, reusing the same document/externalLayer path LoadEffectivePolicyContext uses
// for the same two layers (effective_load.go); it only skips the repository manifest.
func operatorLayers(ctx context.Context, selection SettingsSelection) ([]PolicyLayer, error) {
	loader := effectiveLoader{ctx: ctx}
	documents := []struct{ id, path string }{
		{"fleet", selection.Fleet.Path}, {"workstation", selection.Workstation.Path},
	}
	layers := make([]PolicyLayer, 0, len(documents))
	for _, document := range documents {
		if document.path == "" {
			continue
		}
		layer, err := loader.externalLayer(document.id, document.path)
		if err != nil {
			return nil, err
		}
		layers = append(layers, layer)
	}
	return layers, nil
}
