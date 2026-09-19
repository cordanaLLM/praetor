// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"context"
	"encoding/json"
	"errors"
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

	// maxSyncPackages bounds how many declared dependencies SyncRepositoryDocs harvests
	// in one call (HISS-02): ScanDeclaredDependencies can return as many refs as a
	// manifest declares, and each one performs a network harvest, so the loop needs its
	// own scalar ceiling rather than trusting every future caller's manifest to stay
	// small. DistillOptions.MaxPackages overrides it when a caller needs a different
	// bound (BUG-174).
	maxSyncPackages = 2000

	// defaultSyncTimeout bounds SyncRepositoryDocs when neither DistillOptions.Timeout
	// nor the caller's own context carries a deadline: the loop enforces its own ceiling
	// rather than only hoping a caller supplied one (HISS-02, BUG-174).
	defaultSyncTimeout = 10 * time.Minute
)

// ErrSyncTruncated reports that SyncRepositoryDocs stopped before covering every
// declared dependency, either because the reference count exceeded the configured
// package bound or because the sync deadline elapsed. Every package synced before the
// bound was hit is already in the returned catalog and on disk; nothing after it is
// missing silently.
var ErrSyncTruncated = errors.New("docdistill: sync truncated before covering every declared dependency")

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
	// Atomic (BUG-447): WriteFileSecure truncates catPath in place before writing, so a
	// process interrupted mid-write leaves a zero-length or partial catalog. The rename
	// below only ever replaces catPath with a fully written file.
	if err := util.WriteFileAtomic(catPath, data, cacheFilePerm); err != nil {
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
	// Atomic (BUG-447): shared with SaveCatalog so a distilled doc can never be left
	// truncated by an interrupted write either.
	if err := util.WriteFileAtomic(filePath, []byte(doc.RawMarkdown), cacheFilePerm); err != nil {
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

// syncContext derives the deadline SyncRepositoryDocs enforces on itself (HISS-02): an
// explicit timeout wins, a caller deadline is preserved, and a deadline-free context
// receives defaultSyncTimeout. Mirrors internal/hiss's scanContext so a network/IO loop
// never depends solely on a caller remembering to bound it.
func syncContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, defaultSyncTimeout)
}

// SyncRepositoryDocs scans declared dependencies, harvests missing documentation, and
// compresses it.
//
// The harvest loop carries two self-enforced bounds (HISS-02, BUG-174): a scalar cap on
// the number of refs it processes (opts.MaxPackages, or maxSyncPackages when unset) and
// an overall deadline (opts.Timeout, or the caller's own deadline, or
// defaultSyncTimeout) derived by syncContext independently of whatever the caller
// happens to pass. Hitting either bound stops the loop, saves what was synced so far,
// and returns it wrapped in ErrSyncTruncated rather than silently reporting a partial
// catalog as complete.
func SyncRepositoryDocs(ctx context.Context, repoPath string, opts DistillOptions) (*DocCatalog, error) {
	refs, err := ScanDeclaredDependencies(ctx, repoPath, opts.IncludeTransitive)
	if err != nil {
		return nil, fmt.Errorf("sync docs: manifest scan failed: %w", err)
	}

	cat, err := LoadCatalog(repoPath)
	if err != nil {
		return nil, err
	}

	syncCtx, cancel := syncContext(ctx, opts.Timeout)
	defer cancel()

	bound := opts.MaxPackages
	if bound <= 0 {
		bound = maxSyncPackages
	}
	limit := len(refs)
	truncated := false
	if limit > bound {
		limit = bound
		truncated = true
	}

	for i := 0; i < limit; i++ {
		if syncCtx.Err() != nil {
			truncated, limit = true, i
			break
		}
		syncErr := syncOnePackage(syncCtx, repoPath, refs[i], cat, opts)
		if syncErr == nil {
			continue
		}
		if !stoppedByBound(syncErr) {
			return nil, syncErr
		}
		truncated, limit = true, i
		break
	}

	if err := SaveCatalog(repoPath, cat); err != nil {
		return nil, err
	}
	if truncated {
		return cat, fmt.Errorf("%w: synced %d of %d declared dependencies", ErrSyncTruncated, limit, len(refs))
	}
	return cat, nil
}

// stoppedByBound reports whether a harvest failed because the sync deadline elapsed or
// the caller cancelled, rather than because the harvest itself failed.
//
// The deadline can elapse inside a harvest as easily as between two of them: whether the
// loop notices at its own boundary depends on the platform's timer granularity, which on
// windows-latest is coarse enough to land mid-package. Both are the same event -- the
// bound stopped the sync -- so both save what was harvested and report ErrSyncTruncated,
// which is what SyncRepositoryDocs documents. Any other failure is a real error and is
// returned as one.
func stoppedByBound(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// syncOnePackage harvests, compresses, and writes documentation for one declared
// dependency into cat, skipping it when a cached entry already exists and the
// caller has not asked to force a refresh. Extracted from SyncRepositoryDocs to
// keep the loop body's branching out of the outer function's cyclomatic
// complexity (HISS-04).
func syncOnePackage(ctx context.Context, repoPath string, ref PackageRef, cat *DocCatalog, opts DistillOptions) error {
	key := makeDocKey(ref.Name, ref.Version)
	if _, exists := cat.Packages[key]; exists && !opts.ForceRefresh {
		return nil
	}

	raw, harvestErr := HarvestDocumentation(ctx, ref, opts.OfflineOnly)
	if harvestErr != nil {
		return fmt.Errorf("harvest %s@%s: %w", ref.Name, ref.Version, harvestErr)
	}

	distilled := CompressDocumentation(ref, raw, opts)
	cat.Packages[key] = *distilled

	if writeErr := writeDistilledDoc(repoPath, distilled); writeErr != nil {
		return fmt.Errorf("failed to write distilled doc for %s: %w", ref.Name, writeErr)
	}
	return nil
}

// AuditDocumentationCoverage evaluates the ratio of declared dependencies with active distilled docs.
//
// It takes the same options as SyncRepositoryDocs so the two cannot disagree
// about what a repository declares. It previously hardcoded them, which meant a
// flag accepted by `docs sync` had no equivalent on `docs audit` and the
// coverage figure was not reproducible across the two (issue #96).
func AuditDocumentationCoverage(ctx context.Context, repoPath string, opts DistillOptions) (*DocAuditResult, error) {
	info, err := os.Stat(repoPath)
	if err != nil {
		return nil, fmt.Errorf("audit docs: invalid repository root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("audit docs: repository root %q is not a directory", repoPath)
	}
	refs, err := ScanDeclaredDependencies(ctx, repoPath, opts.IncludeTransitive)
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
	score := 0.0
	if total > 0 {
		score = (float64(documented) / float64(total)) * 100.0
	}

	status := "observed"
	passed := len(missing) == 0
	if total == 0 {
		status = "not_applicable"
		passed = false
	}
	return &DocAuditResult{
		TotalDeclared: total,
		Documented:    documented,
		Missing:       missing,
		CoverageScore: score,
		Passed:        passed,
		Status:        status,
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
