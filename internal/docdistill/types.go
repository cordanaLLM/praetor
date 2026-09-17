// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"maps"
	"slices"
	"strings"
	"time"
)

// PackageKind designates the ecosystem or manifest source of a package.
type PackageKind string

const (
	KindGoModule     PackageKind = "go-module"
	KindNodePackage  PackageKind = "node-package"
	KindGitHubAction PackageKind = "github-action"
	KindFlavorTool   PackageKind = "flavor-tool"
)

// PackageRef identifies a declared dependency and its pinned version.
type PackageRef struct {
	Name       string      `json:"name"`
	Version    string      `json:"version"`
	Kind       PackageKind `json:"kind"`
	Manifest   string      `json:"manifest"`
	Direct     bool        `json:"direct"`
	Repository string      `json:"repository,omitempty"`
}

// DistilledDoc represents a high-density, token-compressed documentation extract.
type DistilledDoc struct {
	PackageName string      `json:"package_name"`
	Version     string      `json:"version"`
	Kind        PackageKind `json:"kind"`
	Summary     string      `json:"summary"`
	APISurface  []string    `json:"api_surface"`
	ConfigRules []string    `json:"config_rules"`
	Invariants  []string    `json:"invariants"`
	ContentHash string      `json:"content_hash"`
	TokenCount  int         `json:"token_count"`
	UpdatedAt   time.Time   `json:"updated_at"`
	RawMarkdown string      `json:"raw_markdown"`
}

// DocCatalog indexes all cached distilled documentation.
type DocCatalog struct {
	Version      string                  `json:"version"`
	LastSyncedAt time.Time               `json:"last_synced_at"`
	Packages     map[string]DistilledDoc `json:"packages"`
}

// maxCatalogEntries bounds a single pass over the catalog (HISS-02).
const maxCatalogEntries = 100000

// SortedKeys returns the catalog keys in a stable order, so a report over the catalog
// reads the same on two runs against an unchanged cache. Ranging the map directly makes
// every printed order, and every persisted fact order, an artefact of the runtime.
func (c *DocCatalog) SortedKeys() []string {
	if c == nil {
		return nil
	}
	keys := slices.Sorted(maps.Keys(c.Packages))
	if len(keys) > maxCatalogEntries {
		return keys[:maxCatalogEntries]
	}
	return keys
}

// Lookup returns the documentation sheet for a package name, deterministically.
//
// An exact PackageName match always wins; a path-suffix match ("yaml.v3" for
// "gopkg.in/yaml.v3") is only the answer when no exact match exists, so a suffix can no
// longer beat a package the caller named in full. When the same package is cached at
// several versions, the greatest catalog key in sorted order wins, which is stable across
// runs. That order is lexical over "name@version" rather than semantic: this repository
// has no version comparator to reuse, and adding a second one here is the duplication
// HISS-19 forbids.
func (c *DocCatalog) Lookup(name string) (DistilledDoc, bool) {
	if c == nil || name == "" {
		return DistilledDoc{}, false
	}
	keys := c.SortedKeys()
	var exact, suffix string
	for i := 0; i < len(keys); i++ {
		switch pkg := c.Packages[keys[i]].PackageName; {
		case pkg == name:
			exact = keys[i]
		case strings.HasSuffix(pkg, "/"+name):
			suffix = keys[i]
		}
	}
	if exact == "" && suffix == "" {
		return DistilledDoc{}, false
	}
	if exact != "" {
		return c.Packages[exact], true
	}
	return c.Packages[suffix], true
}

// DistillOptions configures the doc harvesting and compression behavior.
type DistillOptions struct {
	MaxTokensPerPackage int  `json:"max_tokens_per_package"`
	OfflineOnly         bool `json:"offline_only"`
	ForceRefresh        bool `json:"force_refresh"`
	IncludeTransitive   bool `json:"include_transitive"`
	// MaxPackages bounds how many declared dependencies SyncRepositoryDocs harvests in
	// one call (HISS-02). Zero or negative selects the package default (maxSyncPackages
	// in cache.go).
	MaxPackages int `json:"max_packages,omitempty"`
	// Timeout bounds the whole sync SyncRepositoryDocs enforces on itself. Zero keeps
	// the caller's own context deadline when it has one and applies defaultSyncTimeout
	// otherwise (HISS-02).
	Timeout time.Duration `json:"timeout,omitempty"`
}

// DefaultDistillOptions provides standard bounds.
func DefaultDistillOptions() DistillOptions {
	return DistillOptions{
		MaxTokensPerPackage: 400,
		OfflineOnly:         false,
		ForceRefresh:        false,
		IncludeTransitive:   false,
	}
}

// DocAuditResult summarizes documentation coverage across declared dependencies.
type DocAuditResult struct {
	TotalDeclared int          `json:"total_declared"`
	Documented    int          `json:"documented"`
	Missing       []PackageRef `json:"missing"`
	Stale         []PackageRef `json:"stale"`
	CoverageScore float64      `json:"coverage_score"`
	Passed        bool         `json:"passed"`
	// Status distinguishes a valid repository with no declared dependencies from
	// a successful coverage audit. It is not applicable when TotalDeclared is zero.
	Status string `json:"status,omitempty"`
}
