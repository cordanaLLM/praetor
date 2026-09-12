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
	// cacheDirPerm is the mode of the docs cache directories.
	cacheDirPerm os.FileMode = 0o755
	// cacheFilePerm is the mode of the catalog and distilled markdown files.
	cacheFilePerm os.FileMode = 0o644
)

// cachePath confines a cache-relative path to repoPath.
func cachePath(repoPath, rel string) (string, error) {
	path, err := util.ConfinePath(repoPath, rel)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", rel, err)
	}
	return path, nil
}

// LoadCatalog reads the doc catalog from the repo's .workingdir/docs/catalog.json.
func LoadCatalog(repoPath string) (*DocCatalog, error) {
	catPath, err := cachePath(repoPath, CatalogFileRel)
	if err != nil {
		return nil, err
	}
	if !util.FileExists(catPath) {
		return &DocCatalog{
			Version:      CatalogVersion,
			LastSyncedAt: time.Now().UTC(),
			Packages:     make(map[string]DistilledDoc),
		}, nil
	}

	// #nosec G304 -- catPath is confined to repoPath by ConfinePath.
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
	if cat == nil {
		return fmt.Errorf("doc catalog cannot be nil")
	}
	dir, err := cachePath(repoPath, DocsDirRel)
	if err != nil {
		return err
	}
	if err := util.MkdirSecure(dir, cacheDirPerm); err != nil {
		return fmt.Errorf("failed to create docs dir: %w", err)
	}

	cat.LastSyncedAt = time.Now().UTC()
	data, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal doc catalog: %w", err)
	}

	catPath, err := cachePath(repoPath, CatalogFileRel)
	if err != nil {
		return err
	}
	if err := util.WriteFileSecure(catPath, data, cacheFilePerm); err != nil {
		return fmt.Errorf("failed to write doc catalog: %w", err)
	}
	return nil
}

// writeDistilledDoc writes the distilled markdown body of doc into the cache directory.
// It is the single writer shared by SaveCachedDoc and SyncRepositoryDocs.
func writeDistilledDoc(repoPath string, doc *DistilledDoc) error {
	distilledDir, err := cachePath(repoPath, DistilledDirRel)
	if err != nil {
		return err
	}
	if err := util.MkdirSecure(distilledDir, cacheDirPerm); err != nil {
		return fmt.Errorf("failed creating distilled dir: %w", err)
	}
	filename := sanitizeDocFilename(doc.PackageName, doc.Version)
	filePath, err := cachePath(repoPath, filepath.Join(DistilledDirRel, filename))
	if err != nil {
		return err
	}
	if err := util.WriteFileSecure(filePath, []byte(doc.RawMarkdown), cacheFilePerm); err != nil {
		return fmt.Errorf("failed writing distilled doc file: %w", err)
	}
	return nil
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
	if doc == nil {
		return fmt.Errorf("distilled doc cannot be nil")
	}
	if err := writeDistilledDoc(repoPath, doc); err != nil {
		return err
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

		if writeErr := writeDistilledDoc(repoPath, distilled); writeErr != nil {
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
	score := 100.0
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
