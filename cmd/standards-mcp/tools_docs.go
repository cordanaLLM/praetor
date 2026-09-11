// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/bump"
	"github.com/cordanaLLM/praetor/internal/docdistill"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

// createPackageDocsTool builds the standards_package_docs tool for fast local doc lookups.
func (s *Server) createPackageDocsTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"package": {
				Type:        "string",
				Description: "Exact package or action name (e.g. gopkg.in/yaml.v3 or actions/checkout)",
			},
			"path": {
				Type:        "string",
				Description: "Repository root path (default: workspace root)",
			},
		},
		Required: []string{"package"},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		pkgName, _ := args["package"].(string)
		if pkgName == "" {
			return mcp.ErrorResult("package argument is required"), nil
		}
		repoPath := s.resolvePath(args, "path", s.rootDir)

		cat, err := docdistill.LoadCatalog(repoPath)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("failed loading doc catalog: %v", err)), nil
		}

		for _, doc := range cat.Packages {
			if doc.PackageName == pkgName || strings.HasSuffix(doc.PackageName, "/"+pkgName) {
				return mcp.TextResult(doc.RawMarkdown), nil
			}
		}

		return mcp.ErrorResult(fmt.Sprintf("package '%s' not found in local catalog; run 'standardsctl docs sync' to harvest", pkgName)), nil
	}

	return mcp.NewReadOnlyTool(
		"standards_package_docs",
		"Retrieve token-compressed, authoritative API documentation and configuration rules for a declared project package or action",
		schema,
		handler,
	)
}

// createVersionAuditTool builds the standards_version_audit tool for detecting drift and deprecations.
func (s *Server) createVersionAuditTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]mcp.PropertySchema{
			"path": {
				Type:        "string",
				Description: "Repository root path (default: workspace root)",
			},
			"prerelease": {
				Type:        "boolean",
				Description: "Include prerelease upgrade targets (default: false)",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		repoPath := s.resolvePath(args, "path", s.rootDir)
		prerelease, _ := args["prerelease"].(bool)

		report, err := bump.AuditCodebaseVersions(ctx, repoPath, prerelease)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("version audit failed: %v", err)), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("=== Codebase Version Audit: %s ===\n", repoPath))
		sb.WriteString(fmt.Sprintf("Score: %.1f%% | Scanned: %d | Up to Date: %d | Deprecations: %d\n\n",
			report.ModernizationScore, report.TotalScanned, report.UpToDate, len(report.Deprecations)))

		if len(report.Deprecations) > 0 {
			sb.WriteString("Deprecation Advisories:\n")
			for _, d := range report.Deprecations {
				sb.WriteString(fmt.Sprintf("! [%s] %s: %s\n", d.Kind, d.Component, d.Details))
			}
			sb.WriteString("\n")
		}

		if len(report.PendingUpgrades) > 0 {
			sb.WriteString("Pending Upgrades:\n")
			for _, u := range report.PendingUpgrades {
				sb.WriteString(fmt.Sprintf("- %s: %s -> %s (%s)\n", u.Package, u.CurrentVersion, u.TargetVersion, u.ManifestType))
			}
		}

		return mcp.TextResult(sb.String()), nil
	}

	return mcp.NewReadOnlyTool(
		"standards_version_audit",
		"Audit all declared dependencies and workflow actions against upstream releases and runner deprecations",
		schema,
		handler,
	)
}
