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

	"go.yaml.in/yaml/v3"

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

const (
	// maxNodeAuditIncludes caps include entries examined before resolving any source.
	maxNodeAuditIncludes = 64
	// maxNodeAuditWildcardFiles caps wildcard policy files read per workspace.
	maxNodeAuditWildcardFiles = 16
)

type nodeAuditSettings struct {
	Include        any                    `json:"-" yaml:"include"`
	Audit          *nodeAuditPolicy       `json:"audit" yaml:"audit"`
	AuditConfig    *nodeAuditLegacyConfig `json:"auditConfig" yaml:"auditConfig"`
	auditDefined   bool
	includeDefined bool
}

type nodeAuditInclude struct {
	path     string
	wildcard bool
	audit    bool
}

// decodeNodeAuditSettings preserves defined null settings and include presence while decoding one document.
func decodeNodeAuditSettings(data []byte) (nodeAuditSettings, error) {
	var document map[string]yaml.Node
	var settings nodeAuditSettings
	if err := util.DecodeYAMLDocument(data, &document, util.YAMLDocumentOptions{AllowEmpty: true}); err != nil {
		return settings, err
	}
	include, includeDefined := document["include"]
	settings.includeDefined = includeDefined
	if err := include.Decode(&settings.Include); err != nil {
		return settings, fmt.Errorf("decode pnpm-workspace.yaml include: %w", err)
	}
	audit, auditDefined := document["audit"]
	auditConfig, auditConfigDefined := document["auditConfig"]
	settings.auditDefined = auditDefined || auditConfigDefined
	if err := audit.Decode(&settings.Audit); err != nil {
		return settings, fmt.Errorf("decode audit: %w", err)
	}
	if err := auditConfig.Decode(&settings.AuditConfig); err != nil {
		return settings, fmt.Errorf("decode auditConfig: %w", err)
	}
	return settings, nil
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
		settings, err = decodeNodeAuditSettings(data)
		if err != nil {
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

// resolveNodeAuditIncludes reserves list owners before reading audit owners and bounded wildcard files.
func resolveNodeAuditIncludes(dir *os.Root, settings *nodeAuditSettings) error {
	if !settings.includeDefined {
		return nil
	}
	includes, err := decodeNodeAuditIncludes(settings.Include)
	if err != nil {
		return err
	}
	owner, err := findNodeAuditIncludeOwner(settings, includes)
	if err != nil {
		return err
	}
	for i := 0; i < len(includes) && i < maxNodeAuditIncludes; i++ {
		entry := includes[i]
		if !entry.wildcard && !entry.audit {
			continue
		}
		if err := applyNodeAuditInclude(dir, entry, settings, &owner); err != nil {
			return err
		}
	}
	return nil
}

// applyNodeAuditInclude merges one readable audit owner after enforcing wildcard ownership.
func applyNodeAuditInclude(dir *os.Root, entry nodeAuditInclude, settings *nodeAuditSettings, owner *string) error {
	included, err := readIncludedNodeAuditFile(dir, entry.path)
	if err != nil {
		return err
	}
	if entry.wildcard && !included.auditDefined {
		return nil
	}
	if err := assignNodeAuditIncludeOwner(entry.path, owner); err != nil {
		return err
	}
	settings.Audit, settings.AuditConfig = included.Audit, included.AuditConfig
	return nil
}

// decodeNodeAuditIncludes bounds the decoded source mapping before examining any delegation.
func decodeNodeAuditIncludes(value any) ([]nodeAuditInclude, error) {
	sources, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("pnpm-workspace.yaml include must be a source mapping")
	}
	if len(sources) > maxNodeAuditIncludes {
		return nil, fmt.Errorf("pnpm-workspace.yaml include exceeds %d entries", maxNodeAuditIncludes)
	}
	paths := util.SortedKeys(sources)
	includes := make([]nodeAuditInclude, 0, len(paths))
	wildcards := 0
	for i := 0; i < len(paths) && i < maxNodeAuditIncludes; i++ {
		entry, err := parseNodeAuditInclude(paths[i], sources[paths[i]])
		if err != nil {
			return nil, err
		}
		if entry.wildcard {
			wildcards++
		}
		if wildcards > maxNodeAuditWildcardFiles {
			return nil, fmt.Errorf("pnpm-workspace.yaml include exceeds %d '*' files at %q", maxNodeAuditWildcardFiles, entry.path)
		}
		includes = append(includes, entry)
	}
	return includes, nil
}

// parseNodeAuditInclude accepts only a string wildcard or a list of string setting names.
func parseNodeAuditInclude(path string, value any) (nodeAuditInclude, error) {
	entry := nodeAuditInclude{path: path}
	if text, ok := value.(string); ok && text == "*" {
		entry.wildcard = true
	} else {
		audit, err := nodeAuditIncludeList(path, value)
		if err != nil {
			return entry, err
		}
		entry.audit = audit
	}
	if err := validateNodeAuditIncludeSource(path, entry.wildcard); err != nil {
		return entry, err
	}
	return entry, nil
}

// nodeAuditIncludeList validates decoded names without scalar-to-string coercion.
// The workspace byte limit bounds each flat list; aliases substitute items, never splice lists.
func nodeAuditIncludeList(path string, value any) (bool, error) {
	fields, ok := value.([]any)
	if !ok {
		return false, fmt.Errorf("pnpm-workspace.yaml include %q: expected a list of setting names or '*'", path)
	}
	audit := false
	for i := 0; i < len(fields) && i < nodemanifest.MaxManifestBytes; i++ {
		field, ok := fields[i].(string)
		if !ok {
			return false, fmt.Errorf("pnpm-workspace.yaml include %q: setting names must be strings", path)
		}
		if field == "audit" || field == "auditConfig" {
			audit = true
		}
	}
	return audit, nil
}

// isLocalNodeAuditInclude identifies pnpm's two relative file-source prefixes.
func isLocalNodeAuditInclude(path string) bool {
	return strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../")
}

// validateNodeAuditIncludeSource checks local file syntax without reading unrelated list delegations.
func validateNodeAuditIncludeSource(path string, wildcard bool) error {
	if !isLocalNodeAuditInclude(path) {
		if wildcard {
			return fmt.Errorf("pnpm-workspace.yaml include %q: '*' requires a local .yaml or .yml file starting with ./ or ../", path)
		}
		return nil
	}
	if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
		return fmt.Errorf("pnpm-workspace.yaml include %q must name a .yaml or .yml file", path)
	}
	if strings.ContainsAny(path, "*?[]") {
		return fmt.Errorf("pnpm-workspace.yaml include %q must name one local file, not a glob", path)
	}
	return nil
}

// findNodeAuditIncludeOwner reserves audit and its deprecated auditConfig spelling for one source.
func findNodeAuditIncludeOwner(settings *nodeAuditSettings, includes []nodeAuditInclude) (string, error) {
	owner := ""
	if settings.auditDefined {
		owner = "pnpm-workspace.yaml"
	}
	for i := 0; i < len(includes) && i < maxNodeAuditIncludes; i++ {
		if includes[i].audit {
			if err := assignNodeAuditIncludeOwner(includes[i].path, &owner); err != nil {
				return "", err
			}
		}
	}
	return owner, nil
}

// assignNodeAuditIncludeOwner rejects different sources claiming either spelling of the audit setting.
func assignNodeAuditIncludeOwner(path string, owner *string) error {
	if *owner != "" && *owner != path {
		return fmt.Errorf("pnpm-workspace.yaml audit/auditConfig setting must have a single owner: %q and %q", *owner, path)
	}
	*owner = path
	return nil
}

// readIncludedNodeAuditFile reads one repository-local single-document YAML file through the pinned root.
func readIncludedNodeAuditFile(dir *os.Root, path string) (nodeAuditSettings, error) {
	var included nodeAuditSettings
	if !isLocalNodeAuditInclude(path) {
		return included, fmt.Errorf("pnpm audit policy is delegated to config dependency %q, which the gate does not read; use a local policy file", path)
	}
	data, err := util.ReadLimitedIn(dir, strings.TrimPrefix(path, "./"), nodemanifest.MaxManifestBytes)
	if err != nil {
		return included, fmt.Errorf("read included pnpm audit policy %s: %w", path, err)
	}
	included, err = decodeNodeAuditSettings(data)
	if err != nil {
		return included, fmt.Errorf("parse included pnpm audit policy %s: %w", path, err)
	}
	if included.includeDefined {
		return included, fmt.Errorf("included pnpm audit policy %s must not define include", path)
	}
	return included, nil
}
