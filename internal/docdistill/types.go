// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
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

// DistillOptions configures the doc harvesting and compression behavior.
type DistillOptions struct {
	MaxTokensPerPackage int  `json:"max_tokens_per_package"`
	OfflineOnly         bool `json:"offline_only"`
	ForceRefresh        bool `json:"force_refresh"`
	IncludeTransitive   bool `json:"include_transitive"`
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
}
