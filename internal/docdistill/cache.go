// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	CatalogVersion  = "v1"
	DocsDirRel      = ".workingdir/docs"
	DistilledDirRel = ".workingdir/docs/distilled"
	CatalogFileRel  = ".workingdir/docs/catalog.json"
)

// LoadCatalog reads the doc catalog from the repo's .workingdir/docs/catalog.json.
func LoadCatalog(repoPath string) (*DocCatalog, error) {
	catPath := filepath.Join(repoPath, CatalogFileRel)
	if !util.FileExists(catPath) {
		return &DocCatalog{
			Version:      CatalogVersion,
			LastSyncedAt: time.Now().UTC(),
			Packages:     make(map[string]DistilledDoc),
		}, nil
	}

	data, err := os.ReadFile(catPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read doc catalog: %w", err)
	}

	var cat DocCatalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, fmt.Errorf("failed to parse doc catalog: %w", err)
	}
	if cat.Packages == nil {
		cat.Packages = make(map[string]DistilledDoc)
	}
	return &cat, nil
}

// SaveCatalog writes the doc catalog to the repo's .workingdir/docs/catalog.json.
func SaveCatalog(repoPath string, cat *DocCatalog) error {
	dir := filepath.Join(repoPath, DocsDirRel)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create docs dir: %w", err)
	}

	cat.LastSyncedAt = time.Now().UTC()
	data, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal doc catalog: %w", err)
	}

	catPath := filepath.Join(repoPath, CatalogFileRel)
	return os.WriteFile(catPath, data, 0644)
}

// GetCachedDoc retrieves a cached distilled doc if present.
func GetCachedDoc(repoPath, pkgName, version string) (*DistilledDoc, bool) {
	cat, err := LoadCatalog(repoPath)
	if err != nil {
		return nil, false
	}
	key := makeDocKey(pkgName, version)
	doc, exists := cat.Packages[key]
	if !exists {
		return nil, false
	}
	return &doc, true
}

// SaveCachedDoc writes a distilled doc to the local markdown cache and updates the catalog.
func SaveCachedDoc(repoPath string, doc *DistilledDoc) error {
	distilledDir := filepath.Join(repoPath, DistilledDirRel)
	if err := os.MkdirAll(distilledDir, 0755); err != nil {
		return fmt.Errorf("failed creating distilled dir: %w", err)
	}

	filename := sanitizeDocFilename(doc.PackageName, doc.Version)
	filePath := filepath.Join(distilledDir, filename)
	if err := os.WriteFile(filePath, []byte(doc.RawMarkdown), 0644); err != nil {
		return fmt.Errorf("failed writing distilled doc file: %w", err)
	}

	cat, err := LoadCatalog(repoPath)
	if err != nil {
		return err
	}
	key := makeDocKey(doc.PackageName, doc.Version)
	cat.Packages[key] = *doc
	return SaveCatalog(repoPath, cat)
}

// SyncRepositoryDocs scans declared dependencies, harvests missing documentation, and compresses it.
func SyncRepositoryDocs(ctx context.Context, repoPath string, opts DistillOptions) (*DocCatalog, error) {
	refs, err := ScanDeclaredDependencies(ctx, repoPath, opts.IncludeTransitive)
	if err != nil {
		return nil, fmt.Errorf("sync docs: manifest scan failed: %w", err)
	}

	cat, err := LoadCatalog(repoPath)
	if err != nil {
		return nil, err
	}

	for _, ref := range refs {
		key := makeDocKey(ref.Name, ref.Version)
		if _, exists := cat.Packages[key]; exists && !opts.ForceRefresh {
			continue
		}

		raw, harvestErr := HarvestDocumentation(ctx, ref, opts.OfflineOnly)
		if harvestErr != nil {
			raw = fmt.Sprintf("# %s@%s\n\nHarvesting error: %v\n", ref.Name, ref.Version, harvestErr)
		}

		distilled := CompressDocumentation(ref, raw, opts)
		cat.Packages[key] = *distilled

		distilledDir := filepath.Join(repoPath, DistilledDirRel)
		if mkErr := os.MkdirAll(distilledDir, 0755); mkErr != nil {
			return nil, fmt.Errorf("failed to create distilled dir: %w", mkErr)
		}
		filename := sanitizeDocFilename(ref.Name, ref.Version)
		if writeErr := os.WriteFile(filepath.Join(distilledDir, filename), []byte(distilled.RawMarkdown), 0644); writeErr != nil {
			return nil, fmt.Errorf("failed to write distilled doc for %s: %w", ref.Name, writeErr)
		}
	}

	if err := SaveCatalog(repoPath, cat); err != nil {
		return nil, err
	}
	return cat, nil
}

// AuditDocumentationCoverage evaluates the ratio of declared dependencies with active distilled docs.
func AuditDocumentationCoverage(ctx context.Context, repoPath string) (*DocAuditResult, error) {
	refs, err := ScanDeclaredDependencies(ctx, repoPath, false)
	if err != nil {
		return nil, fmt.Errorf("audit docs: manifest scan failed: %w", err)
	}

	cat, err := LoadCatalog(repoPath)
	if err != nil {
		return nil, err
	}

	var missing []PackageRef
	var documented int

	for _, ref := range refs {
		key := makeDocKey(ref.Name, ref.Version)
		if doc, exists := cat.Packages[key]; exists && doc.TokenCount > 0 {
			documented++
		} else {
			missing = append(missing, ref)
		}
	}

	total := len(refs)
	var score float64 = 100.0
	if total > 0 {
		score = (float64(documented) / float64(total)) * 100.0
	}

	return &DocAuditResult{
		TotalDeclared: total,
		Documented:    documented,
		Missing:       missing,
		CoverageScore: score,
		Passed:        len(missing) == 0,
	}, nil
}

func makeDocKey(pkgName, version string) string {
	return fmt.Sprintf("%s@%s", pkgName, version)
}

func sanitizeDocFilename(pkgName, version string) string {
	clean := strings.ReplaceAll(pkgName, "/", "_")
	clean = strings.ReplaceAll(clean, ":", "_")
	return fmt.Sprintf("%s@%s.md", clean, version)
}
