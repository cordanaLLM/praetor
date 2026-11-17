// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/cordanaLLM/praetor/internal/nodemanifest"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

type nodeAuditSettings struct {
	Audit struct {
		Ignore []string `json:"ignore" yaml:"ignore"`
	} `json:"audit" yaml:"audit"`
	AuditConfig struct {
		IgnoreGHSAs []string `json:"ignoreGhsas" yaml:"ignoreGhsas"`
		IgnoreCVEs  []string `json:"ignoreCves" yaml:"ignoreCves"`
	} `json:"auditConfig" yaml:"auditConfig"`
}

// nodeAuditIgnored reads repository-local pnpm audit policy without querying any user config.
func nodeAuditIgnored(ctx context.Context, root, manager string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if manager != nodeManagerPnpm {
		return nil, nil
	}
	settings, err := readNodeAuditSettings(root)
	if err != nil {
		return nil, err
	}
	if len(settings.AuditConfig.IgnoreCVEs) > 0 {
		return nil, fmt.Errorf("pnpm auditConfig.ignoreCves cannot be covered by GHSA exceptions in .standards.yaml; use ignoreGhsas")
	}
	ids := settings.Audit.Ignore
	if ids == nil {
		ids = settings.AuditConfig.IgnoreGHSAs
	}
	return uniqueNodeIgnoreIDs(ids)
}

// readNodeAuditSettings prefers pnpm-workspace.yaml, which pnpm 12 reads, over legacy package policy.
func readNodeAuditSettings(root string) (nodeAuditSettings, error) {
	var settings nodeAuditSettings
	data, err := util.ReadConfinedLimited(root, "pnpm-workspace.yaml", nodemanifest.MaxManifestBytes)
	if err == nil {
		if err := yaml.Unmarshal(data, &settings); err != nil {
			return settings, fmt.Errorf("parse pnpm-workspace.yaml audit policy: %w", err)
		}
		return settings, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return settings, fmt.Errorf("read pnpm-workspace.yaml audit policy: %w", err)
	}
	manifest, err := parseNodeManifest(root)
	if err != nil {
		return settings, err
	}
	if len(manifest.Pnpm) > 0 {
		if err := json.Unmarshal(manifest.Pnpm, &settings); err != nil {
			return settings, fmt.Errorf("parse package.json pnpm audit policy: %w", err)
		}
	}
	return settings, nil
}
