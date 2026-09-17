// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/bump"
	"github.com/cordanaLLM/praetor/internal/docdistill"
	"github.com/cordanaLLM/praetor/internal/mcp"
)

// versionAuditBudget bounds the upstream registry lookups of standards_version_audit
// (HISS-02); it must stay below the server's tool timeout.
const versionAuditBudget = 3 * time.Minute

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
				Description: "Repository root path (default: server root)",
			},
		},
		Required: []string{"package"},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		pkgName, err := argString(args, "package")
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		if pkgName == "" {
			return mcp.ErrorResult("package argument is required"), nil
		}
		repoPath, err := s.resolvePath(args, "path", s.rootDir)
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		if err := ctx.Err(); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("package docs lookup cancelled: %v", err)), nil
		}

		cat, err := docdistill.LoadCatalog(repoPath)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("failed loading doc catalog: %v", err)), nil
		}

		// Shared with the CLI: an exact package name beats a suffix match, and a package
		// cached at several versions resolves the same way on every call.
		if doc, found := cat.Lookup(pkgName); found {
			return mcp.TextResult(doc.RawMarkdown), nil
		}

		return mcp.ErrorResult(fmt.Sprintf("package '%s' not found in local catalog; run 'praetorctl docs sync' to harvest", pkgName)), nil
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
				Description: "Repository root path (default: server root)",
			},
			"prerelease": {
				Type:        "boolean",
				Description: "Include prerelease upgrade targets (default: false)",
			},
		},
	}

	handler := func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		repoPath, err := s.resolvePath(args, "path", s.rootDir)
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}
		prerelease, err := argBool(args, "prerelease", false)
		if err != nil {
			return mcp.ErrorResult(err.Error()), nil
		}

		auditCtx, cancel := context.WithTimeout(ctx, versionAuditBudget)
		defer cancel()

		report, err := bump.AuditCodebaseVersions(auditCtx, repoPath, prerelease)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("version audit failed: %v", err)), nil
		}

		return mcp.TextResult(formatVersionAudit(repoPath, report)), nil
	}

	// The audit queries module proxies and package registries and spawns the local
	// toolchain: open-world, read-only with respect to the repository.
	return mcp.NewOpenWorldTool(
		"standards_version_audit",
		"Audit all declared dependencies and workflow actions against upstream releases and runner deprecations",
		schema,
		handler,
		true,
		true,
	)
}

// formatVersionAudit renders the version audit report.
func formatVersionAudit(repoPath string, report *bump.VersionAuditReport) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "=== Codebase Version Audit: %s ===\n", repoPath)
	fmt.Fprintf(&sb, "Score: %.1f%% | Scanned: %d | Up to Date: %d | Deprecations: %d\n\n",
		report.ModernizationScore, report.TotalScanned, report.UpToDate, len(report.Deprecations))

	if len(report.Deprecations) > 0 {
		sb.WriteString("Deprecation Advisories:\n")
		for _, d := range report.Deprecations {
			fmt.Fprintf(&sb, "! [%s] %s: %s\n", d.Kind, d.Component, d.Details)
		}
		sb.WriteString("\n")
	}

	if len(report.PendingUpgrades) > 0 {
		sb.WriteString("Pending Upgrades:\n")
		for _, u := range report.PendingUpgrades {
			fmt.Fprintf(&sb, "- %s: %s -> %s (%s)\n", u.Package, u.CurrentVersion, u.TargetVersion, u.ManifestType)
		}
	}

	return sb.String()
}
