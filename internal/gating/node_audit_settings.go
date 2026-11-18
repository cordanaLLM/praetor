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
	"os"
	"strings"

	"github.com/cordanaLLM/praetor/internal/nodemanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

type nodeAuditPolicy struct {
	Ignore []string `json:"ignore" yaml:"ignore"`
}

type nodeAuditLegacyConfig struct {
	IgnoreGHSAs []string `json:"ignoreGhsas" yaml:"ignoreGhsas"`
	IgnoreCVEs  []string `json:"ignoreCves" yaml:"ignoreCves"`
}

type nodeAuditSettings struct {
	Include     map[string][]string    `json:"-" yaml:"include"`
	Audit       *nodeAuditPolicy       `json:"audit" yaml:"audit"`
	AuditConfig *nodeAuditLegacyConfig `json:"auditConfig" yaml:"auditConfig"`
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
	if settings.AuditConfig != nil && len(settings.AuditConfig.IgnoreCVEs) > 0 {
		return nil, fmt.Errorf("pnpm auditConfig.ignoreCves cannot be covered by GHSA exceptions in .standards.yaml; use ignoreGhsas")
	}
	var ids []string
	if settings.Audit != nil && settings.Audit.Ignore != nil {
		ids = settings.Audit.Ignore
	} else if settings.AuditConfig != nil {
		ids = settings.AuditConfig.IgnoreGHSAs
	}
	return uniqueNodeIgnoreIDs(ids)
}

// readNodeAuditSettings prefers pnpm-workspace.yaml, which pnpm 12 reads, over legacy package policy.
func readNodeAuditSettings(root string) (nodeAuditSettings, error) {
	var settings nodeAuditSettings
	err := util.InConfinedDirectory(root, ".", func(dir *os.Root) error {
		var readErr error
		settings, readErr = readNodeAuditSettingsIn(dir)
		return readErr
	})
	return settings, err
}

// readNodeAuditSettingsIn reads workspace or manifest audit policy through one pinned root handle.
func readNodeAuditSettingsIn(dir *os.Root) (nodeAuditSettings, error) {
	var settings nodeAuditSettings
	data, err := util.ReadLimitedIn(dir, "pnpm-workspace.yaml", nodemanifest.MaxManifestBytes)
	if err == nil {
		if err := util.DecodeYAMLDocument(data, &settings, util.YAMLDocumentOptions{AllowEmpty: true}); err != nil {
			return settings, fmt.Errorf("parse pnpm-workspace.yaml audit policy: %w", err)
		}
		if err := resolveNodeAuditIncludes(dir, &settings); err != nil {
			return settings, err
		}
		return settings, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return settings, fmt.Errorf("read pnpm-workspace.yaml audit policy: %w", err)
	}
	manifest, err := parseNodeManifestIn(dir)
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

// resolveNodeAuditIncludes resolves pnpm 12.15+ workspace include delegations for audit settings.
func resolveNodeAuditIncludes(dir *os.Root, settings *nodeAuditSettings) error {
	auditOwner, configOwner, err := findNodeAuditIncludeOwners(settings)
	if err != nil {
		return err
	}
	if auditOwner != "" {
		included, err := readIncludedNodeAuditFile(dir, auditOwner)
		if err != nil {
			return err
		}
		settings.Audit = included.Audit
	}
	if configOwner != "" {
		included, err := readIncludedNodeAuditFile(dir, configOwner)
		if err != nil {
			return err
		}
		settings.AuditConfig = included.AuditConfig
	}
	return nil
}

// findNodeAuditIncludeOwners enforces pnpm's single-owner rule for delegated audit and auditConfig keys.
func findNodeAuditIncludeOwners(settings *nodeAuditSettings) (string, string, error) {
	var auditOwner, configOwner string
	paths := util.SortedKeys(settings.Include)
	for i := 0; i < len(paths) && i < maxNodeAdvisories; i++ {
		path := paths[i]
		fields := settings.Include[path]
		for j := 0; j < len(fields) && j < maxNodeAdvisories; j++ {
			if err := assignNodeAuditIncludeOwner(fields[j], path, settings, &auditOwner, &configOwner); err != nil {
				return "", "", err
			}
		}
	}
	return auditOwner, configOwner, nil
}

// assignNodeAuditIncludeOwner records one delegated setting owner and rejects duplicate or inline conflicts.
func assignNodeAuditIncludeOwner(field, path string, settings *nodeAuditSettings, auditOwner, configOwner *string) error {
	switch field {
	case "audit":
		if settings.Audit != nil || *auditOwner != "" {
			return fmt.Errorf("pnpm-workspace.yaml audit setting must have a single owner")
		}
		*auditOwner = path
	case "auditConfig":
		if settings.AuditConfig != nil || *configOwner != "" {
			return fmt.Errorf("pnpm-workspace.yaml auditConfig setting must have a single owner")
		}
		*configOwner = path
	}
	return nil
}

// readIncludedNodeAuditFile reads one repository-local included YAML file through the pinned root handle.
func readIncludedNodeAuditFile(dir *os.Root, path string) (nodeAuditSettings, error) {
	var included nodeAuditSettings
	if !strings.HasPrefix(path, "./") && !strings.HasPrefix(path, "../") {
		return included, fmt.Errorf("pnpm-workspace.yaml include %q must name a local repository file starting with ./", path)
	}
	if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
		return included, fmt.Errorf("pnpm-workspace.yaml include %q must name a .yaml or .yml file", path)
	}
	data, err := util.ReadLimitedIn(dir, strings.TrimPrefix(path, "./"), nodemanifest.MaxManifestBytes)
	if err != nil {
		return included, fmt.Errorf("read included pnpm audit policy %s: %w", path, err)
	}
	if err := util.DecodeYAMLDocument(data, &included, util.YAMLDocumentOptions{AllowEmpty: true}); err != nil {
		return included, fmt.Errorf("parse included pnpm audit policy %s: %w", path, err)
	}
	if included.Include != nil {
		return included, fmt.Errorf("included pnpm audit policy %s must not define include", path)
	}
	return included, nil
}
